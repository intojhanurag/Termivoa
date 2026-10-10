package localclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// AdminClient serializes bounded local administration exchanges on one CLI
// IPC connection. It reuses ManagementClient's transport, slot and poisoning
// rules; only the admin RPC schema travels through this client. A pairing
// grant is returned to the caller and never placed in an error.
type AdminClient struct {
	transport *ManagementClient
}

// DialAdmin authenticates as a local CLI. The broker accepts admin RPC only
// from a cli peer, so no other role can reach these operations.
func DialAdmin(ctx context.Context, runtimeDir, clientBuild string) (*AdminClient, error) {
	return DialNamedAdmin(ctx, runtimeDir, "broker", clientBuild)
}

// DialNamedAdmin is DialAdmin for an endpoint published under a SocketName
// other than the default broker, such as the gateway's stop control endpoint
// sharing the broker's own runtime directory (see GatewaySocketName).
func DialNamedAdmin(ctx context.Context, runtimeDir, name, clientBuild string) (*AdminClient, error) {
	connection, _, _, err := ipc.DialNamedBroker(ctx, runtimeDir, name, protocol.CLI, clientBuild)
	if err != nil {
		return nil, err
	}
	client, err := newAdminClient(connection, newRequestID)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return client, nil
}

func newAdminClient(connection net.Conn, requestID RequestIDSource) (*AdminClient, error) {
	transport, err := newManagementClient(connection, requestID)
	if err != nil {
		return nil, err
	}
	return &AdminClient{transport: transport}, nil
}

// Close interrupts an active exchange and makes the client unusable.
func (client *AdminClient) Close() error { return client.transport.Close() }

