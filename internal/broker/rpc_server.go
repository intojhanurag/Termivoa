package broker

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/gitchanges"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
	"github.com/intojhanurag/phone-terminal/internal/webpush"
)

const (
	defaultSessionTitle  = "Terminal"
	maxDefaultTitleBytes = 128
	terminalType         = "xterm-256color"
	terminalColorMode    = "truecolor"
)

// ManagementServerConfig binds the restricted local transport to the
// broker-owned runtime and database. Registry and Store must be the exact
// instances owned by the same broker generation.
type ManagementServerConfig struct {
	Registry         *Registry
	Store            *store.Store
	BrokerGeneration string
	HostBuild        string
	HostPlatform     store.Platform
	// Auth is the pairing and browser-credential authority. Without it,
	// gateway and local administration requests fail as host_unavailable.
	Auth *auth.Service
	// Push owns VAPID keys and push subscriptions. Without it, the browser
	// push requests fail as host_unavailable.
	Push *webpush.Notifier
	// Shutdown stops the broker's serve loop after an admin.shutdown result
	// is written. Without it, admin.shutdown fails as host_unavailable.
	Shutdown func()
	// WorktreeRoot holds the worktrees that session creates make, as
	// <root>/<repo>/<name>. Empty means <home>/termivoa/worktrees.
	WorktreeRoot string
	// Tailnet publishes this computer's Tailscale status for multi-computer
	// trust checks. Nil uses the installed tailscale CLI; it runs only when
	// an app origin, pairing via or computer list needs it. A monitor passed
	// here should set OnUpdate to the server's TrustUpdated.
	Tailnet *tailnet.Monitor
	// Development is the development profile: the gateway's loopback origins
	// then count as this computer's own.
	Development bool
	// Now is the clock for computer lists and origin probes. Nil is time.Now.
	Now func() time.Time
}

// ManagementServer dispatches session-management RPC after the local IPC
// peer check and protocol handshake. Requests are processed serially on each
// connection so results preserve request order; separate accepted connections
// may execute concurrently. Closing a gateway connection never closes the
// Registry, Store, or any managed session.
type ManagementServer struct {
	config ManagementServerConfig

	mu            sync.Mutex
	serving       bool
	stopping      bool
	connections   map[net.Conn]struct{}
	connectionsWG sync.WaitGroup

	uploads uploadLimits
	// changesWrites holds the session IDs with a commit or push running
	// (changes.go): one write per session, whoever asks.
	changesWrites sync.Map
	// worktreeCreates runs one worktree create at a time, so a replayed
	// operation finds the first one's receipt instead of making a second.
	worktreeCreates sync.Mutex
	// ownOrigin caches the configured origin, so authorization never reads
	// SQLite for it. It is refreshed whenever the origin is read or set.
	ownOrigin atomic.Pointer[string]
	// trustMu serializes sweepTrust; sweptStatus is the snapshot whose node
	// changes it already turned into revocations. trustWake asks watchTrust
	// for a sweep now (a new snapshot).
	trustMu     sync.Mutex
	sweptStatus *tailnet.Status
	trustWake   chan struct{}
}

