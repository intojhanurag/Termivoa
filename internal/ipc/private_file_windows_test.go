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

func TestPrivateFileWindowsDeniedRepairRetainsAdministratorAdvice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(path, []byte("synthetic non-secret fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	setup, err := openPrivateWindowsFile(path, windows.WRITE_OWNER, windows.OPEN_EXISTING)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
		t.Skip("token cannot open synthetic owner-setup handle")
	}
	if err != nil {
		t.Fatal(err)
	}
	handle := windows.Handle(setup.Fd())
	t.Cleanup(func() {
		defer setup.Close()
		acl, err := currentUserFileACL(sid, false)
		if err != nil {
			t.Error(err)
			return
		}
		if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
			windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
			sid, nil, acl, nil); err != nil {
			t.Error("restore synthetic owner/DACL:", err)
		}
	})
	// OWNER RIGHTS suppresses the owner's implicit WRITE_DAC grant, including
	// an enabled Administrators owner SID in an elevated native test process.
	limited, err := windows.SecurityDescriptorFromString("O:BAD:P(D;;WD;;;" + sid.String() + ")(A;;RC;;;OW)(A;;0x20080;;;" + sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	owner, _, err := limited.Owner()
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := limited.DACL()
	if err != nil {
		t.Fatal(err)
	}
	err = windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, acl, nil)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) || errors.Is(err, windows.ERROR_INVALID_OWNER) {
		t.Skip("token cannot assign Administrators ownership to synthetic fixture")
	}
	if err != nil {
		t.Fatal(err)
	}
	before := privateWindowsDescriptor(t, path)
	inspection, err := openWindowsFileObject(path, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING)
	if err != nil {
		t.Fatal("fixture must allow nonmutating inspection:", err)
	}
	inspection.Close()
	repair, err := openPrivateWindowsFile(path, 0, windows.OPEN_EXISTING)
	if repair != nil {
		repair.Close()
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("fixture repair-open error = %v, want access denied", err)
	}
	err = EnsurePrivateFile(path)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || !strings.Contains(err.Error(), "icacls") || !strings.Contains(err.Error(), "without elevation") {
		t.Fatalf("production denied-open advice/cause = %v", err)
	}
	if before != privateWindowsDescriptor(t, path) {
		t.Fatal("denied repair changed owner/DACL")
	}
	// If even READ_CONTROL is denied, ownership must not be inferred from the
	// pathname or the earlier fixture state. Keep the original access error.
	uninspectable, err := windows.SecurityDescriptorFromString("D:P(D;;RCWD;;;" + sid.String() + ")(A;;0x80;;;OW)(A;;0x80;;;" + sid.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err = uninspectable.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	inspection, err = openWindowsFileObject(path, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING)
	if inspection != nil {
		inspection.Close()
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("fixture inspection error = %v, want access denied", err)
	}
	err = EnsurePrivateFile(path)
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || strings.Contains(err.Error(), "icacls") {
		t.Fatalf("uninspectable ownership was inferred: %v", err)
	}
}
