package broker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

// PairingOriginSetting stores the origin printed in local pairing links.
const PairingOriginSetting = "pairing_origin"

var (
	errProjectPath  = errors.New("broker: project path is not an existing directory")
	errProjectLimit = errors.New("broker: saved project limit reached")
	errNotBrokerRPC = errors.New("broker: request is served by the gateway control endpoint")
)

func isAdminRPC(data []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(data, &probe) == nil && strings.HasPrefix(probe.Type, "admin.")
}

// serveAdminRequest answers one local administration request. The caller has
// already required an IPC cli peer; browsers and gateways never reach here.
func (server *ManagementServer) serveAdminRequest(ctx context.Context, writer *ipc.FrameWriter, data []byte) error {
	request, err := protocol.DecodeAdminRPCRequest(data)
	if err != nil {
		return err
	}
	payload, dispatchErr := server.dispatchAdmin(ctx, request)
	var response []byte
	if dispatchErr == nil {
		response, dispatchErr = encodeRPCResponse(request, protocol.AdminRPCResults[request.Type], payload,
			protocol.DecodeAdminRPCResponse)
	}
	if dispatchErr != nil {
		failure := classifyAdminError(dispatchErr)
		response, err = encodeRPCResponse(request, protocol.AdminError,
			protocol.BrokerRPCErrorPayload{Code: failure.code, Message: failure.message}, protocol.DecodeAdminRPCResponse)
		if err != nil {
			return err
		}
	}
	if err := writer.WriteFrame(response); err != nil {
		return err
	}
	if request.Type == protocol.AdminShutdown && dispatchErr == nil {
		// The result is written first; stopping the serve loop then closes
		// this connection with every other one.
		server.config.Shutdown()
	}
	return nil
}