// NewManagementServer validates that dispatch cannot accidentally cross a
// broker generation or use a different SQLite owner than the Registry.
func NewManagementServer(config ManagementServerConfig) (*ManagementServer, error) {
	if config.Registry == nil {
		return nil, errors.New("broker: management registry must not be nil")
	}
	if config.Store == nil {
		return nil, errors.New("broker: management store must not be nil")
	}
	if config.Registry.config.Store != config.Store {
		return nil, errors.New("broker: management registry and server must share one store")
	}
	if config.BrokerGeneration == "" || config.Registry.config.BrokerGeneration != config.BrokerGeneration {
		return nil, errors.New("broker: management broker generation does not match registry")
	}
	if len(config.HostBuild) == 0 || len(config.HostBuild) > 128 {
		return nil, errors.New("broker: management host build must be 1..128 bytes")
	}
	if config.HostPlatform == "" {
		switch runtime.GOOS {
		case "darwin":
			config.HostPlatform = store.PlatformDarwin
		case "windows":
			config.HostPlatform = store.PlatformWindows
		case "linux":
			config.HostPlatform = store.PlatformLinux
		default:
			return nil, fmt.Errorf("broker: management host platform %q is not supported", runtime.GOOS)
		}
	}
	if config.HostPlatform != store.PlatformDarwin && config.HostPlatform != store.PlatformWindows &&
		config.HostPlatform != store.PlatformLinux {
		return nil, errors.New("broker: management host platform must be darwin, windows, or linux")
	}
	if config.WorktreeRoot == "" {
		config.WorktreeRoot = defaultWorktreeRoot()
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	server := &ManagementServer{
		config: config, connections: make(map[net.Conn]struct{}), trustWake: make(chan struct{}, 1),
	}
	if server.config.Tailnet == nil {
		var lastRefused string
		server.config.Tailnet = tailnet.NewMonitor(tailnet.Config{
			Source: tailnet.CommandSource(func() string {
				path, refused := tailnet.FindBinary(runtime.GOOS, os.Getenv, exec.LookPath)
				// Log why a tailscale was refused once per change, so broker.log
				// explains a multi-computer outage without pterm doctor.
				if reasons := strings.Join(refused, "; "); reasons != lastRefused {
					lastRefused = reasons
					if reasons != "" {
						fmt.Fprintf(os.Stderr, "broker: %s\n", reasons)
					}
				}
				return path
			}),
			OnUpdate: server.TrustUpdated,
		})
	}
	return server, nil
}

// Serve accepts only BrokerListener connections, which have already passed
// the transport's current-user peer check. It owns the listener lifetime but
// deliberately does not own the broker Registry or Store lifetime.
func (server *ManagementServer) Serve(ctx context.Context, listener *ipc.BrokerListener) error {
	if ctx == nil {
		return errors.New("broker: management serve context must not be nil")
	}
	if listener == nil {
		return errors.New("broker: management listener must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	server.mu.Lock()
	if server.serving || server.stopping {
		server.mu.Unlock()
		return errors.New("broker: management server cannot be served more than once")
	}
	server.serving = true
	server.mu.Unlock()

	_, _ = server.pairingOrigin(ctx) // Warm the own-origin cache.
	stopTrust, trustDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(trustDone)
		server.watchTrust(ctx, stopTrust)
	}()
	defer func() {
		close(stopTrust)
		<-trustDone // A sweep must not outlive Serve and the store it reads.
	}()
	shutdown := sync.OnceFunc(func() {
		_ = listener.Close()
		server.stopConnections()
	})
	cancelDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		shutdown()
		close(cancelDone)
	})

	var acceptErr error
	for {
		connection, err := listener.Accept()
		if err != nil {
			acceptErr = err
			break
		}
		if !server.registerConnection(connection) {
			_ = connection.Close()
			break
		}
		go func() {
			defer server.unregisterConnection(connection)
			_ = server.serveConnection(ctx, connection)
		}()
	}
	if stopCancel() {
		shutdown()
	} else {
		<-cancelDone
	}
	server.connectionsWG.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("broker: management accept: %w", acceptErr)
}

func (server *ManagementServer) registerConnection(connection net.Conn) bool {
	server.mu.Lock()
	defer server.mu.Unlock()
	if server.stopping {
		return false
	}
	server.connections[connection] = struct{}{}
	server.connectionsWG.Add(1)
	return true
}

func (server *ManagementServer) unregisterConnection(connection net.Conn) {
	_ = connection.Close()
	server.mu.Lock()
	delete(server.connections, connection)
	server.mu.Unlock()
	server.connectionsWG.Done()
}

