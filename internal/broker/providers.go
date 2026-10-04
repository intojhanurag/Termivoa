package broker

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/herdr"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/terminal"
	"github.com/intojhanurag/phone-terminal/internal/webpush"
)

// Terminal workspace providers (docs/herdr-provider.md): herdr. The broker
// wires one *herdr.Provider, keeps its Integrations settings in the settings
// table, answers the provider routes from its cache, limits them per device,
// and owns the in-memory provider sessions.

const (
	providerEnabledKey = "provider.herdr.enabled"
	providerNotifyKey  = "provider.herdr.notify"

	providerOpensPerMinute   = 20
	providerCreatesPerMinute = 3
	providerUpdatesPerMinute = 10

	// providerIdleEnd ends a provider session this long after its last
	// viewer left; a new session counts as viewed for as long.
	providerIdleEnd   = 10 * time.Second
	providerReapEvery = time.Second
)

// providerFailure is a provider error with its fixed public code and text.
// The text is Termivoa's, never herdr's or git's.
type providerFailure struct{ managementFailure }

func (failure providerFailure) Error() string { return "broker: provider: " + failure.message }

var (
	errProviderUnknown     = providerFailure{managementFailure{protocol.InvalidRequest, "this computer has no such provider"}}
	errProviderUnavailable = providerFailure{managementFailure{protocol.HostUnavailable, "herdr is off or not running on this computer"}}
	errProviderTarget      = providerFailure{managementFailure{protocol.SessionMissing, "that terminal is gone"}}
	errProviderGroup       = providerFailure{managementFailure{protocol.SessionMissing, "that project is gone"}}
	errProviderSessions    = providerFailure{managementFailure{protocol.LimitExceeded, "4 herdr terminals are open; close one and try again"}}
	errProviderRate        = providerFailure{managementFailure{protocol.LimitExceeded, "too many requests; wait a minute and try again"}}
	errProviderBusy        = providerFailure{managementFailure{protocol.LimitExceeded, "another worktree is being made; try again"}}
	errProviderKind        = providerFailure{managementFailure{protocol.InvalidRequest, "herdr cannot start that agent here"}}
	errProviderNotGit      = providerFailure{managementFailure{protocol.NotARepository, "this project is not in a git repository"}}
	errProviderExists      = providerFailure{managementFailure{protocol.WorktreeExists, "a worktree or branch with this name already exists"}}
	errProviderWorktree    = providerFailure{managementFailure{protocol.WorktreeFailed, "herdr could not make the worktree."}}
	errProviderFeature     = providerFailure{managementFailure{protocol.InvalidRequest, "this is not available for a herdr terminal"}}
	errProviderChanges     = providerFailure{managementFailure{protocol.InvalidRequest, "this herdr terminal's folder cannot be read"}}
)

func classifyProviderError(err error) (managementFailure, bool) {
	var failure providerFailure
	if errors.As(err, &failure) {
		return failure.managementFailure, true
	}
	return managementFailure{}, false
}

// providerHost is the broker's side of one provider.
type providerHost struct {
	provider *herdr.Provider
	home     string // where a session starts when its checkout folder is refused
	enabled  atomic.Bool
	notify   atomic.Pointer[[]protocol.HTTPProviderNotifyState]
	limits   providerLimits
	settings sync.Mutex // orders setting writes
	locks    keyedLocks // one open at a time per target

	mu       sync.Mutex
	sessions map[string]*providerOpen // by target key
}

// providerOpen is one live provider session.
type providerOpen struct {
	session        *providerSession
	id, generation string
	title, target  string
	viewed         time.Time // guarded by providerHost.mu
}

// newProviderHost loads the Integrations settings (absent: on, both states)
// and builds the provider. onChange receives push transitions.
func newProviderHost(ctx context.Context, server *ManagementServer, config herdr.Config) (*providerHost, error) {
	host := &providerHost{home: config.Home, sessions: map[string]*providerOpen{}}
	enabled, found, err := server.config.Store.GetSetting(ctx, providerEnabledKey)
	if err != nil {
		return nil, err
	}
	host.enabled.Store(!found || enabled != "0")
	notify := []protocol.HTTPProviderNotifyState{protocol.Blocked, protocol.Done}
	if value, found, err := server.config.Store.GetSetting(ctx, providerNotifyKey); err != nil {
		return nil, err
	} else if found {
		notify = parseNotify(value)
	}
	host.notify.Store(&notify)
	config.Enabled = host.enabled.Load
	host.provider = herdr.New(config, server.providerChanged)
	return host, nil
}

func parseNotify(value string) []protocol.HTTPProviderNotifyState {
	notify := []protocol.HTTPProviderNotifyState{}
	for _, state := range []protocol.HTTPProviderNotifyState{protocol.Blocked, protocol.Done} {
		if slices.Contains(strings.Split(value, ","), string(state)) {
			notify = append(notify, state)
		}
	}
	return notify
}

