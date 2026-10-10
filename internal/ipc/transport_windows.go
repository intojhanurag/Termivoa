//go:build windows

package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const windowsPipeTransport = "windows-named-pipe"

type windowsBrokerLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

// Windows runtime paths retain their clean absolute spelling. Reparse-point
// and ownership hardening requires native Windows validation and remains part
// of the recorded Windows evidence gate.
func canonicalRuntimeDir(runtimeDir string) (string, error) { return runtimeDir, nil }

func prepareRuntimeDir(runtimeDir string) error {
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(runtimeDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("runtime path is not a real directory")
	}
	if err := applyCurrentUserACL(runtimeDir, true); err != nil {
		return err
	}
	return validateCurrentUserACL(runtimeDir)
}

func acquireBrokerLock(directory, filename string) (brokerLock, error) {
	path := filepath.Join(directory, filename)
	file, err := openPrivateWindowsFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_ALWAYS)
	if err != nil {
		return nil, fmt.Errorf("ipc: open broker lock: %w", privateWindowsOpenError(path, err))
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	if err := securePrivateWindowsFile(file, path); err != nil {
		return nil, fmt.Errorf("ipc: restrict broker lock: %w", err)
	}
	lock := &windowsBrokerLock{file: file}
	err = windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &lock.overlapped,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, ErrBrokerAlreadyRunning
		}
		return nil, fmt.Errorf("ipc: acquire broker lock: %w", err)
	}
	closeOnError = false
	return lock, nil
}

func (lock *windowsBrokerLock) Close() error {
	if lock.file == nil {
		return nil
	}
	unlockErr := windows.UnlockFileEx(windows.Handle(lock.file.Fd()), 0, 1, 0, &lock.overlapped)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

// platformEndpointNamed is platformEndpoint for a non-default SocketName. A
// non-default name folds into the pipe-address digest so a second endpoint
// (such as the gateway's stop control) sharing the runtime directory gets its
// own pipe. The default broker pipe address is unchanged.
func platformEndpointNamed(runtimeDir, name string) (transport, address string, err error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", "", fmt.Errorf("ipc: resolve current Windows user: %w", err)
	}
	key := strings.ToLower(runtimeDir)
	if name != defaultSocketName {
		key += "\x00" + name
	}
	digest := sha256.Sum256([]byte(key + "\x00" + sid.String()))
	return windowsPipeTransport, `\\.\pipe\pterm-` + hex.EncodeToString(digest[:16]), nil
}

func listenPlatform(address string) (net.Listener, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(address, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + sid.String() + ")",
		MessageMode:        false,
		InputBufferSize:    int32(protocol.MaxInboundMessageBytes),
		OutputBufferSize:   int32(protocol.MaxOutboundMessageBytes),
	})
}

func dialPlatform(ctx context.Context, address string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, address)
}

func securePlatformEndpoint(string) error { return nil }

func removePlatformEndpoint(string) error { return nil }

// The protected named-pipe DACL is the peer-user boundary on Windows. A pipe
// cannot be opened by another user, so no post-accept impersonation is needed.
func verifyPlatformPeer(net.Conn) error { return nil }

func validatePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("path is not a regular file")
	}
	return validateCurrentUserACL(path)
}

func securePrivateFile(path string) error {
	file, err := openPrivateWindowsFile(path, 0, windows.OPEN_EXISTING)
	if err != nil {
		return privateWindowsOpenError(path, err)
	}
	defer file.Close()
	return securePrivateWindowsFile(file, path)
}

func openPrivateWindowsFile(path string, extraAccess, creation uint32) (*os.File, error) {
	return openWindowsFileObject(path, windows.READ_CONTROL|windows.WRITE_DAC|windows.FILE_READ_ATTRIBUTES|extraAccess, creation)
}

func openWindowsFileObject(path string, access, creation uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, creation,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), path), nil
}

// A failed repair open has no handle on which to inspect ownership. Use a
// separate read-only security/attribute handle solely to select existing
// recovery advice. Never mutate it or infer ownership when inspection fails.
func privateWindowsOpenError(path string, openErr error) error {
	if !errors.Is(openErr, windows.ERROR_ACCESS_DENIED) {
		return openErr
	}
	file, err := openWindowsFileObject(path, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING)
	if err != nil {
		return openErr
	}
	defer file.Close()
	handle := windows.Handle(file.Fd())
	if err := validatePrivateWindowsObject(handle); err != nil {
		return openErr
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return openErr
	}
	owner, _, err := descriptor.Owner()
	if err != nil || owner == nil || !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return openErr
	}
	return windowsOwnerRecoveryError(path, openErr)
}

func validatePrivateWindowsObject(handle windows.Handle) error {
	typeID, err := windows.GetFileType(handle)
	if err != nil {
		return err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if typeID != windows.FILE_TYPE_DISK || info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		return errors.New("path is not a regular non-reparse file")
	}
	if info.NumberOfLinks != 1 {
		return errors.New("private file must have exactly one hard link")
	}
	return nil
}