func (server *ManagementServer) stopConnections() {
	server.mu.Lock()
	server.stopping = true
	connections := slices.Collect(maps.Keys(server.connections))
	server.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

// serveConnection assumes connection came from BrokerListener.Accept. The
// handshake is mandatory even for same-user peers. A malformed or
// direction-invalid frame poisons this connection because no unambiguous,
// validated request ID exists for a correlated error; operational failures
// receive a typed broker.error and the connection remains usable.
func (server *ManagementServer) serveConnection(ctx context.Context, connection net.Conn) error {
	if connection == nil {
		return errors.New("broker: management connection must not be nil")
	}
	defer connection.Close()
	closeOnCancel := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer closeOnCancel()
	peer, err := ipc.ServerHandshake(
		ctx, connection, server.config.BrokerGeneration, server.config.HostBuild,
	)
	if err != nil {
		return err
	}
	reader, err := ipc.NewFrameReader(connection, protocol.MaxInboundMessageBytes)
	if err != nil {
		return err
	}
	writer, err := ipc.NewFrameWriter(connection, protocol.MaxOutboundMessageBytes)
	if err != nil {
		return err
	}
	first := true
	for {
		data, err := reader.ReadFrame()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if first {
			first = false
			if hello, decodeErr := protocol.DecodeClient(data); decodeErr == nil && hello.Type == protocol.Hello {
				return server.serveSessionConnection(ctx, connection, reader, peer, hello, nil)
			}
		}
		// The peer role selects the schema. A gateway can reach only
		// credential-carrying gateway RPC; local administration and
		// unauthenticated session management remain CLI-only.
		if peer.ClientKind == protocol.Gateway {
			// An upload owns the rest of its dedicated connection.
			if request, err := protocol.DecodeGatewayRPCRequest(data); err == nil && request.Type == protocol.GatewayUploadBegin {
				return server.serveUpload(ctx, connection, reader, writer, request)
			}
			binding, err := server.serveGatewayRequest(ctx, writer, data)
			if err != nil || binding == nil {
				if err != nil {
					return err
				}
				continue
			}
			return server.serveBoundLiveStream(ctx, connection, reader, peer, binding)
		}
		if peer.ClientKind != protocol.CLI {
			return errors.New("broker: unsupported local peer role")
		}
		if isAdminRPC(data) {
			if err := server.serveAdminRequest(ctx, writer, data); err != nil {
				return err
			}
			continue
		}
		request, err := protocol.DecodeBrokerRPCRequest(data)
		if err != nil {
			return err
		}
		responseType, payload, dispatchErr := server.dispatch(ctx, request)
		var response []byte
		if dispatchErr == nil {
			response, err = encodeManagementResponse(request, responseType, payload)
			if err != nil {
				dispatchErr = err
			}
		}
		if dispatchErr != nil {
			response, err = encodeManagementError(request, classifyManagementError(dispatchErr))
		}
		if err != nil {
			return err
		}
		if err := writer.WriteFrame(response); err != nil {
			return err
		}
	}
}

func (server *ManagementServer) dispatch(
	ctx context.Context,
	request protocol.BrokerRPCEnvelope,
) (protocol.BrokerRPCMessageType, any, error) {
	switch request.Type {
	case protocol.SessionListRequest:
		payload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionListRequestPayload](request)
		if err != nil {
			return "", nil, err
		}
		result, err := server.listSessions(ctx, payload)
		return protocol.SessionListResult, result, err
	case protocol.SessionCreateRequest:
		payload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionCreateRequestPayload](request)
		if err != nil {
			return "", nil, err
		}
		result, err := server.createSession(ctx, payload)
		return protocol.SessionCreateResult, result, err
	case protocol.SessionRenameRequest:
		payload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionRenameRequestPayload](request)
		if err != nil {
			return "", nil, err
		}
		result, err := server.renameSession(ctx, request, payload)
		return protocol.SessionRenameResult, result, err
	case protocol.SessionTerminateRequest:
		payload, err := protocol.DecodeBrokerRPCPayload[protocol.BrokerRPCSessionTerminateRequestPayload](request)
		if err != nil {
			return "", nil, err
		}
		result, err := server.terminateSession(ctx, request, payload)
		return protocol.SessionTerminateResult, result, err
	case protocol.SessionStatusRequest:
		result, err := server.sessionStatus(ctx, request)
		return protocol.SessionStatusResult, result, err
	default:
		return "", nil, errors.New("broker: unsupported management request")
	}
}