func (host *providerHost) notifies(state protocol.HTTPProviderNotifyState) bool {
	return slices.Contains(*host.notify.Load(), state)
}

// info is HTTPProviderInfo with this computer's settings. installed is false
// when herdr is not installed: the computer then lists no provider.
func (host *providerHost) info() (protocol.HTTPProviderInfo, bool) {
	info, installed := host.provider.Info()
	info.Enabled = host.enabled.Load()
	info.Notify = append([]protocol.HTTPProviderNotifyState{}, *host.notify.Load()...)
	if !info.Enabled {
		info.State, info.CanCreate, info.AgentKinds = protocol.Off, false, []string{}
	}
	return info, installed
}

func (host *providerHost) snapshot() protocol.HTTPProviderSnapshotResponse {
	if !host.enabled.Load() {
		return protocol.HTTPProviderSnapshotResponse{Provider: herdr.ID, State: protocol.Off, Groups: []protocol.HTTPProviderGroup{}}
	}
	return host.provider.Snapshot()
}

// update saves the Integrations settings of this computer for all devices.
func (host *providerHost) update(ctx context.Context, server *ManagementServer, request protocol.HTTPProviderUpdateRequest) (protocol.HTTPProviderInfo, error) {
	host.settings.Lock()
	defer host.settings.Unlock()
	now := server.now()
	if request.Enabled != nil {
		value := "0"
		if *request.Enabled {
			value = "1"
		}
		if err := server.config.Store.PutSetting(ctx, providerEnabledKey, value, now); err != nil {
			return protocol.HTTPProviderInfo{}, err
		}
		host.enabled.Store(*request.Enabled)
	}
	if request.Notify != nil {
		var states []string
		for _, state := range request.Notify {
			states = append(states, string(state))
		}
		notify := parseNotify(strings.Join(states, ","))
		if err := server.config.Store.PutSetting(ctx, providerNotifyKey, strings.Join(states, ","), now); err != nil {
			return protocol.HTTPProviderInfo{}, err
		}
		host.notify.Store(&notify)
	}
	info, _ := host.info()
	return info, nil
}

// providerChanged turns a herdr transition into a push (design section 7):
// only while the provider is on and its notify setting has that state,
// through the provider's own push budget.
func (server *ManagementServer) providerChanged(change herdr.Change) {
	host := server.providers
	if host == nil || server.providerPush == nil || !host.enabled.Load() {
		return
	}
	event := webpush.ProviderEvent{
		Provider: herdr.ID, Target: change.Key, Group: change.Group, Item: change.Item, Agent: change.Agent,
		Kind: webpush.KindWaiting, Duration: change.Worked,
	}
	state := protocol.Blocked
	if change.To == "done" {
		event.Kind, state = webpush.KindDone, protocol.Done
	}
	if host.notifies(state) {
		server.providerPush(event)
	}
}

// providerLimits is the per-device limiter of design S10: a fixed one-minute
// window per route and device.
type providerLimits struct {
	mu      sync.Mutex
	windows map[string]uploadWindow
}

func (limits *providerLimits) take(route, device string, perMinute int, now time.Time) bool {
	limits.mu.Lock()
	defer limits.mu.Unlock()
	if limits.windows == nil {
		limits.windows = map[string]uploadWindow{}
	}
	for key, window := range limits.windows {
		if now.Sub(window.start) >= time.Minute || now.Before(window.start) {
			delete(limits.windows, key)
		}
	}
	key := route + "\x00" + device
	window := limits.windows[key]
	if window.count >= perMinute {
		return false
	}
	if window.count == 0 {
		window.start = now
	}
	window.count++
	limits.windows[key] = window
	return true
}

// providerFor returns the host of an installed provider named id.
func (server *ManagementServer) providerFor(id string) (*providerHost, error) {
	host := server.providers
	if host == nil || id != herdr.ID {
		return nil, errProviderUnknown
	}
	if _, installed := host.provider.Info(); !installed {
		return nil, errProviderUnknown
	}
	return host, nil
}

// runProviders starts the poll loop and the session reaper; the returned
// stop waits for both.
func (server *ManagementServer) runProviders(ctx context.Context) (stop func()) {
	host := server.providers
	if host == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wait sync.WaitGroup
	wait.Go(func() { host.provider.Run(ctx) })
	wait.Go(func() {
		ticker := time.NewTicker(providerReapEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				host.reap(ctx, time.Now())
			}
		}
	})
	return func() {
		cancel()
		wait.Wait()
	}
}