// CreatePairingGrant returns the only delivery of a fresh two-minute grant.
func (client *AdminClient) CreatePairingGrant(ctx context.Context) (protocol.AdminRPCPairGrantResultPayload, error) {
	return adminCall[protocol.AdminRPCPairGrantResultPayload](ctx, client, protocol.AdminPairGrant,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
}

// CreateBoundPairingGrant is CreatePairingGrant bound to one app origin
// (pterm pair --via): the broker resolves via in its own tailscale status,
// applies the owner rule and pins the node it reports in the result.
func (client *AdminClient) CreateBoundPairingGrant(
	ctx context.Context, request protocol.AdminRPCPairGrantPayload,
) (protocol.AdminRPCPairGrantResultPayload, error) {
	return adminCall[protocol.AdminRPCPairGrantResultPayload](ctx, client, protocol.AdminPairGrant, request, false, nil)
}

// OfferPairing asks the broker to offer this computer to the home computer
// via (pterm pair --via, while it waits): the broker sends its one live grant
// bound to that home there. The result never holds the grant.
func (client *AdminClient) OfferPairing(ctx context.Context, via string) (protocol.AdminRPCPairOfferResultPayload, error) {
	return adminCall[protocol.AdminRPCPairOfferResultPayload](ctx, client, protocol.AdminPairOffer,
		protocol.AdminRPCPairOfferPayload{Via: via}, false, nil)
}

// EndOffer deletes the offered grant and link (the link's grant), each only
// when it was not redeemed, and ends the offer.
func (client *AdminClient) EndOffer(ctx context.Context, link *string) error {
	_, err := adminCall[protocol.BrokerRPCEmptyPayload](ctx, client, protocol.AdminPairOfferEnd,
		protocol.AdminRPCPairOfferEndPayload{Grant: link}, true, nil)
	return err
}

// RevokeDevicesVia revokes every device bound to appOrigin, with the same
// durability and stream-closing rule as RevokeDevice.
func (client *AdminClient) RevokeDevicesVia(ctx context.Context, appOrigin string) (protocol.AdminRPCRevokeViaResultPayload, error) {
	return adminCall(ctx, client, protocol.AdminDeviceRevokeVia, protocol.AdminRPCRevokeViaPayload{AppOrigin: appOrigin}, true,
		func(result protocol.AdminRPCRevokeViaResultPayload) error {
			if result.AppOrigin != appOrigin {
				return errors.New("admin revoke-via origin mismatch")
			}
			return nil
		})
}

// Computers lists the confirmed computers of every device on this home computer.
func (client *AdminClient) Computers(ctx context.Context) ([]protocol.AdminRPCComputer, error) {
	result, err := adminCall[protocol.AdminRPCComputerListResultPayload](ctx, client, protocol.AdminComputersList,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
	return result.Computers, err
}

// RemoveComputer removes origin from every device's computer list.
func (client *AdminClient) RemoveComputer(ctx context.Context, origin string) (protocol.AdminRPCComputerRemoveResultPayload, error) {
	return adminCall(ctx, client, protocol.AdminComputersRemove, protocol.AdminRPCComputerRemovePayload{Origin: origin}, true,
		func(result protocol.AdminRPCComputerRemoveResultPayload) error {
			if result.Origin != origin {
				return errors.New("admin computer removal origin mismatch")
			}
			return nil
		})
}

func (client *AdminClient) PendingPairings(ctx context.Context) ([]protocol.AdminRPCPendingPairing, error) {
	result, err := adminCall[protocol.AdminRPCPairPendingResultPayload](ctx, client, protocol.AdminPairPending,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
	return result.Pending, err
}

// ApprovePairing approves exactly one pending ID. An acknowledgement for a
// different ID poisons the connection and reports uncertain delivery.
func (client *AdminClient) ApprovePairing(ctx context.Context, pendingID string) error {
	_, err := adminCall(ctx, client, protocol.AdminPairApprove, protocol.AdminRPCPendingIDPayload{PendingID: pendingID}, true,
		func(result protocol.AdminRPCPendingIDPayload) error {
			if result.PendingID != pendingID {
				return errors.New("admin approval pending ID mismatch")
			}
			return nil
		})
	return err
}

func (client *AdminClient) Devices(ctx context.Context) ([]protocol.AdminRPCDevice, error) {
	result, err := adminCall[protocol.AdminRPCDeviceListResultPayload](ctx, client, protocol.AdminDeviceList,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
	return result.Devices, err
}

// RevokeDevice succeeds only after the broker made the revocation durable and
// closed the device's live streams. A transport failure after the write is a
// DeliveryUncertainError; callers must inspect devices rather than retry blindly.
func (client *AdminClient) RevokeDevice(ctx context.Context, deviceID string) (protocol.AdminRPCDeviceRevokeResultPayload, error) {
	return adminCall(ctx, client, protocol.AdminDeviceRevoke, protocol.AdminRPCDeviceIDPayload{DeviceID: deviceID}, true,
		func(result protocol.AdminRPCDeviceRevokeResultPayload) error {
			if result.DeviceID != deviceID {
				return errors.New("admin revocation device ID mismatch")
			}
			return nil
		})
}

// RenameDevice sets a paired device's display label.
func (client *AdminClient) RenameDevice(ctx context.Context, deviceID, label string) error {
	request := protocol.AdminRPCDeviceRenamePayload{DeviceID: deviceID, Label: label}
	_, err := adminCall(ctx, client, protocol.AdminDeviceRename, request, true,
		func(result protocol.AdminRPCDeviceRenamePayload) error {
			if result != request {
				return errors.New("admin rename acknowledgement mismatch")
			}
			return nil
		})
	return err
}

// AddProject sends a path the broker validates and canonicalizes.
func (client *AdminClient) AddProject(ctx context.Context, path string, name *string) (protocol.AdminRPCProject, error) {
	result, err := adminCall[protocol.AdminRPCProjectResultPayload](ctx, client, protocol.AdminProjectAdd,
		protocol.AdminRPCProjectAddPayload{Path: path, Name: name}, true, nil)
	return result.Project, err
}

func (client *AdminClient) Projects(ctx context.Context) ([]protocol.AdminRPCProject, error) {
	result, err := adminCall[protocol.AdminRPCProjectListResultPayload](ctx, client, protocol.AdminProjectList,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
	return result.Projects, err
}

func (client *AdminClient) ShellProfiles(ctx context.Context) ([]protocol.AdminRPCShellProfile, error) {
	result, err := adminCall[protocol.AdminRPCProfileListResultPayload](ctx, client, protocol.AdminProfileList,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
	return result.Profiles, err
}

// DetectShellProfiles runs the broker's idempotent host shell discovery.
func (client *AdminClient) DetectShellProfiles(ctx context.Context) ([]protocol.AdminRPCShellProfile, error) {
	result, err := adminCall[protocol.AdminRPCProfileListResultPayload](ctx, client, protocol.AdminProfileDetect,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
	return result.Profiles, err
}

// PairingOrigin reads the configured origin when origin is nil and replaces
// it otherwise. The result is nil when no origin is configured.
func (client *AdminClient) PairingOrigin(ctx context.Context, origin *string) (*string, error) {
	result, err := adminCall(ctx, client, protocol.AdminConfigOrigin, protocol.AdminRPCOriginPayload{Origin: origin}, origin != nil,
		func(result protocol.AdminRPCOriginPayload) error {
			if origin != nil && (result.Origin == nil || *result.Origin != *origin) {
				return errors.New("admin origin acknowledgement mismatch")
			}
			return nil
		})
	return result.Origin, err
}

// Shutdown asks the broker to terminate every live session through the
// normal termination path and then stop. A transport failure after the write
// is a DeliveryUncertainError.
func (client *AdminClient) Shutdown(ctx context.Context) (protocol.AdminRPCShutdownResultPayload, error) {
	return adminCall[protocol.AdminRPCShutdownResultPayload](ctx, client, protocol.AdminShutdown,
		protocol.BrokerRPCEmptyPayload{}, true, nil)
}

// ChatStatus reads this computer's Chat view switch and follow state.
func (client *AdminClient) ChatStatus(ctx context.Context) (protocol.AdminRPCChatStatusPayload, error) {
	return adminCall[protocol.AdminRPCChatStatusPayload](ctx, client, protocol.AdminChatStatus,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
}

// ChatSet turns Chat view on or off for this computer. Only pterm chat on,
// after a y/N answer, turns it on.
func (client *AdminClient) ChatSet(ctx context.Context, enabled bool) (protocol.AdminRPCChatStatusPayload, error) {
	return adminCall[protocol.AdminRPCChatStatusPayload](ctx, client, protocol.AdminChatSet,
		protocol.AdminRPCChatSetPayload{Enabled: enabled}, true, nil)
}

// GitHubStatus reads this computer's GitHub state and login. State is nil
// when this build has no GitHub feature.
func (client *AdminClient) GitHubStatus(ctx context.Context) (protocol.AdminRPCGitHubStatusPayload, error) {
	return adminCall[protocol.AdminRPCGitHubStatusPayload](ctx, client, protocol.AdminGithubStatus,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
}

// GitHubDisconnect removes this computer's GitHub login and answers the new state.
func (client *AdminClient) GitHubDisconnect(ctx context.Context) (protocol.AdminRPCGitHubStatusPayload, error) {
	return adminCall[protocol.AdminRPCGitHubStatusPayload](ctx, client, protocol.AdminGithubDisconnect,
		protocol.BrokerRPCEmptyPayload{}, true, nil)
}

// GitHubCredential returns the access token for git's credential helper. The
// token is only in the result, never in an error.
func (client *AdminClient) GitHubCredential(ctx context.Context) (protocol.AdminRPCGitHubCredentialPayload, error) {
	return adminCall[protocol.AdminRPCGitHubCredentialPayload](ctx, client, protocol.AdminGithubCredential,
		protocol.BrokerRPCEmptyPayload{}, false, nil)
}

// adminCall performs one serialized exchange. Correlation, the exact result
// type, and any caller check are verified inside the exchange slot, so a
// malformed acknowledgement closes the connection before a queued call writes.
func adminCall[P any](
	ctx context.Context,
	client *AdminClient,
	requestType protocol.AdminRPCMessageType,
	payload any,
	mutation bool,
	check func(P) error,
) (P, error) {
	var zero P
	transport := client.transport
	if ctx == nil {
		return zero, errors.New("local client context must not be nil")
	}
	data, request, err := client.request(requestType, payload)
	if err != nil {
		return zero, err
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-transport.done:
		return zero, net.ErrClosed
	case <-transport.slot:
	}
	defer func() { transport.slot <- struct{}{} }()
	select {
	case <-transport.done:
		return zero, net.ErrClosed
	default:
	}
	clearDeadline, err := setContextDeadline(ctx, transport.connection)
	if err != nil {
		return zero, err
	}
	defer clearDeadline()

	fail := func(err error) (P, error) {
		_ = transport.Close()
		return zero, deliveryError(ctx, err, mutation)
	}
	if err := transport.writer.WriteFrame(data); err != nil {
		return fail(err)
	}
	responseData, err := transport.reader.ReadFrame()
	if err != nil {
		return fail(err)
	}
	response, err := protocol.DecodeAdminRPCResponse(responseData)
	if err != nil {
		return fail(err)
	}
	if response.RequestID != request.RequestID {
		return fail(errors.New("admin response request ID mismatch"))
	}
	if response.Type == protocol.AdminError {
		remote, decodeErr := protocol.DecodeRPCPayload[protocol.BrokerRPCErrorPayload](response)
		if decodeErr != nil {
			return fail(decodeErr)
		}
		return zero, &RemoteError{Code: remote.Code, Message: remote.Message}
	}
	if response.Type != protocol.AdminRPCResults[requestType] {
		return fail(fmt.Errorf("unexpected admin response type %q", response.Type))
	}
	result, err := protocol.DecodeRPCPayload[P](response)
	if err != nil {
		return fail(err)
	}
	if check != nil {
		if err := check(result); err != nil {
			return fail(err)
		}
	}
	return result, nil
}

func (client *AdminClient) request(
	requestType protocol.AdminRPCMessageType,
	payload any,
) ([]byte, protocol.AdminRPCEnvelope, error) {
	transport := client.transport
	transport.requestMu.Lock()
	defer transport.requestMu.Unlock()
	requestID := transport.requestID()
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, protocol.AdminRPCEnvelope{}, fmt.Errorf("encode admin request payload: %w", err)
	}
	data, err := json.Marshal(protocol.AdminRPCEnvelope{
		Version: protocol.Version, Type: requestType, RequestID: requestID, Payload: payloadJSON,
	})
	if err != nil {
		return nil, protocol.AdminRPCEnvelope{}, fmt.Errorf("encode admin request: %w", err)
	}
	request, err := protocol.DecodeAdminRPCRequest(data)
	if err != nil {
		return nil, protocol.AdminRPCEnvelope{}, err
	}
	return data, request, nil
}
