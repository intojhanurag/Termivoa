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

	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

// PairingOriginSetting stores the origin printed in local pairing links.
const PairingOriginSetting = "pairing_origin"

var (
	errProjectPath  = errors.New("broker: project path is not an existing directory")
	errProjectLimit = errors.New("broker: saved project limit reached")
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
	return writer.WriteFrame(response)
}

func (server *ManagementServer) dispatchAdmin(ctx context.Context, request protocol.AdminRPCEnvelope) (any, error) { //nolint:gocyclo
	authority := server.config.Auth
	if authority == nil {
		return nil, errAuthUnavailable
	}
	switch request.Type {
	case protocol.AdminPairGrant:
		grant, expires, err := authority.CreateGrant(ctx)
		if err != nil {
			return nil, err
		}
		origin, err := server.pairingOrigin(ctx)
		return protocol.AdminRPCPairGrantResultPayload{Grant: grant, ExpiresAt: formatRPCTime(expires), Origin: origin}, err
	case protocol.AdminPairPending:
		pending, err := authority.Pending(ctx)
		if err != nil {
			return nil, err
		}
		result := protocol.AdminRPCPairPendingResultPayload{Pending: []protocol.AdminRPCPendingPairing{}}
		for _, record := range pending {
			result.Pending = append(result.Pending, protocol.AdminRPCPendingPairing{
				ID: record.ID, VerificationCode: record.VerificationCode,
				DeviceLabel: record.DeviceLabel, ExpiresAt: formatRPCTime(record.ExpiresAt),
			})
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
			result.Devices = append(result.Devices, protocol.AdminRPCDevice{
				ID: device.ID, Label: device.Label, Revoked: device.RevokedAt != nil,
				CreatedAt: formatRPCTime(device.CreatedAt), LastUsedAt: formatRPCTime(device.LastUsedAt),
			})
		}
		return result, nil
	case protocol.AdminDeviceRevoke:
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCDeviceIDPayload](request)
		if err != nil {
			return nil, err
		}
		closed, err := authority.RevokeDevice(ctx, payload.DeviceID)
		return protocol.AdminRPCDeviceRevokeResultPayload{DeviceID: payload.DeviceID, ClosedStreams: int64(closed)}, err
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

func (server *ManagementServer) pairingOrigin(ctx context.Context) (*string, error) {
	value, ok, err := server.config.Store.GetSetting(ctx, PairingOriginSetting)
	if err != nil || !ok || protocol.ValidatePairingOrigin(value) != nil {
		return nil, err
	}
	return &value, nil
}

// addProject resolves the local path inside the broker, which owns the
// project registry. Re-adding the same directory returns the saved project.
func (server *ManagementServer) addProject(ctx context.Context, payload protocol.AdminRPCProjectAddPayload) (protocol.AdminRPCProject, error) {
	if !filepath.IsAbs(payload.Path) {
		return protocol.AdminRPCProject{}, errProjectPath
	}
	resolved, err := filepath.EvalSymlinks(payload.Path)
	if err != nil {
		return protocol.AdminRPCProject{}, errProjectPath
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return protocol.AdminRPCProject{}, errProjectPath
	}
	if info, err := os.Stat(resolved); err != nil || !info.IsDir() || len(resolved) > protocol.MaxAdminPathBytes ||
		!utf8.ValidString(resolved) {
		return protocol.AdminRPCProject{}, errProjectPath
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
	id, err := randomUUID()
	if err != nil {
		return protocol.AdminRPCProject{}, err
	}
	now := time.Now().UTC()
	project := store.Project{ID: id, DisplayName: name, CanonicalPath: resolved, CreatedAt: now, UpdatedAt: now}
	if err := server.config.Store.PutProject(ctx, project); err != nil {
		return protocol.AdminRPCProject{}, err
	}
	record, _ := adminProject(project)
	return record, nil
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
	case errors.Is(err, errProjectLimit):
		return managementFailure{protocol.LimitExceeded, "saved project limit reached"}
	default:
		return classifySessionError(err)
	}
}
