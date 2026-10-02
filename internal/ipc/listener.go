package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	EndpointDescriptorVersion = 1
	maxEndpointDescriptorSize = 16 * 1024
	staleProbeTimeout         = 750 * time.Millisecond

	// defaultSocketName is the on-disk name family ("broker.sock",
	// "broker.lock", "broker-endpoint.json") the primary broker endpoint
	// uses. ListenerConfig.SocketName defaults to it.
	defaultSocketName = "broker"
)

// descriptorFileName returns the endpoint descriptor's basename for one
// socket-name family within a runtime directory.
func descriptorFileName(name string) string { return name + "-endpoint.json" }

var (
	ErrBrokerAlreadyRunning       = errors.New("ipc: broker already owns the selected data or runtime location")
	ErrEndpointMissing            = errors.New("ipc: broker endpoint descriptor is missing")
	ErrEndpointInvalid            = errors.New("ipc: broker endpoint descriptor is invalid")
	ErrEndpointGenerationMismatch = errors.New("ipc: endpoint broker generation does not match live broker")
	ErrPeerUnauthorized           = errors.New("ipc: local peer is not the current user")
)

// EndpointDescriptor locates one broker generation. ProcessID is diagnostic;
// callers must establish liveness with ClientHandshake rather than trusting it.
type EndpointDescriptor struct {
	Version          int    `json:"version"`
	Transport        string `json:"transport"`
	Address          string `json:"address"`
	BrokerGeneration string `json:"brokerGeneration"`
	ProcessID        int    `json:"processId"`
}

// ListenerConfig identifies the private runtime directory and the broker
// generation that owns the endpoint.
type ListenerConfig struct {
	RuntimeDir       string
	BrokerGeneration string

	// SocketName selects the on-disk name family ("<name>.sock",
	// "<name>.lock", "<name>-endpoint.json") within RuntimeDir. Empty means
	// "broker", the primary broker endpoint. A second short name lets an
	// unrelated endpoint (such as the gateway's stop control) share the same
	// private, already-hardened runtime directory without colliding with
	// the broker's own files and without exceeding the broker socket's
	// path length, since every name is bounded to the same directory.
	SocketName string
}

// BrokerListener owns the platform listener, endpoint descriptor, and
// single-broker OS lock as one lifetime.
type BrokerListener struct {
	listener   net.Listener
	descriptor EndpointDescriptor
	runtimeDir string
	name       string
	lock       brokerLock
	closeOnce  sync.Once
	closeErr   error
}

// BrokerDataLock owns the process-wide right to open and mutate one broker
// data directory. It is separate from the runtime endpoint lock because
// callers may configure different runtime directories that still point at the
// same SQLite database.
type BrokerDataLock struct {
	lock      brokerLock
	closeOnce sync.Once
	closeErr  error
}

// EnsurePrivateDirectory prepares an absolute current-user-only directory and
// returns its canonical path. Broker process setup uses the same platform ACL
// boundary for persistent application data as the runtime endpoint uses.
func EnsurePrivateDirectory(path string) (string, error) {
	directory, err := checkedRuntimeDir(path)
	if err != nil {
		return "", err
	}
	if err := prepareRuntimeDir(directory); err != nil {
		return "", fmt.Errorf("ipc: prepare private directory: %w", err)
	}
	return directory, nil
}

