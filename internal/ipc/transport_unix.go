//go:build darwin || linux

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const unixTransport = "unix"

// Darwin permits 103 pathname bytes plus the terminating NUL. Linux permits
// a few more, but the product requires one descriptor format that is portable
// across the supported Unix development and macOS release environments.
const unixSocketPathMax = 103

type unixBrokerLock struct {
	file *os.File
}

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
	if err := requireCurrentUnixOwner(info); err != nil {
		return fmt.Errorf("runtime directory ownership: %w", err)
	}
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		return err
	}
	info, err = os.Lstat(runtimeDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("runtime directory changed while being secured")
	}
	if err := requireCurrentUnixOwner(info); err != nil {
		return fmt.Errorf("runtime directory ownership: %w", err)
	}
	return nil
}

func acquireBrokerLock(runtimeDir string) (brokerLock, error) {
	path := filepath.Join(runtimeDir, "broker.lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("ipc: open broker lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("ipc: wrap broker lock file")
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("ipc: inspect broker lock: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("ipc: broker lock is not a regular file")
	}
	if err := requireCurrentUnixOwner(info); err != nil {
		return nil, fmt.Errorf("ipc: broker lock ownership: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 1 {
		return nil, errors.New("ipc: broker lock must have exactly one link")
	}
	if err := file.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("ipc: restrict broker lock: %w", err)
	}
	info, err = file.Stat()
	if err != nil || info.Mode().Perm() != 0o600 {
		return nil, errors.New("ipc: broker lock permissions could not be secured")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrBrokerAlreadyRunning
		}
		return nil, fmt.Errorf("ipc: acquire broker lock: %w", err)
	}
	closeOnError = false
	return &unixBrokerLock{file: file}, nil
}

func (lock *unixBrokerLock) Close() error {
	if lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockErr, closeErr)
}

func platformEndpoint(runtimeDir string) (transport, address string, err error) {
	address = filepath.Join(runtimeDir, "broker.sock")
	if len(address) > unixSocketPathMax {
		return "", "", fmt.Errorf("ipc: Unix socket path is %d bytes, maximum is %d", len(address), unixSocketPathMax)
	}
	return unixTransport, address, nil
}

func listenPlatform(address string) (net.Listener, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	return listener, nil
}

func dialPlatform(ctx context.Context, address string) (net.Conn, error) {
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, "unix", address)
}

func securePlatformEndpoint(address string) error {
	if err := os.Chmod(address, 0o600); err != nil {
		return err
	}
	info, err := os.Lstat(address)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		return errors.New("Unix endpoint is not a mode-0600 socket")
	}
	return requireCurrentUnixOwner(info)
}

func removePlatformEndpoint(address string) error {
	info, err := os.Lstat(address)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("refuse to remove a non-socket endpoint path")
	}
	if err := requireCurrentUnixOwner(info); err != nil {
		return err
	}
	return os.Remove(address)
}

func validatePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("path is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("file mode is %04o, want 0600", info.Mode().Perm())
	}
	return requireCurrentUnixOwner(info)
}

func securePrivateFile(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return validatePrivateFile(path)
}

func requireCurrentUnixOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("Unix file ownership is unavailable")
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("owner UID is %d, want %d", stat.Uid, os.Geteuid())
	}
	return nil
}

func syncRuntimeDir(runtimeDir string) error {
	directory, err := os.Open(runtimeDir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
