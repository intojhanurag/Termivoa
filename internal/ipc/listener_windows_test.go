//go:build windows

package ipc

import (
	"context"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
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

func TestCurrentUserSecurityDescriptorRejectsForeignOwner(t *testing.T) {
	currentSID, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"O:SYD:P(A;;GA;;;" + currentSID.String() + ")",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentUserSecurityDescriptor(descriptor, currentSID, true); err == nil {
		t.Fatal("foreign-owned descriptor passed current-user validation")
	}
}

func TestCurrentUserSecurityDescriptorAcceptsCurrentOwner(t *testing.T) {
	currentSID, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"O:" + currentSID.String() + "D:P(A;;GA;;;" + currentSID.String() + ")",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentUserSecurityDescriptor(descriptor, currentSID, true); err != nil {
		t.Fatalf("current-user descriptor rejected: %v", err)
	}
}
