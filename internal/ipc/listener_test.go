package ipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const secondBrokerGeneration = "44444444-4444-4444-8444-444444444444"

func TestBrokerListenerPublishesAndValidatesLiveEndpoint(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	listener, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: testBrokerGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	serverResult := serveOneHandshake(listener, testBrokerGeneration)

	connection, descriptor, welcome, err := DialBroker(
		context.Background(), runtimeDir, protocol.CLI, "cli-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if descriptor != listener.Descriptor() {
		t.Fatalf("descriptor = %#v, listener = %#v", descriptor, listener.Descriptor())
	}
	if welcome.BrokerGeneration != testBrokerGeneration {
		t.Fatalf("welcome = %#v", welcome)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEndpointDescriptor(runtimeDir); !errors.Is(err, ErrEndpointMissing) {
		t.Fatalf("descriptor after close = %v", err)
	}

	// Closing releases the OS lock and permits a fresh broker generation.
	restarted, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: secondBrokerGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerListenerEnforcesSingleOwner(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	first, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: testBrokerGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: secondBrokerGeneration,
	}); !errors.Is(err, ErrBrokerAlreadyRunning) {
		t.Fatalf("second listener error = %v", err)
	}
}

func TestBrokerDataDirectoryLockIsExclusiveAndReusable(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	canonical, first, err := LockBrokerDataDirectory(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if canonical == "" || first == nil {
		t.Fatalf("data lock = %q, %#v", canonical, first)
	}
	if _, second, err := LockBrokerDataDirectory(dataDir); !errors.Is(err, ErrBrokerAlreadyRunning) || second != nil {
		t.Fatalf("second data lock = %#v, %v", second, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	canonicalAgain, replacement, err := LockBrokerDataDirectory(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalAgain != canonical {
		t.Fatalf("replacement canonical path = %q, want %q", canonicalAgain, canonical)
	}
	if err := replacement.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBrokerListenerRejectsDescriptorGenerationMismatch(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	if err := prepareRuntimeDir(runtimeDir); err != nil {
		t.Fatal(err)
	}
	transport, address, err := platformEndpoint(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := listenPlatform(address)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	defer removePlatformEndpoint(address)
	if err := securePlatformEndpoint(address); err != nil {
		t.Fatal(err)
	}
	if err := writeEndpointDescriptor(runtimeDir, EndpointDescriptor{
		Version: EndpointDescriptorVersion, Transport: transport, Address: address,
		BrokerGeneration: testBrokerGeneration, ProcessID: os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
	defer removeStaleDescriptor(runtimeDir)
	serverResult := serveRawHandshake(raw, secondBrokerGeneration)

	if _, _, _, err := DialBroker(context.Background(), runtimeDir, protocol.CLI, "cli-test"); !errors.Is(err, ErrEndpointGenerationMismatch) {
		t.Fatalf("mismatched descriptor error = %v", err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestListenBrokerUsesHandshakeBeforeRecoveringRecordedEndpoint(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	if err := prepareRuntimeDir(runtimeDir); err != nil {
		t.Fatal(err)
	}
	transport, address, err := platformEndpoint(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := listenPlatform(address)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	defer removePlatformEndpoint(address)
	if err := securePlatformEndpoint(address); err != nil {
		t.Fatal(err)
	}
	if err := writeEndpointDescriptor(runtimeDir, EndpointDescriptor{
		Version: EndpointDescriptorVersion, Transport: transport, Address: address,
		BrokerGeneration: testBrokerGeneration, ProcessID: os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
	defer removeStaleDescriptor(runtimeDir)
	serverResult := serveRawHandshake(raw, testBrokerGeneration)

	if _, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: secondBrokerGeneration,
	}); !errors.Is(err, ErrBrokerAlreadyRunning) {
		t.Fatalf("live recorded endpoint error = %v", err)
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestBrokerListenerRecoversStaleDescriptorAndEndpoint(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	if err := prepareRuntimeDir(runtimeDir); err != nil {
		t.Fatal(err)
	}
	transport, address, err := platformEndpoint(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := listenPlatform(address)
	if err != nil {
		t.Fatal(err)
	}
	if err := securePlatformEndpoint(address); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := writeEndpointDescriptor(runtimeDir, EndpointDescriptor{
		Version: EndpointDescriptorVersion, Transport: transport, Address: address,
		BrokerGeneration: testBrokerGeneration, ProcessID: os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}

	listener, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: secondBrokerGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if listener.Descriptor().BrokerGeneration != secondBrokerGeneration {
		t.Fatalf("recovered descriptor = %#v", listener.Descriptor())
	}
}

func TestListenBrokerDoesNotRecoverEndpointAfterContextCancellation(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	if err := prepareRuntimeDir(runtimeDir); err != nil {
		t.Fatal(err)
	}
	transport, address, err := platformEndpoint(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := listenPlatform(address)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	defer removePlatformEndpoint(address)
	if err := securePlatformEndpoint(address); err != nil {
		t.Fatal(err)
	}
	if err := writeEndpointDescriptor(runtimeDir, EndpointDescriptor{
		Version: EndpointDescriptorVersion, Transport: transport, Address: address,
		BrokerGeneration: testBrokerGeneration, ProcessID: os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
	defer removeStaleDescriptor(runtimeDir)

	accepted := make(chan net.Conn, 1)
	go func() {
		connection, _ := raw.Accept()
		accepted <- connection
	}()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, listenErr := ListenBroker(ctx, ListenerConfig{
			RuntimeDir: runtimeDir, BrokerGeneration: secondBrokerGeneration,
		})
		result <- listenErr
	}()
	connection := <-accepted
	cancel()
	if connection != nil {
		_ = connection.Close()
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stale probe error = %v", err)
	}
	if _, err := ReadEndpointDescriptor(runtimeDir); err != nil {
		t.Fatalf("canceled probe removed descriptor: %v", err)
	}
}

func TestEndpointDescriptorRejectsUnsafeOrMalformedFiles(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	if err := prepareRuntimeDir(runtimeDir); err != nil {
		t.Fatal(err)
	}
	path, err := EndpointDescriptorPath(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securePrivateFile(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEndpointDescriptor(runtimeDir); !errors.Is(err, ErrEndpointInvalid) {
		t.Fatalf("malformed descriptor error = %v", err)
	}
}

func TestBrokerListenerValidatesInputs(t *testing.T) {
	if _, err := ListenBroker(nil, ListenerConfig{}); err == nil {
		t.Fatal("nil listener context was accepted")
	}
	if _, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: "relative", BrokerGeneration: testBrokerGeneration,
	}); err == nil {
		t.Fatal("relative runtime directory was accepted")
	}
	if _, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: t.TempDir(), BrokerGeneration: "not-a-uuid",
	}); err == nil {
		t.Fatal("invalid generation was accepted")
	}
	if _, _, _, err := DialBroker(nil, t.TempDir(), protocol.CLI, "test"); err == nil {
		t.Fatal("nil dial context was accepted")
	}
}

func serveOneHandshake(listener *BrokerListener, generation string) <-chan error {
	result := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			_, err = ServerHandshake(context.Background(), connection, generation, "host-test")
			_ = connection.Close()
		}
		result <- err
	}()
	return result
}

func serveRawHandshake(listener net.Listener, generation string) <-chan error {
	result := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			_, err = ServerHandshake(context.Background(), connection, generation, "host-test")
			_ = connection.Close()
		}
		result <- err
	}()
	return result
}

func shortRuntimeDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "ptipc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func mustEndpointDescriptorPath(t *testing.T, runtimeDir string) string {
	t.Helper()
	path, err := EndpointDescriptorPath(runtimeDir)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