// EnsurePrivateFile reapplies and verifies the platform current-user-only
// boundary on an existing regular file. SQLite sidecar files remain protected
// by the containing private directory.
func EnsurePrivateFile(path string) error {
	if path == "" {
		return errors.New("ipc: private file path must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("ipc: resolve private file: %w", err)
	}
	if filepath.Clean(path) != absolute {
		return errors.New("ipc: private file path must be an absolute clean path")
	}
	if err := securePrivateFile(absolute); err != nil {
		return fmt.Errorf("ipc: secure private file: %w", err)
	}
	return nil
}

// LockBrokerDataDirectory prepares and canonically resolves a private data
// directory, then takes a nonblocking current-user OS lock inside it. The
// returned canonical path must be used for every later database operation.
func LockBrokerDataDirectory(path string) (string, *BrokerDataLock, error) {
	directory, err := EnsurePrivateDirectory(path)
	if err != nil {
		return "", nil, err
	}
	lock, err := acquireBrokerLock(directory, "broker-data.lock")
	if err != nil {
		return "", nil, err
	}
	return directory, &BrokerDataLock{lock: lock}, nil
}

// Close releases broker ownership of the data directory.
func (lock *BrokerDataLock) Close() error {
	if lock == nil {
		return nil
	}
	lock.closeOnce.Do(func() { lock.closeErr = lock.lock.Close() })
	return lock.closeErr
}

// BrokerLocksHeld reports whether a live process holds the broker lock of
// dataDir or runtimeDir. It creates no directory or lock file: a missing lock
// file has never been held.
// ponytail: probing takes an existing free lock for an instant, so a broker
// starting in that instant fails once; its supervisor or the user retries.
func BrokerLocksHeld(dataDir, runtimeDir string) (bool, error) {
	for _, lock := range [][2]string{{dataDir, "broker-data.lock"}, {runtimeDir, "broker.lock"}} {
		if lock[0] == "" {
			// An unknown directory is not the current working directory.
			continue
		}
		if _, err := os.Lstat(filepath.Join(lock[0], lock[1])); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return false, fmt.Errorf("ipc: inspect broker lock: %w", err)
		}
		held, err := acquireBrokerLock(lock[0], lock[1])
		if errors.Is(err, ErrBrokerAlreadyRunning) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if err := held.Close(); err != nil {
			return false, err
		}
	}
	return false, nil
}

