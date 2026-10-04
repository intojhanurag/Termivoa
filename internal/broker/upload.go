package broker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Photo uploads from the controlling phone into the session's project folder.
// See protocol/v1/authentication-rpc.md ("Image uploads").

const (
	uploadFolder       = ".termivoa"
	uploadSubfolder    = "uploads"
	uploadsPerMinute   = 12
	maxActiveUploads   = 4
	uploadIdleTimeout  = 30 * time.Second
	uploadNameAttempts = 3
)

var (
	errUploadNotController = errors.New("broker: only the controlling device may upload")
	errUploadType          = errors.New("broker: upload is not a JPEG, PNG, WebP or GIF image")
	errUploadSize          = errors.New("broker: upload size does not match")
	errUploadLimit         = errors.New("broker: upload limit reached")
	errUploadFolder        = errors.New("broker: project upload folder is not a plain directory")
	errUploadOrder         = errors.New("broker: upload message out of order")
)

// uploadSuffix is the random part of a server-generated upload name.
var uploadSuffix = func() (string, error) {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

// uploadLimits allows one running upload per device, maxActiveUploads in
// total, and uploadsPerMinute started uploads per device in a fixed window.
type uploadLimits struct {
	mu      sync.Mutex
	active  map[string]bool
	windows map[string]uploadWindow
}

type uploadWindow struct {
	start time.Time
	count int
}

func (limits *uploadLimits) acquire(device string, now time.Time) (func(), error) {
	limits.mu.Lock()
	defer limits.mu.Unlock()
	if limits.active == nil {
		limits.active, limits.windows = map[string]bool{}, map[string]uploadWindow{}
	}
	for other, window := range limits.windows {
		if now.Sub(window.start) >= time.Minute {
			delete(limits.windows, other)
		}
	}
	window := limits.windows[device]
	if limits.active[device] || len(limits.active) >= maxActiveUploads || window.count >= uploadsPerMinute {
		return nil, errUploadLimit
	}
	if window.count == 0 {
		window.start = now
	}
	window.count++
	limits.windows[device] = window
	limits.active[device] = true
	return func() {
		limits.mu.Lock()
		delete(limits.active, device)
		limits.mu.Unlock()
	}, nil
}

// serveUpload owns a dedicated gateway connection for one upload: begin, the
// chunks in order, then commit. The file stays in memory (at most
// MaxUploadBytes) and is written only after commit authorizes again. Any
// failure answers gateway.error and ends the connection, dropping the buffer.
func (server *ManagementServer) serveUpload(
	ctx context.Context, connection net.Conn, reader *ipc.FrameReader, writer *ipc.FrameWriter,
	begin protocol.GatewayRPCEnvelope,
) error {
	payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCUploadBeginPayload](begin)
	if err != nil {
		return err
	}
	principal, _, err := server.authorizeUpload(ctx, begin.Origin, payload.Credential, payload.SessionID, payload.Generation)
	var release func()
	if err == nil {
		release, err = server.uploads.acquire(principal.DeviceID, time.Now())
	}
	if err != nil {
		return writeGatewayReply(writer, begin, nil, err)
	}
	// The device's slot is freed before the final reply: once the gateway
	// reads it, the phone may start its next upload at once.
	release = sync.OnceFunc(release)
	defer release()
	finish := func(request protocol.GatewayRPCEnvelope, result any, err error) error {
		release()
		return writeGatewayReply(writer, request, result, err)
	}
	if err := writeGatewayReply(writer, begin, protocol.BrokerRPCEmptyPayload{}, nil); err != nil {
		return err
	}
	data := make([]byte, 0, payload.Size)
	for {
		_ = connection.SetReadDeadline(time.Now().Add(uploadIdleTimeout))
		frame, err := reader.ReadFrame()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		request, err := protocol.DecodeGatewayRPCRequest(frame)
		if err != nil {
			return err
		}
		switch request.Type {
		case protocol.GatewayUploadChunk:
			chunk, err := protocol.DecodeRPCPayload[protocol.GatewayRPCUploadChunkPayload](request)
			if err != nil {
				return err
			}
			bytes, err := protocol.DecodeUploadData(chunk.Data, protocol.MaxUploadChunkBytes)
			if err != nil {
				return finish(request, nil, errors.Join(errUploadOrder, err))
			}
			if int64(len(data)+len(bytes)) > payload.Size {
				return finish(request, nil, errUploadSize)
			}
			data = append(data, bytes...)
			if err := writeGatewayReply(writer, request, protocol.BrokerRPCEmptyPayload{}, nil); err != nil {
				return err
			}
		case protocol.GatewayUploadCommit:
			commit, err := protocol.DecodeRPCPayload[protocol.GatewayRPCCredentialPayload](request)
			if err != nil {
				return err
			}
			result, err := server.commitUpload(ctx, request.Origin, principal.DeviceID, commit.Credential, payload, data)
			return finish(request, result, err)
		default:
			return finish(request, nil, errUploadOrder)
		}
	}
}

