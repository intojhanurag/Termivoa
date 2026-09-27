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

func TestCurrentUserSecurityDescriptorChecksEveryACE(t *testing.T) {
	currentSID, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	user := currentSID.String()
	for dacl, wantOK := range map[string]bool{
		// Windows splits an inheritable GENERIC_ALL grant into these two ACEs.
		"D:P(A;OICIIO;GA;;;" + user + ")(A;;FA;;;" + user + ")": true,
		"D:P(A;OICI;FA;;;" + user + ")":                         true,
		"D:P":                                                   false,
		"D:P(A;;FA;;;" + user + ")(A;;FA;;;SY)":                 false,
		"D:P(D;;FA;;;WD)(A;;FA;;;" + user + ")":                 false,
	} {
		descriptor, err := windows.SecurityDescriptorFromString("O:" + user + dacl)
		if err != nil {
			t.Fatal(err)
		}
		err = validateCurrentUserSecurityDescriptor(descriptor, currentSID, true)
		if (err == nil) != wantOK {
			t.Errorf("%s: validation error = %v, want ok %v", dacl, err, wantOK)
		}
	}
}