// ListenBroker acquires exclusive broker ownership, recovers a stale endpoint,
// binds the current-user-only transport, and atomically publishes its descriptor.
func ListenBroker(ctx context.Context, config ListenerConfig) (*BrokerListener, error) {
	if ctx == nil {
		return nil, errors.New("ipc: listener context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runtimeDir, err := checkedRuntimeDir(config.RuntimeDir)
	if err != nil {
		return nil, err
	}
	if !protocol.ValidUUID(config.BrokerGeneration) {
		return nil, errors.New("ipc: broker generation must be a UUID")
	}
	name := config.SocketName
	if name == "" {
		name = defaultSocketName
	}
	if err := prepareRuntimeDir(runtimeDir); err != nil {
		return nil, fmt.Errorf("ipc: prepare runtime directory: %w", err)
	}
	lock, err := acquireBrokerLock(runtimeDir, name+".lock")
	if err != nil {
		return nil, err
	}
	releaseLock := true
	defer func() {
		if releaseLock {
			_ = lock.Close()
		}
	}()

	if err := recoverStaleEndpoint(ctx, runtimeDir, name); err != nil {
		return nil, err
	}
	transport, address, err := platformEndpointNamed(runtimeDir, name)
	if err != nil {
		return nil, err
	}
	listener, err := listenPlatform(address)
	if err != nil {
		return nil, fmt.Errorf("ipc: listen on %s endpoint: %w", transport, err)
	}
	if err := securePlatformEndpoint(address); err != nil {
		_ = listener.Close()
		_ = removePlatformEndpoint(address)
		return nil, fmt.Errorf("ipc: restrict local endpoint: %w", err)
	}
	descriptor := EndpointDescriptor{
		Version: EndpointDescriptorVersion, Transport: transport, Address: address,
		BrokerGeneration: config.BrokerGeneration, ProcessID: os.Getpid(),
	}
	if err := writeEndpointDescriptorNamed(runtimeDir, name, descriptor); err != nil {
		_ = listener.Close()
		_ = removePlatformEndpoint(address)
		return nil, err
	}

	releaseLock = false
	return &BrokerListener{
		listener: listener, descriptor: descriptor, runtimeDir: runtimeDir, name: name, lock: lock,
	}, nil
}

// Accept returns only connections whose OS peer identity belongs to the
// current user. The pipe DACL supplies this boundary on Windows.
func (listener *BrokerListener) Accept() (net.Conn, error) {
	for {
		connection, err := listener.listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := verifyPlatformPeer(connection); err != nil {
			_ = connection.Close()
			if errors.Is(err, ErrPeerUnauthorized) {
				continue
			}
			return nil, fmt.Errorf("ipc: verify local peer: %w", err)
		}
		return connection, nil
	}
}

func (listener *BrokerListener) Descriptor() EndpointDescriptor {
	return listener.descriptor
}

// Close removes only this generation's descriptor and endpoint before
// releasing the single-broker lock. It never scans for or kills processes.
func (listener *BrokerListener) Close() error {
	listener.closeOnce.Do(func() {
		listenerErr := listener.listener.Close()
		descriptorErr := removeOwnedDescriptor(listener.runtimeDir, listener.name, listener.descriptor)
		endpointErr := removePlatformEndpoint(listener.descriptor.Address)
		lockErr := listener.lock.Close()
		listener.closeErr = errors.Join(listenerErr, descriptorErr, endpointErr, lockErr)
	})
	return listener.closeErr
}

// DialBroker reads the restricted endpoint descriptor, connects, performs the
// protocol handshake, and rejects a stale descriptor generation.
func DialBroker(
	ctx context.Context,
	runtimeDir string,
	clientKind protocol.IPCClientKind,
	clientBuild string,
) (net.Conn, EndpointDescriptor, protocol.IPCWelcomeClass, error) {
	return DialNamedBroker(ctx, runtimeDir, defaultSocketName, clientKind, clientBuild)
}

// DialNamedBroker is DialBroker for an endpoint published under
// ListenerConfig.SocketName rather than the default "broker" family, such as
// the gateway's stop control endpoint sharing the broker's own runtime
// directory.
func DialNamedBroker(
	ctx context.Context,
	runtimeDir, name string,
	clientKind protocol.IPCClientKind,
	clientBuild string,
) (net.Conn, EndpointDescriptor, protocol.IPCWelcomeClass, error) {
	if ctx == nil {
		return nil, EndpointDescriptor{}, protocol.IPCWelcomeClass{}, errors.New("ipc: dial context must not be nil")
	}
	runtimeDir, err := checkedRuntimeDir(runtimeDir)
	if err != nil {
		return nil, EndpointDescriptor{}, protocol.IPCWelcomeClass{}, err
	}
	descriptor, err := ReadNamedEndpointDescriptor(runtimeDir, name)
	if err != nil {
		return nil, EndpointDescriptor{}, protocol.IPCWelcomeClass{}, err
	}
	connection, welcome, err := dialDescriptor(ctx, runtimeDir, name, descriptor, clientKind, clientBuild)
	if err != nil {
		return nil, descriptor, protocol.IPCWelcomeClass{}, err
	}
	return connection, descriptor, welcome, nil
}

// ValidateBrokerEndpoint proves that the descriptor reaches the matching live
// broker generation. It does not rely on the recorded process ID.
func ValidateBrokerEndpoint(
	ctx context.Context,
	runtimeDir string,
	clientKind protocol.IPCClientKind,
	clientBuild string,
) (EndpointDescriptor, protocol.IPCWelcomeClass, error) {
	connection, descriptor, welcome, err := DialBroker(ctx, runtimeDir, clientKind, clientBuild)
	if connection != nil {
		_ = connection.Close()
	}
	return descriptor, welcome, err
}

func dialDescriptor(
	ctx context.Context,
	runtimeDir, name string,
	descriptor EndpointDescriptor,
	clientKind protocol.IPCClientKind,
	clientBuild string,
) (net.Conn, protocol.IPCWelcomeClass, error) {
	if err := validateEndpointDescriptor(runtimeDir, name, descriptor); err != nil {
		return nil, protocol.IPCWelcomeClass{}, err
	}
	connection, err := dialPlatform(ctx, descriptor.Address)
	if err != nil {
		return nil, protocol.IPCWelcomeClass{}, fmt.Errorf("ipc: dial broker endpoint: %w", err)
	}
	welcome, err := ClientHandshake(ctx, connection, clientKind, clientBuild)
	if err != nil {
		_ = connection.Close()
		return nil, protocol.IPCWelcomeClass{}, err
	}
	if welcome.BrokerGeneration != descriptor.BrokerGeneration {
		_ = connection.Close()
		return nil, protocol.IPCWelcomeClass{}, ErrEndpointGenerationMismatch
	}
	return connection, welcome, nil
}

func recoverStaleEndpoint(ctx context.Context, runtimeDir, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	descriptor, err := ReadNamedEndpointDescriptor(runtimeDir, name)
	if err == nil {
		probeContext, cancel := context.WithTimeout(ctx, staleProbeTimeout)
		connection, _, probeErr := dialDescriptor(probeContext, runtimeDir, name, descriptor, protocol.CLI, "endpoint-probe")
		cancel()
		if connection != nil {
			_ = connection.Close()
		}
		if probeErr == nil {
			return ErrBrokerAlreadyRunning
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	} else if !errors.Is(err, ErrEndpointMissing) && !errors.Is(err, ErrEndpointInvalid) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	_, address, endpointErr := platformEndpointNamed(runtimeDir, name)
	if endpointErr != nil {
		return endpointErr
	}
	if err := removeStaleDescriptorNamed(runtimeDir, name); err != nil {
		return err
	}
	if err := removePlatformEndpoint(address); err != nil {
		return fmt.Errorf("ipc: remove stale local endpoint: %w", err)
	}
	return nil
}

func EndpointDescriptorPath(runtimeDir string) (string, error) {
	return EndpointDescriptorPathNamed(runtimeDir, defaultSocketName)
}

// EndpointDescriptorPathNamed is EndpointDescriptorPath for a non-default
// SocketName.
func EndpointDescriptorPathNamed(runtimeDir, name string) (string, error) {
	runtimeDir, err := checkedRuntimeDir(runtimeDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(runtimeDir, descriptorFileName(name)), nil
}

func ReadEndpointDescriptor(runtimeDir string) (EndpointDescriptor, error) {
	return ReadNamedEndpointDescriptor(runtimeDir, defaultSocketName)
}

// ReadNamedEndpointDescriptor is ReadEndpointDescriptor for a non-default
// SocketName.
func ReadNamedEndpointDescriptor(runtimeDir, name string) (EndpointDescriptor, error) {
	path, err := EndpointDescriptorPathNamed(runtimeDir, name)
	if err != nil {
		return EndpointDescriptor{}, err
	}
	if err := validatePrivateFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EndpointDescriptor{}, ErrEndpointMissing
		}
		return EndpointDescriptor{}, fmt.Errorf("%w: %v", ErrEndpointInvalid, err)
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return EndpointDescriptor{}, ErrEndpointMissing
		}
		return EndpointDescriptor{}, fmt.Errorf("ipc: open endpoint descriptor: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxEndpointDescriptorSize+1))
	if err != nil {
		return EndpointDescriptor{}, fmt.Errorf("ipc: read endpoint descriptor: %w", err)
	}
	if len(data) > maxEndpointDescriptorSize {
		return EndpointDescriptor{}, fmt.Errorf("%w: descriptor exceeds %d bytes", ErrEndpointInvalid, maxEndpointDescriptorSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var descriptor EndpointDescriptor
	if err := decoder.Decode(&descriptor); err != nil {
		return EndpointDescriptor{}, fmt.Errorf("%w: decode descriptor: %v", ErrEndpointInvalid, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return EndpointDescriptor{}, err
	}
	if err := validateEndpointDescriptor(runtimeDir, name, descriptor); err != nil {
		return EndpointDescriptor{}, err
	}
	return descriptor, nil
}

func writeEndpointDescriptor(runtimeDir string, descriptor EndpointDescriptor) error {
	return writeEndpointDescriptorNamed(runtimeDir, defaultSocketName, descriptor)
}

func writeEndpointDescriptorNamed(runtimeDir, name string, descriptor EndpointDescriptor) error {
	if err := validateEndpointDescriptor(runtimeDir, name, descriptor); err != nil {
		return err
	}
	path := filepath.Join(runtimeDir, descriptorFileName(name))
	payload, err := json.Marshal(descriptor)
	if err != nil {
		return fmt.Errorf("ipc: encode endpoint descriptor: %w", err)
	}
	temporary, err := os.CreateTemp(runtimeDir, ".broker-endpoint-*")
	if err != nil {
		return fmt.Errorf("ipc: create endpoint descriptor: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		_ = temporary.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("ipc: restrict endpoint descriptor: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("ipc: write endpoint descriptor: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("ipc: sync endpoint descriptor: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("ipc: close endpoint descriptor: %w", err)
	}
	if err := securePrivateFile(temporaryPath); err != nil {
		return fmt.Errorf("ipc: apply endpoint descriptor ACL: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("ipc: publish endpoint descriptor: %w", err)
	}
	removeTemporary = false
	return syncRuntimeDir(runtimeDir)
}

func removeOwnedDescriptor(runtimeDir, name string, owned EndpointDescriptor) error {
	descriptor, err := ReadNamedEndpointDescriptor(runtimeDir, name)
	if errors.Is(err, ErrEndpointMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	if descriptor.BrokerGeneration != owned.BrokerGeneration || descriptor.Address != owned.Address {
		return nil
	}
	return removeStaleDescriptorNamed(runtimeDir, name)
}

func removeStaleDescriptor(runtimeDir string) error {
	return removeStaleDescriptorNamed(runtimeDir, defaultSocketName)
}

func removeStaleDescriptorNamed(runtimeDir, name string) error {
	path := filepath.Join(runtimeDir, descriptorFileName(name))
	if err := validatePrivateFile(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("ipc: refuse unsafe endpoint descriptor removal: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("ipc: remove endpoint descriptor: %w", err)
	}
	return syncRuntimeDir(runtimeDir)
}

func validateEndpointDescriptor(runtimeDir, name string, descriptor EndpointDescriptor) error {
	transport, address, err := platformEndpointNamed(runtimeDir, name)
	if err != nil {
		return err
	}
	if descriptor.Version != EndpointDescriptorVersion || descriptor.Transport != transport || descriptor.Address != address ||
		!protocol.ValidUUID(descriptor.BrokerGeneration) || descriptor.ProcessID <= 0 {
		return ErrEndpointInvalid
	}
	return nil
}

func checkedRuntimeDir(runtimeDir string) (string, error) {
	if runtimeDir == "" {
		return "", errors.New("ipc: runtime directory must not be empty")
	}
	absolute, err := filepath.Abs(runtimeDir)
	if err != nil {
		return "", fmt.Errorf("ipc: resolve runtime directory: %w", err)
	}
	if filepath.Clean(runtimeDir) != absolute {
		return "", errors.New("ipc: runtime directory must be an absolute clean path")
	}
	canonical, err := canonicalRuntimeDir(absolute)
	if err != nil {
		return "", fmt.Errorf("ipc: canonicalize runtime directory: %w", err)
	}
	return canonical, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: descriptor contains trailing JSON", ErrEndpointInvalid)
		}
		return fmt.Errorf("%w: descriptor trailing data: %v", ErrEndpointInvalid, err)
	}
	return nil
}

type brokerLock interface {
	Close() error
}