func (server *ManagementServer) listSessions(
	ctx context.Context,
	payload protocol.BrokerRPCSessionListRequestPayload,
) (protocol.BrokerRPCSessionListResultPayload, error) {
	offset, err := decodeSessionCursor(payload.Cursor)
	if err != nil {
		return protocol.BrokerRPCSessionListResultPayload{}, err
	}
	sessions, err := server.config.Store.ListSessions(ctx)
	if err != nil {
		return protocol.BrokerRPCSessionListResultPayload{}, err
	}
	if offset > len(sessions) {
		return protocol.BrokerRPCSessionListResultPayload{}, errInvalidSessionCursor
	}
	end := offset + int(payload.Limit)
	if end > len(sessions) {
		end = len(sessions)
	}
	result := protocol.BrokerRPCSessionListResultPayload{
		Sessions: make([]protocol.BrokerRPCSessionRecord, 0, end-offset),
	}
	for _, session := range sessions[offset:end] {
		result.Sessions = append(result.Sessions, server.withPresence(ctx, managementSessionRecord(session), ""))
	}
	if end < len(sessions) {
		cursor := encodeSessionCursor(end)
		result.NextCursor = &cursor
	}
	return result, nil
}

func (server *ManagementServer) createSession(
	ctx context.Context,
	payload protocol.BrokerRPCSessionCreateRequestPayload,
) (protocol.BrokerRPCSessionMutationResultPayload, error) {
	if payload.Worktree != nil {
		// git must not be cut short by a closed request; a replay (checked
		// again under the lock) never makes a second worktree.
		ctx = context.WithoutCancel(ctx)
		server.worktreeCreates.Lock()
		defer server.worktreeCreates.Unlock()
	}
	replayed, found, err := server.config.Registry.ReplayCreate(
		ctx, payload.OperationID, payload.ProjectID, payload.ShellProfileID, payload.Worktree,
	)
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	if found {
		return protocol.BrokerRPCSessionMutationResultPayload{
			OperationID: payload.OperationID, Session: server.withWorktree(managementMutationSessionRecord(replayed.Snapshot)),
		}, nil
	}
	project, err := server.config.Store.GetProject(ctx, payload.ProjectID)
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	if project.DisabledAt != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, store.ErrProjectMissing
	}
	profile, err := server.config.Store.GetShellProfile(ctx, payload.ShellProfileID)
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	if profile.DisabledAt != nil || !server.profileMatchesHost(profile.Platform) {
		return protocol.BrokerRPCSessionMutationResultPayload{}, store.ErrShellProfileMissing
	}
	title, directory := project.DisplayName, project.CanonicalPath
	if !validManagementTitle(title) {
		title = defaultSessionTitle
	}
	var worktree gitchanges.Created
	if payload.Worktree != nil {
		if worktree, err = server.prepareWorktree(ctx, *payload.Worktree); err != nil {
			return protocol.BrokerRPCSessionMutationResultPayload{}, err
		}
		title, directory = payload.Worktree.Name, worktree.Path
	}
	created, err := server.config.Registry.Create(ctx, CreateSessionRequest{
		OperationID: payload.OperationID, Title: title,
		ProjectID: payload.ProjectID, ShellProfileID: payload.ShellProfileID, Worktree: payload.Worktree,
		Process: ptyhost.Spec{
			Executable: profile.ExecutablePath,
			Args:       withDirectoryHook(profile.Platform, profile.ExecutablePath, profile.Arguments),
			Directory:  directory,
			Env:        []string{"TERM=" + terminalType, "COLORTERM=" + terminalColorMode},
			Columns:    ptyhost.DefaultColumns,
			Rows:       ptyhost.DefaultRows,
		},
	})
	if err != nil {
		if payload.Worktree != nil && created.Snapshot.ID == "" {
			// No session started there: do not leave a worktree nobody asked to keep.
			err = errors.Join(err, rollBackCreate(ctx, worktree))
		}
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	return protocol.BrokerRPCSessionMutationResultPayload{
		OperationID: payload.OperationID, Session: server.withWorktree(managementMutationSessionRecord(created.Snapshot)),
	}, nil
}

