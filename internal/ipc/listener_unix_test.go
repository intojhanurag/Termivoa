//go:build darwin || linux

package ipc

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestBrokerListenerUsesPrivateUnixPermissions(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	listener, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: testBrokerGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for path, want := range map[string]os.FileMode{
		runtimeDir:                                0o700,
		listener.Descriptor().Address:             0o600,
		mustEndpointDescriptorPath(t, runtimeDir): 0o600,
	} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode = %04o, want %04o", path, info.Mode().Perm(), want)
		}
	}
}

func TestListenBrokerRefusesUnsafeStaleUnixArtifacts(t *testing.T) {
	t.Run("permissive descriptor", func(t *testing.T) {
		runtimeDir := shortRuntimeDir(t)
		if err := prepareRuntimeDir(runtimeDir); err != nil {
			t.Fatal(err)
		}
		transport, address, err := platformEndpoint(runtimeDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeEndpointDescriptor(runtimeDir, EndpointDescriptor{
			Version: EndpointDescriptorVersion, Transport: transport, Address: address,
			BrokerGeneration: testBrokerGeneration, ProcessID: os.Getpid(),
		}); err != nil {
			t.Fatal(err)
		}
		path := mustEndpointDescriptorPath(t, runtimeDir)
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ListenBroker(context.Background(), ListenerConfig{
			RuntimeDir: runtimeDir, BrokerGeneration: secondBrokerGeneration,
		}); err == nil {
			t.Fatal("permissive descriptor was deleted and replaced")
		}
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("unsafe descriptor was removed: %v", err)
		}
	})

	t.Run("non-socket endpoint", func(t *testing.T) {
		runtimeDir := shortRuntimeDir(t)
		if err := prepareRuntimeDir(runtimeDir); err != nil {
			t.Fatal(err)
		}
		_, address, err := platformEndpoint(runtimeDir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(address, []byte("not a socket"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := ListenBroker(context.Background(), ListenerConfig{
			RuntimeDir: runtimeDir, BrokerGeneration: testBrokerGeneration,
		}); err == nil {
			t.Fatal("non-socket endpoint was deleted and replaced")
		}
		if data, err := os.ReadFile(address); err != nil || string(data) != "not a socket" {
			t.Fatalf("non-socket artifact changed: %q, %v", data, err)
		}
	})
}

func TestListenBrokerRejectsUnixSymlinksBeforeChangingTargets(t *testing.T) {
	t.Run("runtime directory", func(t *testing.T) {
		parent := shortRuntimeDir(t)
		target := t.TempDir()
		if err := os.Chmod(target, 0o755); err != nil {
			t.Fatal(err)
		}
		link := parent + "-link"
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(link) })
		if _, err := ListenBroker(context.Background(), ListenerConfig{
			RuntimeDir: link, BrokerGeneration: testBrokerGeneration,
		}); err == nil {
			t.Fatal("symlinked runtime directory was accepted")
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("runtime symlink target mode = %04o, want unchanged 0755", info.Mode().Perm())
		}
	})

	t.Run("broker lock", func(t *testing.T) {
		runtimeDir := shortRuntimeDir(t)
		if err := prepareRuntimeDir(runtimeDir); err != nil {
			t.Fatal(err)
		}
		target, err := os.CreateTemp(t.TempDir(), "lock-target-")
		if err != nil {
			t.Fatal(err)
		}
		targetPath := target.Name()
		if err := target.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(targetPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(targetPath, runtimeDir+"/broker.lock"); err != nil {
			t.Fatal(err)
		}
		if _, err := ListenBroker(context.Background(), ListenerConfig{
			RuntimeDir: runtimeDir, BrokerGeneration: testBrokerGeneration,
		}); err == nil {
			t.Fatal("symlinked broker lock was accepted")
		}
		info, err := os.Stat(targetPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Fatalf("lock symlink target mode = %04o, want unchanged 0644", info.Mode().Perm())
		}
	})

	t.Run("intermediate runtime directory", func(t *testing.T) {
		parent := shortRuntimeDir(t)
		target := shortRuntimeDir(t)
		resolvedRuntime := target + "/runtime"
		if err := os.Mkdir(resolvedRuntime, 0o755); err != nil {
			t.Fatal(err)
		}
		link := parent + "/redirect"
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := ListenBroker(context.Background(), ListenerConfig{
			RuntimeDir: link + "/runtime", BrokerGeneration: testBrokerGeneration,
		}); err == nil {
			t.Fatal("intermediate runtime symlink was accepted")
		}
		info, err := os.Stat(resolvedRuntime)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("intermediate symlink target mode = %04o, want unchanged 0755", info.Mode().Perm())
		}
	})
}

func TestRetargetedIntermediateSymlinkCannotStartSecondBroker(t *testing.T) {
	parent := shortRuntimeDir(t)
	anchor := parent + "/anchor"
	runtimeDir := anchor + "/runtime"
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	first, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: testBrokerGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	originalAnchor := parent + "/anchor-original"
	if err := os.Rename(anchor, originalAnchor); err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	target := shortRuntimeDir(t)
	if err := os.Mkdir(target+"/runtime", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, anchor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(anchor)
		_ = os.Rename(originalAnchor, anchor)
		_ = first.Close()
	})

	if _, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: secondBrokerGeneration,
	}); err == nil {
		t.Fatal("retargeted intermediate symlink started a second broker")
	}
	if _, err := os.Lstat(target + "/runtime/broker.lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected broker created a lock through retargeted path: %v", err)
	}
	if _, err := os.Lstat(originalAnchor + "/runtime/broker.lock"); err != nil {
		t.Fatalf("first broker lost its original lock: %v", err)
	}
}

func TestEndpointDescriptorRejectsPermissiveUnixMode(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	if err := prepareRuntimeDir(runtimeDir); err != nil {
		t.Fatal(err)
	}
	path := mustEndpointDescriptorPath(t, runtimeDir)
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEndpointDescriptor(runtimeDir); !errors.Is(err, ErrEndpointInvalid) {
		t.Fatalf("permissive descriptor error = %v", err)
	}
}

func TestGroupWritableAncestorNamesTheFix(t *testing.T) {
	parent := shortRuntimeDir(t)
	// Ubuntu's default umask for a new user (002) makes folders like this.
	if err := os.Chmod(parent, 0o775); err != nil {
		t.Fatal(err)
	}
	err := validateUnixAncestors(parent + "/runtime")
	if err == nil || !strings.Contains(err.Error(), "chmod go-w "+parent) {
		t.Fatalf("validateUnixAncestors = %v, want the chmod go-w fix for %s", err, parent)
	}
}
