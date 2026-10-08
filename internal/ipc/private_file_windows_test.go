//go:build windows

package ipc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func privateWindowsDescriptor(t *testing.T, path string) string {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	return descriptor.String()
}

func TestPrivateFileWindowsRefusesLinksWithoutACLMutation(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			outside, path := filepath.Join(dir, "outside"), filepath.Join(dir, "private")
			if err := os.WriteFile(outside, []byte("synthetic non-secret fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := privateWindowsDescriptor(t, outside)
			var err error
			if kind == "symlink" {
				err = os.Symlink(outside, path)
			} else {
				err = os.Link(outside, path)
			}
			if kind == "symlink" && errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
				t.Skip("Windows symlink privilege unavailable")
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := EnsurePrivateFile(path); err == nil {
				t.Fatal("linked object accepted")
			}
			if before != privateWindowsDescriptor(t, outside) {
				t.Fatal("refusal changed outside owner or DACL")
			}
		})
	}
}

func TestPrivateFileWindowsRepairsSameObjectAndPreventsReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(path, []byte("synthetic non-secret fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	loose, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + sid.String() + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := loose.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentUserACL(path); err == nil {
		t.Fatal("loose fixture unexpectedly private")
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := openPrivateWindowsFile(path, 0, windows.OPEN_EXISTING)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Rename(path, path+"-moved"); !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		t.Fatalf("protected rename error = %v", err)
	}
	if err := securePrivateWindowsFile(file, path); err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentUserACL(path); err != nil {
		t.Fatal(err)
	}
	after, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("repair changed file identity")
	}
}

func TestPrivateFileWindowsRefusesDirectoryWithoutACLMutation(t *testing.T) {
	path := t.TempDir()
	before := privateWindowsDescriptor(t, path)
	if err := EnsurePrivateFile(path); err == nil {
		t.Fatal("directory accepted")
	}
	if before != privateWindowsDescriptor(t, path) {
		t.Fatal("refusal changed directory ACL")
	}
}

func TestPrivateFileWindowsOwnerRecoveryAdvicePreservesCause(t *testing.T) {
	cause := windows.ERROR_ACCESS_DENIED
	err := windowsOwnerRecoveryError("synthetic-file", cause)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "icacls") || !strings.Contains(err.Error(), "without elevation") {
		t.Fatal("owner-recovery advice or cause lost")
	}
}
