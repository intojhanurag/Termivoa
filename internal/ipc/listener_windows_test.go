//go:build windows

package ipc

import (
	"context"
	"path/filepath"
	"testing"
)

func TestBrokerListenerAppliesCurrentUserWindowsACLs(t *testing.T) {
	runtimeDir := shortRuntimeDir(t)
	listener, err := ListenBroker(context.Background(), ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: testBrokerGeneration,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, path := range []string{
		runtimeDir,
		filepath.Join(runtimeDir, "broker.lock"),
		mustEndpointDescriptorPath(t, runtimeDir),
	} {
		if err := validateCurrentUserACL(path); err != nil {
			t.Fatalf("%s ACL: %v", path, err)
		}
	}
}