func (server *ManagementServer) renameSession(
	ctx context.Context,
	request protocol.BrokerRPCEnvelope,
	payload protocol.BrokerRPCSessionRenameRequestPayload,
) (protocol.BrokerRPCSessionMutationResultPayload, error) {
	result, err := server.config.Registry.Rename(ctx, RenameSessionRequest{
		OperationID: payload.OperationID, SessionID: *request.SessionID,
		Generation: *request.Generation, Title: payload.Title,
	})
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	return protocol.BrokerRPCSessionMutationResultPayload{
		OperationID: payload.OperationID, Session: managementMutationSessionRecord(result.Snapshot),
	}, nil
}

func (server *ManagementServer) terminateSession(
	ctx context.Context,
	request protocol.BrokerRPCEnvelope,
	payload protocol.BrokerRPCSessionTerminateRequestPayload,
) (protocol.BrokerRPCSessionMutationResultPayload, error) {
	result, err := server.config.Registry.Terminate(ctx, TerminateSessionRequest{
		OperationID: payload.OperationID, SessionID: *request.SessionID,
		Generation: *request.Generation,
	})
	if err != nil {
		return protocol.BrokerRPCSessionMutationResultPayload{}, err
	}
	return protocol.BrokerRPCSessionMutationResultPayload{
		OperationID: payload.OperationID, Session: managementMutationSessionRecord(result.Snapshot),
	}, nil
}

func (server *ManagementServer) sessionStatus(
	ctx context.Context,
	request protocol.BrokerRPCEnvelope,
) (protocol.BrokerRPCSessionResultPayload, error) {
	metadata, err := server.config.Store.GetSession(ctx, *request.SessionID)
	if err != nil {
		return protocol.BrokerRPCSessionResultPayload{}, err
	}
	if metadata.Generation != *request.Generation {
		return protocol.BrokerRPCSessionResultPayload{}, store.ErrGenerationChanged
	}
	return protocol.BrokerRPCSessionResultPayload{Session: server.withPresence(ctx, managementSessionRecord(metadata), "")}, nil
}

// withPresence adds live, non-secret presence computed at request time.
// deviceID is the requesting browser device; it is empty on local CLI paths,
// where controllerIsYou, lastOutputAt, preview and agent stay absent so terminal
// content never appears in CLI output. A session that is not live in this
// broker, or that already exited or was lost while still registered, keeps
// every presence field absent.
func (server *ManagementServer) withPresence(
	ctx context.Context, record protocol.BrokerRPCSessionRecord, deviceID string,
) protocol.BrokerRPCSessionRecord {
	if record.State == protocol.Exited || record.State == protocol.Lost {
		return record
	}
	session, err := server.config.Registry.Get(record.ID)
	if err != nil {
		return record
	}
	browser := deviceID != ""
	presence, err := session.Presence(ctx, browser)
	if err != nil {
		return record
	}
	controlled := presence.ControllerDevice != ""
	viewers := int64(presence.Viewers)
	record.Controlled, record.ViewerCount = &controlled, &viewers
	if browser {
		mine := controlled && presence.ControllerDevice == deviceID
		record.ControllerIsYou = &mine
		record.Previews = presence.Previews
		record.Cwd = cwdPayload(presence.Cwd)
		if !presence.LastOutput.IsZero() {
			value := presence.LastOutput.UTC().Format(millisecondRFC3339)
			record.LastOutputAt = &value
		}
		if presence.Preview != "" {
			record.Preview = &presence.Preview
		}
		record.Agent = agentPayload(presence.Agent)
	}
	record.Worktree = server.sessionWorktree(session)
	return record
}

const millisecondRFC3339 = "2006-01-02T15:04:05.000Z07:00"