func (server *ManagementServer) commitUpload(
	ctx context.Context, origin *string, device, credential string, begin protocol.GatewayRPCUploadBeginPayload, data []byte,
) (protocol.HTTPUploadResponse, error) {
	if int64(len(data)) != begin.Size {
		return protocol.HTTPUploadResponse{}, errUploadSize
	}
	// Control, the credential and the origin's trust may have changed while
	// the chunks arrived.
	principal, projectDir, err := server.authorizeUpload(ctx, origin, credential, begin.SessionID, begin.Generation)
	if err != nil {
		return protocol.HTTPUploadResponse{}, err
	}
	if principal.DeviceID != device {
		return protocol.HTTPUploadResponse{}, errUploadNotController
	}
	return saveUpload(projectDir, data, time.Now())
}

// authorizeUpload returns the credential's principal and the session's
// project folder when the request's origin may use that credential
// (admitDevice) and that device holds the controller lease of the running
// session generation.
func (server *ManagementServer) authorizeUpload(
	ctx context.Context, origin *string, credential, sessionID, generation string,
) (auth.Principal, string, error) {
	if server.config.Auth == nil {
		return auth.Principal{}, "", errAuthUnavailable
	}
	principal, err := server.config.Auth.Authenticate(ctx, credential)
	if err == nil {
		err = server.admitDevice(origin, principal)
	}
	if err != nil {
		return auth.Principal{}, "", err
	}
	session, err := server.config.Registry.Get(sessionID)
	if err != nil {
		return auth.Principal{}, "", err
	}
	if isProviderSession(session) {
		return auth.Principal{}, "", errProviderFeature // design section 6, rule 8
	}
	status, err := session.Status(ctx)
	if err != nil {
		return auth.Principal{}, "", err
	}
	if status.Generation != generation {
		return auth.Principal{}, "", ErrGenerationChanged
	}
	if status.State != SessionRunning {
		return auth.Principal{}, "", ErrSessionNotRunning
	}
	presence, err := session.Presence(ctx, false)
	if err != nil {
		return auth.Principal{}, "", err
	}
	if presence.ControllerDevice == "" || presence.ControllerDevice != principal.DeviceID {
		return auth.Principal{}, "", errUploadNotController
	}
	metadata, err := server.config.Store.GetSession(ctx, sessionID)
	if err != nil {
		return auth.Principal{}, "", err
	}
	project, err := server.config.Store.GetProject(ctx, metadata.ProjectID)
	if err != nil {
		return auth.Principal{}, "", err
	}
	return principal, project.CanonicalPath, nil
}

// detectImage identifies the image type from magic bytes only.
func detectImage(data []byte) (protocol.HTTPUploadContentType, string, bool) {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff}):
		return protocol.ImageJPEG, ".jpg", true
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return protocol.ImagePNG, ".png", true
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return protocol.ImageGIF, ".gif", true
	case len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")):
		return protocol.ImageWebp, ".webp", true
	}
	return "", "", false
}