// providerSession is a provider session as the registry hands it out.
// History (the attach client draws on the alternate screen), preview,
// tasks and worktrees are refused; Changes and files read the checkout
// folder only when it passed the project-path checks (N3).
type providerSession struct {
	registrySession
	changes string // "" when Changes is refused
}

func (session *providerSession) ReadHistory(context.Context, string, terminal.HistoryReadRequest) (terminal.HistoryReadResult, error) {
	return terminal.HistoryReadResult{}, errProviderFeature
}

func (session *providerSession) PreviewTarget(context.Context, uint16) (string, error) {
	return "", errProviderFeature
}

func (session *providerSession) ChangesFolder(context.Context) (string, error) {
	if session.changes == "" {
		return "", errProviderChanges
	}
	return session.changes, nil
}

func (session *providerSession) ProjectFolder() string { return "" }

// providerRefused lists the live requests a provider session refuses.
func providerRefused(kind protocol.MessageType) bool {
	switch kind {
	case protocol.HistoryRead, protocol.TasksList, protocol.BranchesList, protocol.WorktreeRemove:
		return true
	}
	return false
}

// providerDirectory is N3: the checkout path must pass the project-path
// checks and be owned by this user. Else the session starts in the home
// folder and Changes is refused.
func (host *providerHost) providerDirectory(path string) (start, changes string) {
	if path == "" {
		return host.home, ""
	}
	resolved, info, err := resolveProjectFolder(path)
	if err != nil || ipc.RequireCurrentOwner(info) != nil {
		return host.home, ""
	}
	return resolved, resolved
}

// open opens, or reuses, the provider session of one target (design
// section 6): one lock per target, never control, never --takeover.
func (server *ManagementServer) openProvider(
	ctx context.Context, host *providerHost, device string, request protocol.HTTPProviderOpenRequest,
) (protocol.HTTPProviderOpenResponse, error) {
	if !host.enabled.Load() {
		return protocol.HTTPProviderOpenResponse{}, errProviderUnavailable
	}
	if !host.limits.take("open", device, providerOpensPerMinute, server.now()) {
		return protocol.HTTPProviderOpenResponse{}, errProviderRate
	}
	attach, err := host.provider.Attach(request.TargetID)
	switch {
	case errors.Is(err, herdr.ErrTargetMissing):
		return protocol.HTTPProviderOpenResponse{}, errProviderTarget
	case err != nil:
		return protocol.HTTPProviderOpenResponse{}, errProviderUnavailable
	}
	unlock := host.locks.lock(attach.Key)
	defer unlock()
	response := protocol.HTTPProviderOpenResponse{Session: protocol.HTTPProviderSession{
		Title: attach.Title, Provider: herdr.ID, TargetID: request.TargetID, State: protocol.Running,
	}}
	host.mu.Lock()
	existing := host.sessions[attach.Key]
	host.mu.Unlock()
	if existing != nil {
		if status, err := existing.session.Status(ctx); err == nil && status.State == SessionRunning {
			host.mu.Lock()
			existing.viewed = time.Now()
			host.mu.Unlock()
			response.Session.ID, response.Session.Generation, response.Session.Title = existing.id, existing.generation, existing.title
			response.Session.Columns, response.Session.Rows = int64(status.Columns), int64(status.Rows)
			response.Reused = true
			return response, nil
		}
	}
	start, changes := host.providerDirectory(attach.Directory)
	session, id, generation, err := server.config.Registry.CreateProvider(ptyhost.Spec{
		Executable: attach.Argv[0], Args: attach.Argv[1:], Env: attach.Env, Directory: start,
		Columns: int(request.Columns), Rows: int(request.Rows),
	}, changes)
	if errors.Is(err, ErrProviderLimit) {
		return protocol.HTTPProviderOpenResponse{}, errProviderSessions
	}
	if err != nil {
		return protocol.HTTPProviderOpenResponse{}, err
	}
	host.mu.Lock()
	host.sessions[attach.Key] = &providerOpen{
		session: session, id: id, generation: generation, title: attach.Title, target: request.TargetID, viewed: time.Now(),
	}
	host.mu.Unlock()
	response.Session.ID, response.Session.Generation = id, generation
	response.Session.Columns, response.Session.Rows = request.Columns, request.Rows
	return response, nil
}