func (server *ManagementServer) profileMatchesHost(platform store.Platform) bool {
	if server.config.HostPlatform == store.PlatformWindows {
		return platform == store.PlatformWindows || platform == store.PlatformWSL
	}
	return platform == server.config.HostPlatform
}

func managementSessionRecord(metadata store.SessionMetadata) protocol.BrokerRPCSessionRecord {
	record := protocol.BrokerRPCSessionRecord{
		ID: metadata.ID, Generation: metadata.Generation, Title: metadata.Title,
		ProjectID: metadata.ProjectID, ShellProfileID: metadata.ShellProfileID,
		State: protocol.State(metadata.State), Columns: int64(metadata.Columns), Rows: int64(metadata.Rows),
		CreatedAt: metadata.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: metadata.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if metadata.StartedAt != nil {
		value := metadata.StartedAt.UTC().Format(time.RFC3339Nano)
		record.StartedAt = &value
	}
	if metadata.EndedAt != nil {
		value := metadata.EndedAt.UTC().Format(time.RFC3339Nano)
		record.EndedAt = &value
	}
	if metadata.ExitCode != nil {
		value := int64(*metadata.ExitCode)
		record.ExitCode = &value
	}
	if metadata.TitleCustom {
		record.TitleIsCustom = &metadata.TitleCustom
	}
	return record
}

func managementMutationSessionRecord(snapshot store.MutationSessionSnapshot) protocol.BrokerRPCSessionRecord {
	record := protocol.BrokerRPCSessionRecord{
		ID: snapshot.ID, Generation: snapshot.Generation, Title: snapshot.Title,
		ProjectID: snapshot.ProjectID, ShellProfileID: snapshot.ShellProfileID,
		State: protocol.State(snapshot.State), Columns: int64(snapshot.Columns), Rows: int64(snapshot.Rows),
		CreatedAt: snapshot.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: snapshot.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if snapshot.HasStartedAt {
		value := snapshot.StartedAt.UTC().Format(time.RFC3339Nano)
		record.StartedAt = &value
	}
	if snapshot.HasEndedAt {
		value := snapshot.EndedAt.UTC().Format(time.RFC3339Nano)
		record.EndedAt = &value
	}
	if snapshot.HasExitCode {
		value := int64(snapshot.ExitCode)
		record.ExitCode = &value
	}
	if snapshot.TitleCustom {
		record.TitleIsCustom = &snapshot.TitleCustom
	}
	return record
}

type managementFailure struct {
	code    protocol.ErrorCode
	message string
}

var errInvalidSessionCursor = errors.New("broker: invalid session-list cursor")

func (server *ManagementServer) now() time.Time {
	return server.config.Now().UTC().Truncate(time.Millisecond)
}

func classifyManagementError(err error) managementFailure {
	if failure, ok := classifyAuthError(err); ok {
		return failure
	}
	var addFailure *gitchanges.AddError
	switch {
	case errors.As(err, &addFailure):
		return managementFailure{protocol.WorktreeFailed, worktreeAddMessage(addFailure.Output)}
	case errors.Is(err, errWorktreeSource):
		return managementFailure{protocol.SessionMissing, "the session the worktree is made from is not running"}
	case errors.Is(err, store.ErrProjectMissing):
		return managementFailure{protocol.ProjectMissing, "saved project is unavailable"}
	case errors.Is(err, store.ErrShellProfileMissing):
		return managementFailure{protocol.ShellProfileMissing, "saved shell profile is unavailable"}
	case errors.Is(err, ErrOperationConflict), errors.Is(err, store.ErrOperationConflict):
		return managementFailure{protocol.OperationConflict, "operation ID conflicts with an earlier request"}
	case errors.Is(err, ErrSessionMissing), errors.Is(err, store.ErrSessionMissing):
		return managementFailure{protocol.SessionMissing, "session does not exist"}
	case errors.Is(err, ErrGenerationChanged), errors.Is(err, store.ErrGenerationChanged):
		return managementFailure{protocol.GenerationChanged, "session generation changed"}
	case errors.Is(err, ErrSessionRetired):
		return managementFailure{protocol.SessionLost, "session process is no longer live"}
	case errors.Is(err, ErrSessionLimit), errors.Is(err, ErrOperationLimit),
		errors.Is(err, store.ErrOperationReceiptLimit):
		return managementFailure{protocol.LimitExceeded, "broker resource limit reached"}
	case errors.Is(err, gitchanges.ErrWorktreeExists):
		return managementFailure{protocol.WorktreeExists, "a worktree or branch with this name already exists"}
	case errors.Is(err, gitchanges.ErrNotRepository):
		return managementFailure{protocol.NotARepository, "the session folder is not in a git repository"}
	case errors.Is(err, gitchanges.ErrWorktreeLimit):
		return managementFailure{protocol.LimitExceeded, "this repository already has the most worktrees allowed"}
	case errors.Is(err, gitchanges.ErrWorktreeBase):
		return managementFailure{protocol.InvalidRequest, "the base is not a branch of this repository"}
	case errors.Is(err, gitchanges.ErrWorktreeName):
		return managementFailure{protocol.InvalidRequest, "the worktree or repository name is not allowed"}
	case errors.Is(err, ErrOperationID), errors.Is(err, errInvalidSessionCursor):
		return managementFailure{protocol.InvalidRequest, "request is invalid"}
	case errors.Is(err, ErrOperationResultUnavailable), errors.Is(err, store.ErrMutationResultUnavailable):
		return managementFailure{protocol.InternalError,
			"operation completed, but its original result is unavailable after a local data upgrade"}
	case errors.Is(err, ErrRegistryClosed), errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return managementFailure{protocol.HostUnavailable, "broker is unavailable"}
	default:
		return managementFailure{protocol.InternalError, "broker could not complete the request"}
	}
}

func encodeManagementResponse(
	request protocol.BrokerRPCEnvelope,
	responseType protocol.BrokerRPCMessageType,
	payload any,
) ([]byte, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("broker: encode management payload: %w", err)
	}
	response := protocol.BrokerRPCEnvelope{
		Version: protocol.Version, Type: responseType, RequestID: request.RequestID,
		Payload: payloadJSON,
	}
	if responseType == protocol.SessionRenameResult || responseType == protocol.SessionTerminateResult ||
		responseType == protocol.SessionStatusResult {
		response.SessionID = request.SessionID
		response.Generation = request.Generation
	}
	data, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("broker: encode management response: %w", err)
	}
	if _, err := protocol.DecodeBrokerRPCResponse(data); err != nil {
		return nil, fmt.Errorf("broker: validate management response: %w", err)
	}
	return data, nil
}

