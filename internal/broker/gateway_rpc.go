package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

const fallbackOptionName = "Unnamed"

var errAuthUnavailable = errors.New("broker: authentication service is not configured")

// serveGatewayRequest answers one correlated gateway request. A malformed
// request poisons the connection, as in management RPC. A successful
// gateway.live.open returns the credential binding that now owns the
// connection; every other request leaves it in RPC mode.
func (server *ManagementServer) serveGatewayRequest(
	ctx context.Context, writer *ipc.FrameWriter, data []byte,
) (*auth.Binding, error) {
	request, err := protocol.DecodeGatewayRPCRequest(data)
	if err != nil {
		return nil, err
	}
	payload, binding, dispatchErr := server.dispatchGateway(ctx, request)
	var response []byte
	if dispatchErr == nil {
		response, dispatchErr = encodeRPCResponse(request, protocol.GatewayRPCResults[request.Type], payload,
			protocol.DecodeGatewayRPCResponse)
	}
	if dispatchErr != nil {
		if binding != nil {
			binding.Release()
			binding = nil
		}
		failure := classifySessionError(dispatchErr)
		response, err = encodeRPCResponse(request, protocol.GatewayError,
			protocol.BrokerRPCErrorPayload{Code: failure.code, Message: failure.message}, protocol.DecodeGatewayRPCResponse)
		if err != nil {
			return nil, err
		}
	}
	if err := writer.WriteFrame(response); err != nil {
		if binding != nil {
			binding.Release()
		}
		return nil, err
	}
	return binding, nil
}

// serveBoundLiveStream requires the canonical browser hello next. The binding
// is released on every path that does not hand it to the live stream.
func (server *ManagementServer) serveBoundLiveStream(
	ctx context.Context, conn net.Conn, reader *ipc.FrameReader,
	peer protocol.IPCHelloClass, binding *auth.Binding,
) error {
	stopWatch := make(chan struct{})
	go func() {
		select {
		case <-binding.Revoked():
			_ = conn.SetReadDeadline(time.Now())
		case <-stopWatch:
		}
	}()
	data, err := reader.ReadFrame()
	close(stopWatch)
	if err != nil {
		binding.Release()
		return err
	}
	hello, err := protocol.DecodeClient(data)
	if err != nil || hello.Type != protocol.Hello {
		binding.Release()
		return errors.Join(errors.New("broker: bound live stream must start with hello"), err)
	}
	return server.serveSessionConnection(ctx, conn, reader, peer, hello, binding)
}

