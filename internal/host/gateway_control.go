package host

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const gatewayControlTimeout = 5 * time.Second

// GatewayControl is a running gateway's stop endpoint, used by `pterm stop`
// and `pterm shutdown`. It reuses the broker endpoint machinery under
// localclient.GatewaySocketName: a current-user-only socket or pipe inside
// the broker's own private runtime directory, a single-instance OS lock and
// a descriptor carrying a random token that the handshake returns. It
// accepts only admin.gateway.stop from a cli peer.
type GatewayControl struct {
	listener *ipc.BrokerListener
	done     chan struct{}
}

// ListenGatewayControl publishes the endpoint. stop is called once a stop
// result has been written; it must make the gateway exit cleanly.
func ListenGatewayControl(ctx context.Context, runtimeDir, hostBuild string, stop func()) (*GatewayControl, error) {
	if stop == nil {
		return nil, errors.New("host: gateway stop function must not be nil")
	}
	token, err := newUUID()
	if err != nil {
		return nil, err
	}
	listener, err := ipc.ListenBroker(ctx, ipc.ListenerConfig{
		RuntimeDir: runtimeDir, BrokerGeneration: token, SocketName: localclient.GatewaySocketName,
	})
	if errors.Is(err, ipc.ErrBrokerAlreadyRunning) {
		return nil, errors.New("host: a gateway is already running for this runtime directory")
	}
	if err != nil {
		return nil, err
	}
	control := &GatewayControl{listener: listener, done: make(chan struct{})}
	go func() {
		defer close(control.done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			if serveGatewayControl(ctx, connection, token, hostBuild) {
				stop()
			}
		}
	}()
	return control, nil
}

// Close removes the endpoint. Its descriptor disappears last, which is how
// StopGateway observes that the gateway has exited.
func (control *GatewayControl) Close() error {
	err := control.listener.Close()
	<-control.done
	return err
}

// serveGatewayControl answers one bounded exchange and reports whether the
// peer asked the gateway to stop.
func serveGatewayControl(ctx context.Context, connection net.Conn, token, hostBuild string) bool {
	defer connection.Close()
	hello, err := ipc.ServerHandshake(ctx, connection, token, hostBuild)
	if err != nil || hello.ClientKind != protocol.CLI {
		return false
	}
	if connection.SetDeadline(time.Now().Add(gatewayControlTimeout)) != nil {
		return false
	}
	reader, _ := ipc.NewFrameReader(connection, protocol.MaxInboundMessageBytes)
	data, err := reader.ReadFrame()
	if err != nil {
		return false
	}
	request, err := protocol.DecodeAdminRPCRequest(data)
	if err != nil {
		return false
	}
	responseType, payload := protocol.AdminGatewayStopResult, any(protocol.BrokerRPCEmptyPayload{})
	if request.Type != protocol.AdminGatewayStop {
		responseType, payload = protocol.AdminError, protocol.BrokerRPCErrorPayload{
			Code: protocol.InvalidRequest, Message: "the gateway control endpoint only stops the gateway",
		}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	response, err := json.Marshal(protocol.AdminRPCEnvelope{
		Version: protocol.Version, Type: responseType, RequestID: request.RequestID, Payload: payloadJSON,
	})
	if err != nil {
		return false
	}
	writer, _ := ipc.NewFrameWriter(connection, protocol.MaxOutboundMessageBytes)
	return writer.WriteFrame(response) == nil && responseType == protocol.AdminGatewayStopResult
}