func (server *ManagementServer) dispatchAdmin(ctx context.Context, request protocol.AdminRPCEnvelope) (any, error) { //nolint:gocyclo
	switch request.Type {
	case protocol.AdminShutdown:
		if server.config.Shutdown == nil {
			return nil, ErrRegistryClosed
		}
		terminated, err := server.config.Registry.TerminateAll(ctx)
		return protocol.AdminRPCShutdownResultPayload{TerminatedSessions: int64(terminated)}, err
	case protocol.AdminGatewayStop:
		return nil, errNotBrokerRPC
	case protocol.AdminGithubStatus, protocol.AdminGithubDisconnect, protocol.AdminGithubCredential:
		return server.dispatchAdminGitHub(ctx, request)
	case protocol.AdminChatStatus, protocol.AdminChatSet:
		return server.dispatchAdminChat(ctx, request)
	}
	authority := server.config.Auth
	if authority == nil {
		return nil, errAuthUnavailable
	}
	switch request.Type {
	case protocol.AdminPairGrant:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCPairGrantPayload](request)
		if err != nil {
			return nil, err
		}
		var app store.AppBinding
		if payload.Via != nil {
			if app, _, err = server.resolveVia(ctx, *payload.Via, payload.AllowTagged != nil && *payload.AllowTagged); err != nil {
				return nil, err
			}
		}
		grant, expires, err := authority.CreateBoundGrant(ctx, app)
		if err != nil {
			return nil, err
		}
		origin, err := server.pairingOrigin(ctx)
		result := protocol.AdminRPCPairGrantResultPayload{Grant: grant, ExpiresAt: formatRPCTime(expires), Origin: origin}
		result.AppOrigin, result.AppNode = wireNode(app)
		return result, err
	case protocol.AdminPairOffer:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCPairOfferPayload](request)
		if err != nil {
			return nil, err
		}
		return server.offerPairing(ctx, authority, payload.Via)
	case protocol.AdminPairOfferEnd:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCPairOfferEndPayload](request)
		if err != nil {
			return nil, err
		}
		return protocol.BrokerRPCEmptyPayload{}, server.endOffer(ctx, authority, payload.Grant)
	case protocol.AdminPairPending:
		pending, err := authority.Pending(ctx)
		if err != nil {
			return nil, err
		}
		result := protocol.AdminRPCPairPendingResultPayload{Pending: []protocol.AdminRPCPendingPairing{}}
		for _, record := range pending {
			pending := protocol.AdminRPCPendingPairing{
				ID: record.ID, VerificationCode: record.VerificationCode,
				DeviceLabel: record.DeviceLabel, ExpiresAt: formatRPCTime(record.ExpiresAt),
			}
			pending.AppOrigin, pending.AppNode = wireNode(record.App)
			result.Pending = append(result.Pending, pending)
		}
		return result, nil
	case protocol.AdminPairApprove:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCPendingIDPayload](request)
		if err != nil {
			return nil, err
		}
		return payload, authority.Approve(ctx, payload.PendingID)
	case protocol.AdminDeviceList:
		devices, err := authority.Devices(ctx)
		if err != nil {
			return nil, err
		}
		result := protocol.AdminRPCDeviceListResultPayload{Devices: []protocol.AdminRPCDevice{}}
		for _, device := range devices {
			record := protocol.AdminRPCDevice{
				ID: device.ID, Label: device.Label, Revoked: device.RevokedAt != nil,
				Status:    adminDeviceStatus(device.Status),
				CreatedAt: formatRPCTime(device.CreatedAt), LastUsedAt: formatRPCTime(device.LastUsedAt),
			}
			record.AppOrigin, record.AppNode = wireNode(device.App)
			result.Devices = append(result.Devices, record)
		}
		return result, nil
	case protocol.AdminDeviceRevoke:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceIDPayload](request)
		if err != nil {
			return nil, err
		}
		closed, err := authority.RevokeDevice(ctx, payload.DeviceID)
		return protocol.AdminRPCDeviceRevokeResultPayload{DeviceID: payload.DeviceID, ClosedStreams: int64(closed)}, err
	case protocol.AdminDeviceRevokeVia:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCRevokeViaPayload](request)
		if err != nil {
			return nil, err
		}
		revoked, closed, err := authority.RevokeVia(ctx, payload.AppOrigin)
		return protocol.AdminRPCRevokeViaResultPayload{
			AppOrigin: payload.AppOrigin, DeviceIDS: revoked, ClosedStreams: int64(closed),
		}, err
	case protocol.AdminComputersList:
		computers, err := server.config.Store.ListConfirmedComputers(ctx, protocol.MaxAdminComputers)
		if err != nil {
			return nil, err
		}
		result := protocol.AdminRPCComputerListResultPayload{Computers: []protocol.AdminRPCComputer{}}
		for _, computer := range computers {
			result.Computers = append(result.Computers, protocol.AdminRPCComputer{
				DeviceID: computer.DeviceID, Origin: computer.Origin, Label: computer.Label,
				CreatedAt: formatRPCTime(computer.CreatedAt),
			})
		}
		return result, nil
	case protocol.AdminComputersRemove:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCComputerRemovePayload](request)
		if err != nil {
			return nil, err
		}
		removed, err := server.config.Store.RemoveComputer(ctx, "", payload.Origin)
		return protocol.AdminRPCComputerRemoveResultPayload{Origin: payload.Origin, Removed: int64(removed)}, err
	case protocol.AdminDeviceRename:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceRenamePayload](request)
		if err != nil {
			return nil, err
		}
		return payload, authority.RenameDevice(ctx, payload.DeviceID, payload.Label)
	case protocol.AdminProjectAdd:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCProjectAddPayload](request)
		if err != nil {
			return nil, err
		}
		project, err := server.addProject(ctx, payload)
		return protocol.AdminRPCProjectResultPayload{Project: project}, err
	case protocol.AdminProjectList:
		projects, err := server.config.Store.ListProjects(ctx, protocol.MaxAdminProjects)
		if err != nil {
			return nil, err
		}
		result := protocol.AdminRPCProjectListResultPayload{Projects: []protocol.AdminRPCProject{}}
		for _, project := range projects {
			if record, ok := adminProject(project); ok {
				result.Projects = append(result.Projects, record)
			}
		}
		return result, nil
	case protocol.AdminProfileList, protocol.AdminProfileDetect:
		if request.Type == protocol.AdminProfileDetect {
			if err := DetectShellProfiles(ctx, server.config.Store, server.config.HostPlatform, time.Now()); err != nil {
				return nil, err
			}
		}
		profiles, err := server.config.Store.ListShellProfiles(ctx, protocol.MaxAdminProfiles)
		if err != nil {
			return nil, err
		}
		result := protocol.AdminRPCProfileListResultPayload{Profiles: []protocol.AdminRPCShellProfile{}}
		for _, profile := range profiles {
			if record, ok := adminShellProfile(profile); ok {
				result.Profiles = append(result.Profiles, record)
			}
		}
		return result, nil
	case protocol.AdminConfigOrigin:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCOriginPayload](request)
		if err != nil {
			return nil, err
		}
		if payload.Origin != nil {
			if err := server.config.Store.PutSetting(ctx, PairingOriginSetting, *payload.Origin, time.Now()); err != nil {
				return nil, err
			}
		}
		origin, err := server.pairingOrigin(ctx)
		return protocol.AdminRPCOriginPayload{Origin: origin}, err
	default:
		return nil, errors.New("broker: unsupported administration request")
	}
}

// pairingOrigin reads the configured origin and refreshes the own-origin
// cache that authorization reads.
func (server *ManagementServer) pairingOrigin(ctx context.Context) (*string, error) {
	value, ok, err := server.config.Store.GetSetting(ctx, PairingOriginSetting)
	if err != nil {
		return nil, err
	}
	if !ok || protocol.ValidatePairingOrigin(value) != nil {
		server.ownOrigin.Store(nil)
		return nil, nil
	}
	server.ownOrigin.Store(&value)
	return &value, nil
}

