//go:build linux

package ipc

import (
	"errors"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

func verifyPlatformPeer(connection net.Conn) error {
	unixConnection, ok := connection.(*net.UnixConn)
	if !ok {
		return errors.New("local listener returned a non-Unix connection")
	}
	raw, err := unixConnection.SyscallConn()
	if err != nil {
		return err
	}
	var credentials *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credentials, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if socketErr != nil {
		return socketErr
	}
	if credentials == nil || credentials.Uid != uint32(os.Geteuid()) {
		return ErrPeerUnauthorized
	}
	return nil
}