func securePrivateWindowsFile(file *os.File, path string) error {
	handle := windows.Handle(file.Fd())
	if err := validatePrivateWindowsObject(handle); err != nil {
		return err
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	descriptor, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return err
	}
	if owner == nil {
		return errors.New("file has no owner")
	}
	securityInfo := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	var newOwner *windows.SID
	if !owner.Equals(sid) {
		if !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return errors.New("file owner is not the current user")
		}
		// Keep the original no-delete-sharing handle open during recovery so a
		// pathname replacement cannot redirect the second open.
		recovery, err := openPrivateWindowsFile(path, windows.WRITE_OWNER, windows.OPEN_EXISTING)
		if err != nil {
			return windowsOwnerRecoveryError(path, err)
		}
		defer recovery.Close()
		originalInfo, err := file.Stat()
		if err != nil {
			return err
		}
		recoveryInfo, err := recovery.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(originalInfo, recoveryInfo) {
			return errors.New("private file path changed during owner recovery")
		}
		handle = windows.Handle(recovery.Fd())
		if err := validatePrivateWindowsObject(handle); err != nil {
			return err
		}
		// Re-read ownership on the handle that will receive the mutation.
		descriptor, err = windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		owner, _, err = descriptor.Owner()
		if err != nil || owner == nil || (!owner.Equals(sid) && !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
			return errors.New("file owner changed during owner recovery")
		}
		securityInfo |= windows.OWNER_SECURITY_INFORMATION
		newOwner = sid
	}
	acl, err := currentUserFileACL(sid, false)
	if err != nil {
		return err
	}
	if err := windows.SetSecurityInfo(handle, windows.SE_FILE_OBJECT, securityInfo, newOwner, nil, acl, nil); err != nil {
		if newOwner != nil {
			return windowsOwnerRecoveryError(path, err)
		}
		return err
	}
	if err := validatePrivateWindowsObject(handle); err != nil {
		return err
	}
	descriptor, err = windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	if err := validateCurrentUserSecurityDescriptor(descriptor, sid, true); err != nil {
		return err
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("private file DACL is not protected")
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !pathInfo.Mode().IsRegular() || !os.SameFile(info, pathInfo) {
		return errors.New("private file path changed during permission repair")
	}
	return validatePrivateWindowsObject(handle)
}

func windowsOwnerRecoveryError(path string, err error) error {
	return fmt.Errorf(
		"%s is owned by Administrators (most likely an elevated pterm run created it) and this user cannot take it back: %w; "+
			"from an elevated prompt run: icacls \"%s\" /setowner <your user name> /T /C, then run pterm again without elevation",
		path, err, path,
	)
}

// fileAllAccess is FILE_ALL_ACCESS. A specific mask, unlike GENERIC_ALL, is
// stored as one ACE even when it is inheritable, so the directory DACL stays a
// single current-user ACE instead of an effective ACE plus an inherit-only one.
const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1FF

func applyCurrentUserACL(path string, inherit bool) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	owner, err := fileOwner(path)
	if err != nil {
		return err
	}
	// An elevated run creates objects owned by BUILTIN\Administrators. Take
	// them back for the current user in the same call that sets the DACL; the
	// access check uses the old DACL, which (inherited from the user profile)
	// grants the user WRITE_DAC and WRITE_OWNER. Any other foreign owner is
	// still rejected so another user cannot pre-create a path we then trust.
	securityInfo := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	var newOwner *windows.SID
	if !owner.Equals(sid) {
		if !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return fmt.Errorf("%s: file owner %s is not the current user", path, owner)
		}
		securityInfo |= windows.OWNER_SECURITY_INFORMATION
		newOwner = sid
	}
	acl, err := currentUserFileACL(sid, inherit)
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInfo, newOwner, nil, acl, nil)
	if err != nil && newOwner != nil {
		return windowsOwnerRecoveryError(path, err)
	}
	return err
}

func currentUserFileACL(sid *windows.SID, inherit bool) (*windows.ACL, error) {
	var pinner runtime.Pinner
	pinner.Pin(sid)
	defer pinner.Unpin()
	inheritance := uint32(windows.NO_INHERITANCE)
	if inherit {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: fileAllAccess,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm: windows.TRUSTEE_IS_SID, TrusteeType: windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}
	return windows.ACLFromEntries(entries, nil)
}

func validateCurrentUserACL(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	// GetNamedSecurityInfo in x/sys v0.47.0 copies the native descriptor onto
	// the Go heap and calls LocalFree before returning. Callers must not free
	// this returned descriptor a second time.
	descriptor, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return err
	}
	return validateCurrentUserSecurityDescriptor(descriptor, sid, true)
}

func fileOwner(path string) (*windows.SID, error) {
	// The x/sys wrapper returns a Go-owned copy; see validateCurrentUserACL.
	descriptor, err := windows.GetNamedSecurityInfo(
		path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		return nil, err
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return nil, fmt.Errorf("read file owner: %w", err)
	}
	if owner == nil {
		return nil, errors.New("file has no owner")
	}
	return owner, nil
}

func validateCurrentUserSecurityDescriptor(
	descriptor *windows.SECURITY_DESCRIPTOR,
	sid *windows.SID,
	requireACL bool,
) error {
	if descriptor == nil {
		return errors.New("file has no security descriptor")
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read file owner: %w", err)
	}
	if owner == nil || !owner.Equals(sid) {
		return errors.New("file owner is not the current user")
	}
	if !requireACL {
		return nil
	}
	acl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	// A nil DACL grants everyone access and an empty one grants nobody.
	if acl == nil || acl.AceCount == 0 {
		return errors.New("file DACL is not current-user-only")
	}
	// Every ACE, including inherit-only ones, must allow the current user.
	for index := range uint32(acl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil {
			return err
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("file DACL contains an ACE that is not an allow ACE")
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !aceSID.Equals(sid) {
			return errors.New("file DACL grants a principal other than the current user")
		}
	}
	return nil
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	if user == nil || user.User.Sid == nil {
		return nil, errors.New("current Windows token has no user SID")
	}
	return user.User.Sid, nil
}

func syncRuntimeDir(string) error { return nil }

// RequireCurrentOwner accepts every file: Windows guards folders with ACLs
// that FileInfo does not describe, and herdr (its only caller) does not run
// on Windows.
func RequireCurrentOwner(os.FileInfo) error { return nil }