func (server *ManagementServer) dispatchGateway( //nolint:gocyclo
	ctx context.Context, request protocol.GatewayRPCEnvelope,
) (any, *auth.Binding, error) {
	authority := server.config.Auth
	if authority == nil {
		return nil, nil, errAuthUnavailable
	}
	// Every credential-carrying request is authorized before any read,
	// mutation, or operation-receipt replay.
	authorize := func(credential string) (auth.Principal, error) {
		return authority.Authenticate(ctx, credential)
	}
	switch request.Type {
	case protocol.GatewayPairRedeem:
		payload, err := protocol.DecodeRPCPayload[protocol.HTTPPairRedeemRequest](request)
		if err != nil {
			return nil, nil, err
		}
		result, err := authority.Redeem(ctx, payload.Grant, payload.DeviceLabel)
		return result, nil, err
	case protocol.GatewayPairPoll:
		payload, err := protocol.DecodeRPCPayload[protocol.HTTPPairPollRequest](request)
		if err != nil {
			return nil, nil, err
		}
		result, err := authority.Poll(ctx, payload.PendingID, payload.PollSecret)
		if err != nil {
			return nil, nil, err
		}
		response := protocol.GatewayRPCPairPollResultPayload{Response: protocol.HTTPPairPollResponse{Status: protocol.Pending}}
		if result.Approved {
			expires := formatRPCTime(result.ExpiresAt)
			response.Response = protocol.HTTPPairPollResponse{Status: protocol.Approved,
				Device: &protocol.HTTPDeviceSummary{ID: result.DeviceID, Label: result.DeviceLabel}}
			response.Credential, response.CredentialExpiresAt = &result.Credential, &expires
		}
		return response, nil, nil
	case protocol.GatewayAuthCheck:
		principal, err := authorizeCredential(request, authorize)
		if err != nil {
			return nil, nil, err
		}
		return protocol.GatewayRPCPrincipalPayload{
			DeviceID: principal.DeviceID, DeviceLabel: principal.DeviceLabel,
			CredentialID: principal.CredentialID, ExpiresAt: formatRPCTime(principal.ExpiresAt),
		}, nil, nil
	case protocol.GatewayAuthLogout:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCCredentialPayload](request)
		if err != nil {
			return nil, nil, err
		}
		return protocol.BrokerRPCEmptyPayload{}, nil, authority.Logout(ctx, payload.Credential)
	case protocol.GatewaySettings:
		principal, err := authorizeCredential(request, authorize)
		if err != nil {
			return nil, nil, err
		}
		result, err := server.browserSettings(ctx, principal)
		return result, nil, err
	case protocol.GatewaySessionList:
		principal, err := authorizeCredential(request, authorize)
		if err != nil {
			return nil, nil, err
		}
		sessions, err := server.config.Store.ListSessions(ctx)
		if err != nil {
			return nil, nil, err
		}
		result := protocol.HTTPSessionListResponse{Sessions: []protocol.BrokerRPCSessionRecord{}}
		for _, session := range sessions[:min(len(sessions), protocol.MaxHTTPSessionList)] {
			result.Sessions = append(result.Sessions, server.withPresence(ctx, managementSessionRecord(session), principal.DeviceID))
		}
		return result, nil, nil
	case protocol.GatewaySessionCreate:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCSessionCreatePayload](request)
		if err != nil {
			return nil, nil, err
		}
		if _, err := authorize(payload.Credential); err != nil {
			return nil, nil, err
		}
		result, err := server.createSession(ctx, protocol.BrokerRPCSessionCreateRequestPayload{
			OperationID: payload.Request.OperationID, ProjectID: payload.Request.ProjectID,
			ShellProfileID: payload.Request.ShellProfileID,
		})
		return protocol.HTTPSessionMutationResponse{OperationID: result.OperationID, Session: result.Session}, nil, err
	case protocol.GatewaySessionRename:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCSessionRenamePayload](request)
		if err != nil {
			return nil, nil, err
		}
		if _, err := authorize(payload.Credential); err != nil {
			return nil, nil, err
		}
		result, err := server.config.Registry.Rename(ctx, RenameSessionRequest{
			OperationID: payload.Request.OperationID, SessionID: payload.SessionID,
			Generation: payload.Request.Generation, Title: payload.Request.Title,
		})
		if err != nil {
			return nil, nil, err
		}
		return protocol.HTTPSessionMutationResponse{
			OperationID: payload.Request.OperationID, Session: managementMutationSessionRecord(result.Snapshot),
		}, nil, nil
	case protocol.GatewaySessionTerminate:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCSessionTerminatePayload](request)
		if err != nil {
			return nil, nil, err
		}
		if _, err := authorize(payload.Credential); err != nil {
			return nil, nil, err
		}
		result, err := server.config.Registry.Terminate(ctx, TerminateSessionRequest{
			OperationID: payload.Request.OperationID, SessionID: payload.SessionID, Generation: payload.Request.Generation,
		})
		if err != nil {
			return nil, nil, err
		}
		return protocol.HTTPSessionMutationResponse{
			OperationID: payload.Request.OperationID, Session: managementMutationSessionRecord(result.Snapshot),
		}, nil, nil
	case protocol.GatewayLiveOpen:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCCredentialPayload](request)
		if err != nil {
			return nil, nil, err
		}
		binding, _, err := authority.Bind(ctx, payload.Credential)
		if err != nil {
			return nil, nil, err
		}
		return protocol.BrokerRPCEmptyPayload{}, binding, nil
	default:
		return nil, nil, errors.New("broker: unsupported gateway request")
	}
}

func authorizeCredential(
	request protocol.GatewayRPCEnvelope, authorize func(string) (auth.Principal, error),
) (auth.Principal, error) {
	payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCCredentialPayload](request)
	if err != nil {
		return auth.Principal{}, err
	}
	return authorize(payload.Credential)
}

