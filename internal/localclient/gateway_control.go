package localclient

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// GatewaySocketName is the ipc.ListenerConfig.SocketName the gateway's stop
// control endpoint publishes under, inside the broker's own private,
// already-hardened runtime directory. It shares that directory (and its
// prepare/lock/permission/peer-check machinery) with the broker's own
// "broker.*" family rather than nesting in a subdirectory: ipc always names
// a socket "<dir>/<name>.sock", so any subdirectory would make the control
// socket longer than the broker's own "<runtimeDir>/broker.sock" and could
// cross the platform's Unix-socket path limit for a long home directory even
// when the broker still fits. "gw.sock" is 4 bytes shorter than
// "broker.sock", so it never exceeds the broker's own budget.
const GatewaySocketName = "gw"

// StopGateway asks the gateway serving runtimeDir to exit and waits until it
// has removed its endpoint. It reports false when no gateway is running,
// including a stale endpoint left by a gateway that crashed.
func StopGateway(ctx context.Context, runtimeDir, clientBuild string) (bool, error) {
	if _, err := os.Stat(runtimeDir); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	connection, _, _, err := ipc.DialNamedBroker(ctx, runtimeDir, GatewaySocketName, protocol.CLI, clientBuild)
	if errors.Is(err, ipc.ErrEndpointMissing) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	client, err := newAdminClient(connection, newRequestID)
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
		if _, err := ipc.ReadNamedEndpointDescriptor(runtimeDir, GatewaySocketName); errors.Is(err, ipc.ErrEndpointMissing) {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return true, errors.New("the gateway accepted the stop request but has not exited yet")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