// addProject resolves the local path inside the broker, which owns the
// project registry. Re-adding the same directory returns the saved project.
func (server *ManagementServer) addProject(ctx context.Context, payload protocol.AdminRPCProjectAddPayload) (protocol.AdminRPCProject, error) {
	resolved, _, err := resolveProjectFolder(payload.Path)
	if err != nil {
		return protocol.AdminRPCProject{}, err
	}
	projects, err := server.config.Store.ListProjects(ctx, protocol.MaxAdminProjects+1)
	if err != nil {
		return protocol.AdminRPCProject{}, err
	}
	for _, project := range projects {
		if project.CanonicalPath == resolved {
			record, _ := adminProject(project)
			return record, nil
		}
	}
	if len(projects) >= protocol.MaxAdminProjects {
		return protocol.AdminRPCProject{}, errProjectLimit
	}
	name := filepath.Base(resolved)
	if payload.Name != nil {
		name = *payload.Name
	}
	if protocol.ValidateDisplayText(name, protocol.MaxNamedOptionBytes) != nil {
		name = "Project"
	}
	now := time.Now().UTC()
	project := store.Project{ID: uuid.New().String(), DisplayName: name, CanonicalPath: resolved, CreatedAt: now, UpdatedAt: now}
	if err := server.config.Store.PutProject(ctx, project); err != nil {
		return protocol.AdminRPCProject{}, err
	}
	record, _ := adminProject(project)
	return record, nil
}

// resolveProjectFolder is the project path check: an absolute path whose
// resolved form is an existing directory of bounded, valid UTF-8 length. It
// returns the resolved path and its FileInfo.
func resolveProjectFolder(path string) (string, os.FileInfo, error) {
	if !filepath.IsAbs(path) {
		return "", nil, errProjectPath
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", nil, errProjectPath
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", nil, errProjectPath
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() || len(resolved) > protocol.MaxAdminPathBytes || !utf8.ValidString(resolved) {
		return "", nil, errProjectPath
	}
	return resolved, info, nil
}

// adminDeviceStatus maps the store's live-credential status to the wire
// enum. An unrecognized store value fails closed to revoked rather than
// reporting a paired device as active.
func adminDeviceStatus(status store.DeviceStatus) protocol.AdminRPCDeviceStatus {
	switch status {
	case store.DeviceActive:
		return protocol.Active
	case store.DeviceExpired:
		return protocol.Expired
	default:
		return protocol.AdminRPCDeviceStatusRevoked
	}
}

func adminProject(project store.Project) (protocol.AdminRPCProject, bool) {
	option := namedOption(project.ID, project.DisplayName)
	record := protocol.AdminRPCProject{ID: project.ID, Name: option.Name, Path: project.CanonicalPath}
	return record, len(record.Path) <= protocol.MaxAdminPathBytes && utf8.ValidString(record.Path)
}

func adminShellProfile(profile store.ShellProfile) (protocol.AdminRPCShellProfile, bool) {
	option := namedOption(profile.ID, profile.DisplayName)
	if len(profile.ExecutablePath) > protocol.MaxAdminPathBytes || !utf8.ValidString(profile.ExecutablePath) ||
		len(profile.Arguments) > protocol.MaxAdminProfileArguments {
		return protocol.AdminRPCShellProfile{}, false
	}
	for _, argument := range profile.Arguments {
		if len(argument) > protocol.MaxAdminArgumentBytes || !utf8.ValidString(argument) {
			return protocol.AdminRPCShellProfile{}, false
		}
	}
	return protocol.AdminRPCShellProfile{
		ID: profile.ID, Name: option.Name, Executable: profile.ExecutablePath,
		Arguments: append([]string{}, profile.Arguments...),
	}, true
}

func classifyAdminError(err error) managementFailure {
	switch {
	case errors.Is(err, errProjectPath):
		return managementFailure{protocol.InvalidRequest, "project path must be an existing directory"}
	case errors.Is(err, errNotBrokerRPC):
		return managementFailure{protocol.InvalidRequest, "the broker does not stop the gateway; run pterm stop"}
	case errors.Is(err, errProjectLimit):
		return managementFailure{protocol.LimitExceeded, "saved project limit reached"}
	case errors.Is(err, errHomeChanged):
		return managementFailure{protocol.HomeChanged, "the app's computer is now a different Tailscale machine"}
	case errors.Is(err, errNoOfferOrigin):
		return managementFailure{protocol.InvalidRequest,
			"this computer has no tailnet https address; run `pterm setup` or `pterm config origin https://<this computer>`"}
	default:
		return classifySessionError(err)
	}
}