// saveUpload writes data as a new current-user-only file in
// <projectDir>/.termivoa/uploads with a server-generated name and returns the
// project-relative path. os.Root keeps every step inside projectDir;
// .termivoa and uploads must be real directories, never symlinks.
func saveUpload(projectDir string, data []byte, now time.Time) (protocol.HTTPUploadResponse, error) {
	contentType, extension, ok := detectImage(data)
	if !ok {
		return protocol.HTTPUploadResponse{}, errUploadType
	}
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return protocol.HTTPUploadResponse{}, err
	}
	defer root.Close()
	folder, err := openUploadFolder(root, uploadFolder)
	if err != nil {
		return protocol.HTTPUploadResponse{}, err
	}
	defer folder.Close()
	if err := ensureIgnoreAll(folder); err != nil {
		return protocol.HTTPUploadResponse{}, err
	}
	uploads, err := openUploadFolder(folder, uploadSubfolder)
	if err != nil {
		return protocol.HTTPUploadResponse{}, err
	}
	defer uploads.Close()
	for range uploadNameAttempts {
		suffix, err := uploadSuffix()
		if err != nil {
			return protocol.HTTPUploadResponse{}, err
		}
		name := now.Format("20060102-150405") + "-" + suffix + extension
		file, err := uploads.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return protocol.HTTPUploadResponse{}, err
		}
		_, writeErr := file.Write(data)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			_ = uploads.Remove(name)
			return protocol.HTTPUploadResponse{}, err
		}
		// Reapplies 0600, or the current-user-only ACL on Windows.
		if err := ipc.EnsurePrivateFile(filepath.Join(projectDir, uploadFolder, uploadSubfolder, name)); err != nil {
			_ = uploads.Remove(name)
			return protocol.HTTPUploadResponse{}, err
		}
		return protocol.HTTPUploadResponse{
			Path: uploadFolder + "/" + uploadSubfolder + "/" + name, Size: int64(len(data)), ContentType: contentType,
		}, nil
	}
	return protocol.HTTPUploadResponse{}, errors.New("broker: no free upload name")
}

// openUploadFolder creates name as a 0700 directory if missing and opens it.
// A symlink, junction or file is refused; the opened directory must be the
// one that was checked.
func openUploadFolder(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errUploadFolder
	}
	folder, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	if opened, err := folder.Stat("."); err != nil || !os.SameFile(info, opened) {
		_ = folder.Close()
		return nil, errUploadFolder
	}
	return folder, nil
}

// ensureIgnoreAll creates .termivoa/.gitignore with "*" unless something
// already has that name, so uploads are never committed.
func ensureIgnoreAll(folder *os.Root) error {
	file, err := folder.OpenFile(".gitignore", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString("*\n")
	return errors.Join(writeErr, file.Close())
}

// writeGatewayReply answers one upload message with its result or a typed
// gateway.error.
func writeGatewayReply(writer *ipc.FrameWriter, request protocol.GatewayRPCEnvelope, payload any, failure error) error {
	var response []byte
	var err error
	if failure == nil {
		response, failure = encodeRPCResponse(request, protocol.GatewayRPCResults[request.Type], payload,
			protocol.DecodeGatewayRPCResponse)
	}
	if failure != nil {
		classified := classifySessionError(failure)
		response, err = encodeRPCResponse(request, protocol.GatewayError,
			protocol.BrokerRPCErrorPayload{Code: classified.code, Message: classified.message}, protocol.DecodeGatewayRPCResponse)
		if err != nil {
			return err
		}
	}
	return writer.WriteFrame(response)
}

func classifyUploadError(err error) (managementFailure, bool) {
	switch {
	case errors.Is(err, errUploadNotController):
		return managementFailure{protocol.NotController, "take control before attaching a photo"}, true
	case errors.Is(err, errUploadType):
		return managementFailure{protocol.InvalidRequest, "only JPEG, PNG, WebP or GIF images can be attached"}, true
	case errors.Is(err, errUploadLimit):
		return managementFailure{protocol.LimitExceeded, "too many photos; wait a minute and try again"}, true
	case errors.Is(err, errUploadFolder):
		return managementFailure{protocol.InvalidRequest,
			"the project's .termivoa folder is a link or a file; fix it on the computer"}, true
	case errors.Is(err, errUploadSize), errors.Is(err, errUploadOrder):
		return managementFailure{protocol.InvalidRequest, "the upload was incomplete; try again"}, true
	}
	return managementFailure{}, false
}
