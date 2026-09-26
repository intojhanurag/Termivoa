package localclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// GatewayControlDir is the private runtime subdirectory that holds a running
// gateway's stop endpoint. The gateway publishes it with the same machinery
// as the broker endpoint: a current-user-only socket or pipe, an OS
// single-instance lock, and a descriptor whose random token the handshake
// must return. No process ID is trusted and no process is signaled.
func GatewayControlDir(runtimeDir string) string { return filepath.Join(runtimeDir, "gateway") }

// StopGateway asks the gateway serving runtimeDir to exit and waits until it
// has removed its endpoint. It reports false when no gateway is running,
// including a stale endpoint left by a gateway that crashed.
func StopGateway(ctx context.Context, runtimeDir, clientBuild string) (bool, error) {
	directory := GatewayControlDir(runtimeDir)
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	connection, _, _, err := ipc.DialBroker(ctx, directory, protocol.CLI, clientBuild)
	if errors.Is(err, ipc.ErrEndpointMissing) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	client, err := newAdminClient(connection, newUUID)
	if err != nil {
		_ = connection.Close()
		return false, err
	}
	_, err = adminCall[protocol.BrokerRPCEmptyPayload](ctx, client, protocol.AdminGatewayStop,
		protocol.BrokerRPCEmptyPayload{}, true, nil)
	_ = client.Close()
	if err != nil {
		return false, err
	}
	for {
		if _, err := ipc.ReadEndpointDescriptor(directory); errors.Is(err, ipc.ErrEndpointMissing) {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, errors.New("the gateway accepted the stop request but has not exited yet")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