// createProvider is milestone M2 (design sections 4.3 and 10). ctx is the
// broker's serve context, never the phone's request: a phone that leaves
// does not cut it short. herdr's and git's text never leaves the host.
func (server *ManagementServer) createProvider(
	ctx context.Context, host *providerHost, device string, request protocol.HTTPProviderCreateRequest,
) (protocol.HTTPProviderCreateResponse, error) {
	if !host.enabled.Load() {
		return protocol.HTTPProviderCreateResponse{}, errProviderUnavailable
	}
	if !host.limits.take("create", device, providerCreatesPerMinute, server.now()) {
		return protocol.HTTPProviderCreateResponse{}, errProviderRate
	}
	response, err := host.provider.Create(ctx, request)
	for herdrErr, failure := range map[error]providerFailure{
		herdr.ErrUnavailable: errProviderUnavailable, herdr.ErrTargetMissing: errProviderGroup,
		herdr.ErrBusy: errProviderBusy, herdr.ErrKind: errProviderKind, herdr.ErrNotGit: errProviderNotGit,
		herdr.ErrExists: errProviderExists, herdr.ErrWorktree: errProviderWorktree,
	} {
		if errors.Is(err, herdrErr) {
			return protocol.HTTPProviderCreateResponse{}, failure
		}
	}
	if err != nil {
		return protocol.HTTPProviderCreateResponse{}, errProviderWorktree
	}
	return response, nil
}

// reap ends provider sessions (design section 6, rule 6): 10 s after the
// last viewer left (the first 10 s count as viewed), when the target left
// the snapshot (herdr restarted or the pane closed), or when the provider
// is off. Ending the attach gives the laptop its pane size back.
func (host *providerHost) reap(ctx context.Context, now time.Time) {
	host.mu.Lock()
	opens := maps.Clone(host.sessions)
	host.mu.Unlock()
	for key, open := range opens {
		select {
		case <-open.session.Ended():
			host.mu.Lock()
			if host.sessions[key] == open {
				delete(host.sessions, key)
			}
			host.mu.Unlock()
			continue
		default:
		}
		end := !host.enabled.Load() || !host.provider.Has(key)
		if !end {
			presence, err := open.session.Presence(ctx, false)
			host.mu.Lock()
			if err == nil && presence.Viewers > 0 {
				open.viewed = now
			}
			end = now.Sub(open.viewed) >= providerIdleEnd
			host.mu.Unlock()
		}
		if end {
			terminate, cancel := context.WithTimeout(ctx, shutdownTimeout)
			_ = open.session.Terminate(terminate)
			cancel()
		}
	}
}

// keyedLocks is one mutex per key, dropped when nobody holds or waits.
type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	sync.Mutex
	users int
}

func (locks *keyedLocks) lock(key string) (unlock func()) {
	locks.mu.Lock()
	if locks.locks == nil {
		locks.locks = map[string]*keyedLock{}
	}
	held := locks.locks[key]
	if held == nil {
		held = &keyedLock{}
		locks.locks[key] = held
	}
	held.users++
	locks.mu.Unlock()
	held.Lock()
	return func() {
		held.Unlock()
		locks.mu.Lock()
		if held.users--; held.users == 0 {
			delete(locks.locks, key)
		}
		locks.mu.Unlock()
	}
}

// dispatchProvider answers the gateway.providers.* requests.
func (server *ManagementServer) dispatchProvider(
	ctx context.Context, request protocol.GatewayRPCEnvelope, authorize func(string) (auth.Principal, error),
) (any, error) {
	switch request.Type {
	case protocol.GatewayProvidersGet:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCProviderPayload](request)
		if err != nil {
			return nil, err
		}
		if _, err := authorize(payload.Credential); err != nil {
			return nil, err
		}
		host, err := server.providerFor(payload.Provider)
		if err != nil {
			return nil, err
		}
		return host.snapshot(), nil
	case protocol.GatewayProvidersUpdate:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCProviderUpdatePayload](request)
		if err != nil {
			return nil, err
		}
		principal, err := authorize(payload.Credential)
		if err != nil {
			return nil, err
		}
		host, err := server.providerFor(payload.Provider)
		if err != nil {
			return nil, err
		}
		if !host.limits.take("update", principal.DeviceID, providerUpdatesPerMinute, server.now()) {
			return nil, errProviderRate
		}
		return host.update(ctx, server, payload.Request)
	case protocol.GatewayProvidersOpen:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCProviderOpenPayload](request)
		if err != nil {
			return nil, err
		}
		principal, err := authorize(payload.Credential)
		if err != nil {
			return nil, err
		}
		host, err := server.providerFor(payload.Provider)
		if err != nil {
			return nil, err
		}
		return server.openProvider(ctx, host, principal.DeviceID, payload.Request)
	case protocol.GatewayProvidersCreate:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCProviderCreatePayload](request)
		if err != nil {
			return nil, err
		}
		principal, err := authorize(payload.Credential)
		if err != nil {
			return nil, err
		}
		host, err := server.providerFor(payload.Provider)
		if err != nil {
			return nil, err
		}
		return server.createProvider(ctx, host, principal.DeviceID, payload.Request)
	default:
		return nil, errors.New("broker: unsupported provider request")
	}
}