// browserSettings exposes saved projects and shell profiles by ID and name
// only. Paths, executables and arguments never leave the broker toward a browser.
func (server *ManagementServer) browserSettings(ctx context.Context, principal auth.Principal) (protocol.HTTPSettingsResponse, error) {
	projects, err := server.config.Store.ListProjects(ctx, protocol.MaxSettingsProjects)
	if err != nil {
		return protocol.HTTPSettingsResponse{}, err
	}
	profiles, err := server.config.Store.ListShellProfiles(ctx, protocol.MaxSettingsShellProfiles)
	if err != nil {
		return protocol.HTTPSettingsResponse{}, err
	}
	result := protocol.HTTPSettingsResponse{
		Device:           protocol.HTTPDeviceSummary{ID: principal.DeviceID, Label: principal.DeviceLabel},
		Projects:         []protocol.HTTPNamedOption{},
		ShellProfiles:    []protocol.HTTPNamedOption{},
		HostBuild:        server.config.HostBuild,
		BrokerGeneration: server.config.BrokerGeneration,
	}
	for _, project := range projects {
		result.Projects = append(result.Projects, namedOption(project.ID, project.DisplayName))
	}
	for _, profile := range profiles {
		if server.profileMatchesHost(profile.Platform) {
			result.ShellProfiles = append(result.ShellProfiles, namedOption(profile.ID, profile.DisplayName))
		}
	}
	return result, nil
}

func namedOption(id, name string) protocol.HTTPNamedOption {
	if protocol.ValidateDisplayText(name, protocol.MaxNamedOptionBytes) != nil {
		name = fallbackOptionName
	}
	return protocol.HTTPNamedOption{ID: id, Name: name}
}

// encodeRPCResponse validates the exact bytes before they are sent, so a
// broker bug cannot emit an out-of-contract response.
func encodeRPCResponse[T ~string](
	request protocol.RPCEnvelope[T], responseType T, payload any,
	validate func([]byte) (protocol.RPCEnvelope[T], error),
) ([]byte, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("broker: encode RPC payload: %w", err)
	}
	data, err := json.Marshal(protocol.RPCEnvelope[T]{
		Version: protocol.Version, Type: responseType, RequestID: request.RequestID, Payload: payloadJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("broker: encode RPC response: %w", err)
	}
	if _, err := validate(data); err != nil {
		return nil, fmt.Errorf("broker: validate RPC response: %w", err)
	}
	return data, nil
}

func formatRPCTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func classifyAuthError(err error) (managementFailure, bool) {
	switch {
	case errors.Is(err, auth.ErrRevoked), errors.Is(err, ErrAccessRevoked):
		return managementFailure{protocol.Revoked, "browser access was revoked"}, true
	case errors.Is(err, auth.ErrUnauthorized), errors.Is(err, auth.ErrExpired), errors.Is(err, auth.ErrPollSecret):
		return managementFailure{protocol.Unauthorized, "browser is not authorized"}, true
	case errors.Is(err, auth.ErrPairingExpired):
		return managementFailure{protocol.PairingExpired, "pairing is expired or already used; start pairing again"}, true
	case errors.Is(err, auth.ErrRateLimited), errors.Is(err, auth.ErrCapacity):
		return managementFailure{protocol.LimitExceeded, "pairing limit reached; wait and try again"}, true
	case errors.Is(err, auth.ErrInvalidLabel):
		return managementFailure{protocol.InvalidRequest, "device label is invalid"}, true
	case errors.Is(err, auth.ErrApproved):
		return managementFailure{protocol.InvalidRequest, "pending pairing was already approved"}, true
	case errors.Is(err, auth.ErrDeviceMissing), errors.Is(err, store.ErrDeviceMissing):
		return managementFailure{protocol.InvalidRequest, "device does not exist"}, true
	case errors.Is(err, auth.ErrCleanup):
		return managementFailure{protocol.InternalError,
			"revocation is saved, but some live connections did not confirm closure in time"}, true
	case errors.Is(err, errAuthUnavailable):
		return managementFailure{protocol.HostUnavailable, "broker authentication is unavailable"}, true
	}
	return managementFailure{}, false
}