func encodeManagementError(
	request protocol.BrokerRPCEnvelope,
	failure managementFailure,
) ([]byte, error) {
	payload, err := json.Marshal(protocol.BrokerRPCErrorPayload{
		Code: failure.code, Message: failure.message,
	})
	if err != nil {
		return nil, err
	}
	response := protocol.BrokerRPCEnvelope{
		Version: protocol.Version, Type: protocol.BrokerError,
		RequestID: request.RequestID, SessionID: request.SessionID,
		Generation: request.Generation, Payload: payload,
	}
	data, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	if _, err := protocol.DecodeBrokerRPCResponse(data); err != nil {
		return nil, fmt.Errorf("broker: validate management error: %w", err)
	}
	return data, nil
}

func encodeSessionCursor(offset int) string {
	var value [8]byte
	binary.BigEndian.PutUint64(value[:], uint64(offset))
	return base64.RawURLEncoding.EncodeToString(value[:])
}

func decodeSessionCursor(cursor *string) (int, error) {
	if cursor == nil {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(*cursor)
	if err != nil || len(decoded) != 8 {
		return 0, errInvalidSessionCursor
	}
	offset := binary.BigEndian.Uint64(decoded)
	if offset > uint64(^uint(0)>>1) {
		return 0, errInvalidSessionCursor
	}
	return int(offset), nil
}

func validManagementTitle(value string) bool {
	if value == "" || len(value) > maxDefaultTitleBytes || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
