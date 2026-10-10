package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxDeviceLabelBytes      = 64
	MaxNamedOptionBytes      = 128
	MaxSettingsProjects      = 200
	MaxSettingsShellProfiles = 64
	MaxHTTPSessionList       = 200
	MaxAdminPendingPairings  = 16
	MaxAdminDevices          = 200
	MaxAdminProjects         = 200
	MaxAdminProfiles         = 64
	MaxAdminProfileArguments = 16
	MaxAdminArgumentBytes    = 256
	MaxAdminPathBytes        = 4096
	MaxAdminShutdownSessions = 64
	MaxPairingOriginBytes    = 512
	SecretTextBytes          = 43
)

// RPCEnvelope is the shared correlated envelope of the gateway and local
// administration RPC schemas. It carries no session identity: every
// session-scoped field lives in a validated payload. Origin is the browser
// request's Origin, forwarded by the gateway; only gateway requests carry it.
type RPCEnvelope[T ~string] struct {
	Version   int             `json:"version"`
	Type      T               `json:"type"`
	RequestID string          `json:"requestId"`
	Origin    *string         `json:"origin,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

type (
	GatewayRPCEnvelope = RPCEnvelope[GatewayRPCMessageType]
	AdminRPCEnvelope   = RPCEnvelope[AdminRPCMessageType]
)

// GatewayRPCResults maps each gateway request to its only successful result.
var GatewayRPCResults = map[GatewayRPCMessageType]GatewayRPCMessageType{
	GatewayPairRedeem: GatewayPairRedeemResult, GatewayPairPoll: GatewayPairPollResult,
	GatewayAuthCheck: GatewayAuthCheckResult, GatewayAuthLogout: GatewayAuthLogoutResult,
	GatewaySettings: GatewaySettingsResult, GatewaySessionList: GatewaySessionListResult,
	GatewaySessionCreate: GatewaySessionCreateResult, GatewaySessionRename: GatewaySessionRenameResult,
	GatewaySessionTerminate: GatewaySessionTerminateResult, GatewayLiveOpen: GatewayLiveOpenResult,
	GatewayOrigin: GatewayOriginResult, GatewayDeviceRename: GatewayDeviceRenameResult,
	GatewayPushConfig: GatewayPushConfigResult, GatewayPushSubscribe: GatewayPushSubscribeResult,
	GatewayPushUnsubscribe: GatewayPushUnsubscribeResult, GatewayPushTest: GatewayPushTestResult,
	GatewayUploadBegin: GatewayUploadBeginResult, GatewayUploadChunk: GatewayUploadChunkResult,
	GatewayUploadCommit:  GatewayUploadCommitResult,
	GatewayPreviewCheck:  GatewayPreviewCheckResult,
	GatewayComputersList: GatewayComputersListResult, GatewayComputersAdd: GatewayComputersAddResult,
	GatewayComputersRemove: GatewayComputersRemoveResult,
	GatewayComputersOffer:  GatewayComputersOfferResult, GatewayComputersFound: GatewayComputersFoundResult,
	GatewayProvidersGet: GatewayProvidersGetResult, GatewayProvidersUpdate: GatewayProvidersUpdateResult,
	GatewayProvidersOpen: GatewayProvidersOpenResult, GatewayProvidersCreate: GatewayProvidersCreateResult,
	GatewayProvidersManage: GatewayProvidersManageResult,
	GatewayGithubGet:       GatewayGithubGetResult, GatewayGithubAction: GatewayGithubActionResult,
	GatewayGithubRepos: GatewayGithubReposResult,
	GatewayChatUpdate:  GatewayChatUpdateResult,
}

// AdminRPCResults maps each local administration request to its result.
var AdminRPCResults = map[AdminRPCMessageType]AdminRPCMessageType{
	AdminPairGrant: AdminPairGrantResult, AdminPairPending: AdminPairPendingResult,
	AdminPairApprove: AdminPairApproveResult, AdminDeviceList: AdminDeviceListResult,
	AdminDeviceRevoke: AdminDeviceRevokeResult, AdminProjectAdd: AdminProjectAddResult,
	AdminProjectList: AdminProjectListResult, AdminProfileList: AdminProfileListResult,
	AdminProfileDetect: AdminProfileDetectResult, AdminConfigOrigin: AdminConfigOriginResult,
	AdminShutdown: AdminShutdownResult, AdminGatewayStop: AdminGatewayStopResult,
	AdminDeviceRename: AdminDeviceRenameResult, AdminDeviceRevokeVia: AdminDeviceRevokeViaResult,
	AdminComputersList: AdminComputersListResult, AdminComputersRemove: AdminComputersRemoveResult,
	AdminPairOffer: AdminPairOfferResult, AdminPairOfferEnd: AdminPairOfferEndResult,
	AdminGithubStatus: AdminGithubStatusResult, AdminGithubDisconnect: AdminGithubDisconnectResult,
	AdminGithubCredential: AdminGithubCredentialResult,
	AdminChatStatus:       AdminChatStatusResult, AdminChatSet: AdminChatSetResult,
}

func DecodeGatewayRPCRequest(data []byte) (GatewayRPCEnvelope, error) {
	return decodeRPC(data, true, true, gatewayRPCRequestTypes(), GatewayError, validateGatewayRPCPayload)
}

func DecodeGatewayRPCResponse(data []byte) (GatewayRPCEnvelope, error) {
	return decodeRPC(data, false, false, gatewayRPCRequestTypes(), GatewayError, validateGatewayRPCPayload)
}

func DecodeAdminRPCRequest(data []byte) (AdminRPCEnvelope, error) {
	return decodeRPC(data, true, false, adminRPCRequestTypes(), AdminError, validateAdminRPCPayload)
}

func DecodeAdminRPCResponse(data []byte) (AdminRPCEnvelope, error) {
	return decodeRPC(data, false, false, adminRPCRequestTypes(), AdminError, validateAdminRPCPayload)
}

// DecodeRPCPayload strictly decodes a payload from an already validated envelope.
func DecodeRPCPayload[P any, T ~string](envelope RPCEnvelope[T]) (P, error) {
	var payload P
	if err := strictDecode(envelope.Payload, &payload); err != nil {
		return payload, fmt.Errorf("protocol: %s payload: %w", envelope.Type, err)
	}
	return payload, nil
}

func gatewayRPCRequestTypes() map[GatewayRPCMessageType]bool {
	types := make(map[GatewayRPCMessageType]bool, len(GatewayRPCResults))
	for request := range GatewayRPCResults {
		types[request] = true
	}
	return types
}

func adminRPCRequestTypes() map[AdminRPCMessageType]bool {
	types := make(map[AdminRPCMessageType]bool, len(AdminRPCResults))
	for request := range AdminRPCResults {
		types[request] = true
	}
	return types
}

func decodeRPC[T ~string](
	data []byte, request, allowOrigin bool, requests map[T]bool, errorType T,
	validate func(RPCEnvelope[T]) error,
) (RPCEnvelope[T], error) {
	limit := MaxOutboundMessageBytes
	if request {
		limit = MaxInboundMessageBytes
	}
	if len(data) == 0 || len(data) > limit {
		return RPCEnvelope[T]{}, fmt.Errorf("protocol: RPC message size %d is outside 1..%d bytes", len(data), limit)
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return RPCEnvelope[T]{}, err
	}
	var envelope RPCEnvelope[T]
	if err := strictDecode(data, &envelope); err != nil {
		return RPCEnvelope[T]{}, fmt.Errorf("protocol: decode RPC envelope: %w", err)
	}
	if envelope.Version != Version {
		return RPCEnvelope[T]{}, fmt.Errorf("protocol: RPC version %d is not supported", envelope.Version)
	}
	if !ValidUUID(envelope.RequestID) {
		return RPCEnvelope[T]{}, errors.New("protocol: RPC requestId must be a canonical UUID")
	}
	if envelope.Origin != nil {
		if !allowOrigin {
			return RPCEnvelope[T]{}, errors.New("protocol: only gateway requests carry an origin")
		}
		if err := ValidatePairingOrigin(*envelope.Origin); err != nil {
			return RPCEnvelope[T]{}, fmt.Errorf("protocol: RPC origin: %w", err)
		}
	}
	isRequest := requests[envelope.Type]
	isResponse := envelope.Type == errorType
	if !isResponse {
		for requestType := range requests {
			if string(envelope.Type) == string(requestType)+".result" {
				isResponse = true
				break
			}
		}
	}
	if request && !isRequest || !request && !isResponse {
		return RPCEnvelope[T]{}, fmt.Errorf("protocol: %q is not valid in this RPC direction", envelope.Type)
	}
	if err := validate(envelope); err != nil {
		return RPCEnvelope[T]{}, fmt.Errorf("protocol: %s payload: %w", envelope.Type, err)
	}
	return envelope, nil
}

func validateGatewayRPCPayload(envelope GatewayRPCEnvelope) error { //nolint:gocyclo
	switch envelope.Type {
	case GatewayPairRedeem:
		payload, err := DecodeRPCPayload[HTTPPairRedeemRequest](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Grant); err != nil {
			return err
		}
		return ValidateDeviceLabel(payload.DeviceLabel)
	case GatewayPairPoll:
		payload, err := DecodeRPCPayload[HTTPPairPollRequest](envelope)
		if err != nil {
			return err
		}
		if !ValidUUID(payload.PendingID) {
			return errors.New("pendingId must be a canonical UUID")
		}
		return ValidateSecretText(payload.PollSecret)
	case GatewayAuthCheck, GatewayAuthLogout, GatewaySettings, GatewaySessionList, GatewayLiveOpen,
		GatewayPushConfig, GatewayPushTest, GatewayComputersList, GatewayComputersFound:
		payload, err := DecodeRPCPayload[GatewayRPCCredentialPayload](envelope)
		if err != nil {
			return err
		}
		return ValidateSecretText(payload.Credential)
	case GatewaySessionCreate:
		payload, err := DecodeRPCPayload[GatewayRPCSessionCreatePayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		if !ValidUUID(payload.Request.OperationID) || !ValidUUID(payload.Request.ProjectID) || !ValidUUID(payload.Request.ShellProfileID) {
			return errors.New("create request contains a non-canonical UUID")
		}
		return ValidateWorktreeCreate(payload.Request.Worktree)
	case GatewaySessionRename:
		payload, err := DecodeRPCPayload[GatewayRPCSessionRenamePayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		if !ValidUUID(payload.SessionID) || !ValidUUID(payload.Request.OperationID) || !ValidUUID(payload.Request.Generation) {
			return errors.New("rename request contains a non-canonical UUID")
		}
		return validateBrokerRPCText(payload.Request.Title, MaxBrokerRPCRenameTitleBytes, "title")
	case GatewaySessionTerminate:
		payload, err := DecodeRPCPayload[GatewayRPCSessionTerminatePayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		if !ValidUUID(payload.SessionID) || !ValidUUID(payload.Request.OperationID) || !ValidUUID(payload.Request.Generation) {
			return errors.New("terminate request contains a non-canonical UUID")
		}
		return nil
	case GatewayOrigin:
		_, err := DecodeRPCPayload[BrokerRPCEmptyPayload](envelope)
		return err
	case GatewayOriginResult:
		payload, err := DecodeRPCPayload[GatewayRPCOriginResultPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Origin != nil {
			if err := ValidatePairingOrigin(*payload.Origin); err != nil {
				return err
			}
		}
		if err := validateOriginSet(payload.BoundOrigins, MaxBoundOrigins, true, "boundOrigins"); err != nil {
			return err
		}
		return validateOriginSet(payload.PairingOrigins, MaxPairingOrigins, true, "pairingOrigins")
	case GatewayComputersAdd, GatewayComputersRemove, GatewayComputersListResult, GatewayComputersAddResult,
		GatewayComputersOffer, GatewayComputersFoundResult:
		return validateGatewayComputerPayload(envelope)
	case GatewayDeviceRename:
		payload, err := DecodeRPCPayload[GatewayRPCDeviceRenamePayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		return ValidateDeviceLabel(payload.Request.Label)
	case GatewayDeviceRenameResult:
		payload, err := DecodeRPCPayload[HTTPDeviceSummary](envelope)
		if err != nil {
			return err
		}
		if !ValidUUID(payload.ID) {
			return errors.New("device id must be a canonical UUID")
		}
		return ValidateDeviceLabel(payload.Label)
	case GatewayPushSubscribe:
		payload, err := DecodeRPCPayload[GatewayRPCPushSubscribePayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		return ValidatePushSubscription(payload.Request)
	case GatewayPushUnsubscribe:
		payload, err := DecodeRPCPayload[GatewayRPCPushUnsubscribePayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		return ValidatePushEndpointText(payload.Request.Endpoint)
	case GatewayPushConfigResult:
		payload, err := DecodeRPCPayload[HTTPPushConfigResponse](envelope)
		if err != nil {
			return err
		}
		return validatePushKey(payload.PublicKey, PushPublicKeyText)
	case GatewayPreviewCheck:
		payload, err := DecodeRPCPayload[GatewayRPCPreviewCheckPayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Credential); err != nil {
			return err
		}
		if !ValidUUID(payload.SessionID) || !ValidPreviewPort(payload.Port) {
			return errors.New("preview check needs a canonical session UUID and a port 1..65535")
		}
		return nil
	case GatewayPreviewCheckResult:
		payload, err := DecodeRPCPayload[GatewayRPCPreviewTargetPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Host != "127.0.0.1" && payload.Host != "::1" {
			return errors.New("preview host must be a loopback address")
		}
		return nil
	case GatewayPushSubscribeResult, GatewayPushUnsubscribeResult:
		_, err := DecodeRPCPayload[BrokerRPCEmptyPayload](envelope)
		return err
	case GatewayPushTestResult:
		payload, err := DecodeRPCPayload[HTTPPushTestResponse](envelope)
		if err != nil {
			return err
		}
		if payload.Sent < 0 || payload.Sent > MaxPushTestSent {
			return errors.New("sent is out of range")
		}
		return nil
	case GatewayPairRedeemResult:
		payload, err := DecodeRPCPayload[HTTPPairRedeemResponse](envelope)
		if err != nil {
			return err
		}
		if !ValidUUID(payload.PendingID) {
			return errors.New("pendingId must be a canonical UUID")
		}
		if err := ValidateSecretText(payload.PollSecret); err != nil {
			return err
		}
		if err := ValidateVerificationCode(payload.VerificationCode); err != nil {
			return err
		}
		_, err = parseBrokerRPCTime(payload.ExpiresAt, "expiresAt")
		return err
	case GatewayPairPollResult:
		payload, err := DecodeRPCPayload[GatewayRPCPairPollResultPayload](envelope)
		if err != nil {
			return err
		}
		return validateGatewayPollResult(payload)
	case GatewayAuthCheckResult:
		payload, err := DecodeRPCPayload[GatewayRPCPrincipalPayload](envelope)
		if err != nil {
			return err
		}
		if !ValidUUID(payload.DeviceID) || !ValidUUID(payload.CredentialID) {
			return errors.New("principal contains a non-canonical UUID")
		}
		if err := ValidateDeviceLabel(payload.DeviceLabel); err != nil {
			return err
		}
		_, err = parseBrokerRPCTime(payload.ExpiresAt, "expiresAt")
		return err
	case GatewayAuthLogoutResult, GatewayLiveOpenResult, GatewayComputersRemoveResult, GatewayComputersOfferResult:
		_, err := DecodeRPCPayload[BrokerRPCEmptyPayload](envelope)
		return err
	case GatewaySettingsResult:
		payload, err := DecodeRPCPayload[HTTPSettingsResponse](envelope)
		if err != nil {
			return err
		}
		return validateSettings(payload)
	case GatewaySessionListResult:
		payload, err := DecodeRPCPayload[HTTPSessionListResponse](envelope)
		if err != nil {
			return err
		}
		if payload.Sessions == nil || len(payload.Sessions) > MaxHTTPSessionList {
			return errors.New("session list is outside bounds")
		}
		for index, record := range payload.Sessions {
			if err := validateBrokerRPCSessionRecord(record); err != nil {
				return fmt.Errorf("sessions[%d]: %w", index, err)
			}
		}
		return nil
	case GatewaySessionCreateResult, GatewaySessionRenameResult, GatewaySessionTerminateResult:
		payload, err := DecodeRPCPayload[HTTPSessionMutationResponse](envelope)
		if err != nil {
			return err
		}
		if !ValidUUID(payload.OperationID) {
			return errors.New("operationId must be a canonical UUID")
		}
		return validateBrokerRPCSessionRecord(payload.Session)
	case GatewayError:
		return validateRPCError[GatewayRPCMessageType](envelope)
	case GatewayProvidersGet, GatewayProvidersUpdate, GatewayProvidersOpen, GatewayProvidersCreate, GatewayProvidersManage,
		GatewayProvidersGetResult, GatewayProvidersUpdateResult, GatewayProvidersOpenResult, GatewayProvidersCreateResult,
		GatewayProvidersManageResult:
		return validateGatewayProviderPayload(envelope)
	case GatewayGithubGet, GatewayGithubAction, GatewayGithubRepos,
		GatewayGithubGetResult, GatewayGithubActionResult, GatewayGithubReposResult:
		return validateGatewayGitHubPayload(envelope)
	case GatewayChatUpdate, GatewayChatUpdateResult:
		return validateGatewayChatPayload(envelope)
	default:
		return validateGatewayUploadPayload(envelope)
	}
}

func validateGatewayPollResult(payload GatewayRPCPairPollResultPayload) error {
	switch payload.Response.Status {
	case HTTPPairPollStatusPending:
		if payload.Response.Device != nil || payload.Credential != nil || payload.CredentialExpiresAt != nil {
			return errors.New("pending poll must not carry a device or credential")
		}
		return nil
	case Approved:
		if payload.Response.Device == nil || payload.Credential == nil || payload.CredentialExpiresAt == nil {
			return errors.New("approved poll must carry exactly one device and credential")
		}
		if !ValidUUID(payload.Response.Device.ID) {
			return errors.New("device id must be a canonical UUID")
		}
		if err := ValidateDeviceLabel(payload.Response.Device.Label); err != nil {
			return err
		}
		if err := ValidateSecretText(*payload.Credential); err != nil {
			return err
		}
		_, err := parseBrokerRPCTime(*payload.CredentialExpiresAt, "credentialExpiresAt")
		return err
	default:
		return errors.New("unknown poll status")
	}
}

var hostBuildPattern = regexp.MustCompile(`^[0-9A-Za-z._+-]+$`)

// ValidHostBuild applies the settings schema's hostBuild rule: 1..128 ASCII
// letters, digits and . _ + - (a peer's value is shown as text).
func ValidHostBuild(build string) bool {
	return len(build) <= 128 && hostBuildPattern.MatchString(build)
}

func validateSettings(payload HTTPSettingsResponse) error {
	if !ValidUUID(payload.Device.ID) || !ValidUUID(payload.BrokerGeneration) {
		return errors.New("settings contain a non-canonical UUID")
	}
	if err := ValidateDeviceLabel(payload.Device.Label); err != nil {
		return err
	}
	if !ValidHostBuild(payload.HostBuild) {
		return errors.New("hostBuild is outside bounds")
	}
	if payload.MinProtocolRevision < 1 || payload.ProtocolRevision < payload.MinProtocolRevision || payload.ProtocolRevision > 1000000 {
		return errors.New("protocol revisions need 1 <= minProtocolRevision <= protocolRevision <= 1000000")
	}
	if payload.Projects == nil || payload.ShellProfiles == nil ||
		len(payload.Projects) > MaxSettingsProjects || len(payload.ShellProfiles) > MaxSettingsShellProfiles {
		return errors.New("settings lists are outside bounds")
	}
	for _, option := range append(append([]HTTPNamedOption(nil), payload.Projects...), payload.ShellProfiles...) {
		if !ValidUUID(option.ID) {
			return errors.New("settings option id must be a canonical UUID")
		}
		if err := validateBrokerRPCText(option.Name, MaxNamedOptionBytes, "option name"); err != nil {
			return err
		}
	}
	if len(payload.Providers) > MaxProviders {
		return errors.New("settings list too many providers")
	}
	for _, provider := range payload.Providers {
		if err := ValidateProviderInfo(provider); err != nil {
			return err
		}
	}
	if payload.Github != nil {
		return ValidateGitHubInfo(*payload.Github)
	}
	return nil
}

func validateAdminRPCPayload(envelope AdminRPCEnvelope) error { //nolint:gocyclo
	switch envelope.Type {
	case AdminPairGrant, AdminDeviceRevokeVia, AdminDeviceRevokeViaResult, AdminComputersListResult,
		AdminComputersRemove, AdminComputersRemoveResult, AdminPairOffer, AdminPairOfferResult, AdminPairOfferEnd:
		return validateAdminComputerPayload(envelope)
	case AdminComputersList, AdminPairPending, AdminDeviceList, AdminProjectList, AdminProfileList, AdminProfileDetect,
		AdminShutdown, AdminGatewayStop, AdminGatewayStopResult, AdminPairOfferEndResult:
		_, err := DecodeRPCPayload[BrokerRPCEmptyPayload](envelope)
		return err
	case AdminShutdownResult:
		payload, err := DecodeRPCPayload[AdminRPCShutdownResultPayload](envelope)
		if err != nil {
			return err
		}
		if payload.TerminatedSessions < 0 || payload.TerminatedSessions > MaxAdminShutdownSessions {
			return errors.New("protocol: admin.shutdown.result terminatedSessions is out of range")
		}
		return nil
	case AdminPairApprove, AdminPairApproveResult:
		payload, err := DecodeRPCPayload[AdminRPCPendingIDPayload](envelope)
		if err != nil {
			return err
		}
		if !ValidUUID(payload.PendingID) {
			return errors.New("pendingId must be a canonical UUID")
		}
		return nil
	case AdminDeviceRevoke:
		payload, err := DecodeRPCPayload[AdminRPCDeviceIDPayload](envelope)
		if err != nil {
			return err
		}
		if !ValidUUID(payload.DeviceID) {
			return errors.New("deviceId must be a canonical UUID")
		}
		return nil
	case AdminDeviceRename, AdminDeviceRenameResult:
		payload, err := DecodeRPCPayload[AdminRPCDeviceRenamePayload](envelope)
		if err != nil {
			return err
		}
		if !ValidUUID(payload.DeviceID) {
			return errors.New("deviceId must be a canonical UUID")
		}
		return ValidateDeviceLabel(payload.Label)
	case AdminProjectAdd:
		payload, err := DecodeRPCPayload[AdminRPCProjectAddPayload](envelope)
		if err != nil {
			return err
		}
		if err := validateAdminPath(payload.Path, false); err != nil {
			return err
		}
		if payload.Name != nil {
			return validateBrokerRPCText(*payload.Name, MaxNamedOptionBytes, "name")
		}
		return nil
	case AdminConfigOrigin, AdminConfigOriginResult:
		payload, err := DecodeRPCPayload[AdminRPCOriginPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Origin != nil {
			return ValidatePairingOrigin(*payload.Origin)
		}
		return nil
	case AdminPairGrantResult:
		payload, err := DecodeRPCPayload[AdminRPCPairGrantResultPayload](envelope)
		if err != nil {
			return err
		}
		if err := ValidateSecretText(payload.Grant); err != nil {
			return err
		}
		if payload.Origin != nil {
			if err := ValidatePairingOrigin(*payload.Origin); err != nil {
				return err
			}
		}
		if err := validateAppBinding(payload.AppOrigin, payload.AppNode); err != nil {
			return err
		}
		_, err = parseBrokerRPCTime(payload.ExpiresAt, "expiresAt")
		return err
	case AdminPairPendingResult:
		payload, err := DecodeRPCPayload[AdminRPCPairPendingResultPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Pending == nil || len(payload.Pending) > MaxAdminPendingPairings {
			return errors.New("pending list is outside bounds")
		}
		for _, pending := range payload.Pending {
			if !ValidUUID(pending.ID) {
				return errors.New("pending id must be a canonical UUID")
			}
			if err := ValidateVerificationCode(pending.VerificationCode); err != nil {
				return err
			}
			if err := ValidateDeviceLabel(pending.DeviceLabel); err != nil {
				return err
			}
			if _, err := parseBrokerRPCTime(pending.ExpiresAt, "expiresAt"); err != nil {
				return err
			}
			if err := validateAppBinding(pending.AppOrigin, pending.AppNode); err != nil {
				return err
			}
		}
		return nil
	case AdminDeviceListResult:
		payload, err := DecodeRPCPayload[AdminRPCDeviceListResultPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Devices == nil || len(payload.Devices) > MaxAdminDevices {
			return errors.New("device list is outside bounds")
		}
		for _, device := range payload.Devices {
			if !ValidUUID(device.ID) {
				return errors.New("device id must be a canonical UUID")
			}
			if err := ValidateDeviceLabel(device.Label); err != nil {
				return err
			}
			for _, value := range []string{device.CreatedAt, device.LastUsedAt} {
				if _, err := parseBrokerRPCTime(value, "device time"); err != nil {
					return err
				}
			}
			if err := validateDeviceStatus(device.Status, device.Revoked); err != nil {
				return err
			}
			if err := validateAppBinding(device.AppOrigin, device.AppNode); err != nil {
				return err
			}
		}
		return nil
	case AdminDeviceRevokeResult:
		payload, err := DecodeRPCPayload[AdminRPCDeviceRevokeResultPayload](envelope)
		if err != nil {
			return err
		}
		if !ValidUUID(payload.DeviceID) || payload.ClosedStreams < 0 || payload.ClosedStreams > 65536 {
			return errors.New("revocation result is outside bounds")
		}
		return nil
	case AdminProjectAddResult:
		payload, err := DecodeRPCPayload[AdminRPCProjectResultPayload](envelope)
		if err != nil {
			return err
		}
		return validateAdminProject(payload.Project)
	case AdminProjectListResult:
		payload, err := DecodeRPCPayload[AdminRPCProjectListResultPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Projects == nil || len(payload.Projects) > MaxAdminProjects {
			return errors.New("project list is outside bounds")
		}
		for _, project := range payload.Projects {
			if err := validateAdminProject(project); err != nil {
				return err
			}
		}
		return nil
	case AdminProfileListResult, AdminProfileDetectResult:
		payload, err := DecodeRPCPayload[AdminRPCProfileListResultPayload](envelope)
		if err != nil {
			return err
		}
		if payload.Profiles == nil || len(payload.Profiles) > MaxAdminProfiles {
			return errors.New("profile list is outside bounds")
		}
		for _, profile := range payload.Profiles {
			if !ValidUUID(profile.ID) {
				return errors.New("profile id must be a canonical UUID")
			}
			if err := validateBrokerRPCText(profile.Name, MaxNamedOptionBytes, "profile name"); err != nil {
				return err
			}
			if err := validateAdminPath(profile.Executable, true); err != nil {
				return err
			}
			if profile.Arguments == nil || len(profile.Arguments) > MaxAdminProfileArguments {
				return errors.New("profile arguments are outside bounds")
			}
			for _, argument := range profile.Arguments {
				if len(argument) > MaxAdminArgumentBytes || !utf8.ValidString(argument) || strings.IndexByte(argument, 0) >= 0 {
					return errors.New("profile argument is outside bounds")
				}
			}
		}
		return nil
	case AdminGithubStatus, AdminGithubDisconnect, AdminGithubCredential,
		AdminGithubStatusResult, AdminGithubDisconnectResult, AdminGithubCredentialResult:
		return validateAdminGitHubPayload(envelope)
	case AdminChatStatus, AdminChatSet, AdminChatStatusResult, AdminChatSetResult:
		return validateAdminChatPayload(envelope)
	case AdminError:
		return validateRPCError[AdminRPCMessageType](envelope)
	default:
		return errors.New("unknown admin RPC message type")
	}
}

func validateAdminProject(project AdminRPCProject) error {
	if !ValidUUID(project.ID) {
		return errors.New("project id must be a canonical UUID")
	}
	if err := validateBrokerRPCText(project.Name, MaxNamedOptionBytes, "project name"); err != nil {
		return err
	}
	return validateAdminPath(project.Path, true)
}

func validateAdminPath(value string, absolute bool) error {
	if value == "" || len(value) > MaxAdminPathBytes || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return errors.New("path is outside bounds")
	}
	if absolute && !filepath.IsAbs(value) && !strings.HasPrefix(value, "/") {
		return errors.New("path must be absolute")
	}
	return nil
}

// validateDeviceStatus checks status against its enum and against the
// compatibility revoked flag, which must agree: revoked is kept only for
// callers that have not moved to status.
func validateDeviceStatus(status AdminRPCDeviceStatus, revoked bool) error {
	switch status {
	case Active, Expired, AdminRPCDeviceStatusRevoked:
	default:
		return errors.New("device status is not a recognized value")
	}
	if (status == AdminRPCDeviceStatusRevoked) != revoked {
		return errors.New("device status disagrees with the revoked flag")
	}
	return nil
}

func validateRPCError[T ~string](envelope RPCEnvelope[T]) error {
	payload, err := DecodeRPCPayload[BrokerRPCErrorPayload](envelope)
	if err != nil {
		return err
	}
	if !validErrorCode(payload.Code) {
		return errors.New("unknown RPC error code")
	}
	return validateBrokerRPCText(payload.Message, MaxBrokerRPCErrorBytes, "error message")
}

// ValidateSecretText checks the canonical transport shape of a 256-bit secret
// without decoding it. The broker still parses and hashes every secret.
func ValidateSecretText(value string) error {
	if len(value) != SecretTextBytes {
		return errors.New("secret has an invalid shape")
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return errors.New("secret has an invalid shape")
		}
	}
	return nil
}

const (
	PushPublicKeyText    = 87 // 65 bytes, unpadded base64url
	PushAuthText         = 22 // 16 bytes, unpadded base64url
	MaxPushEndpointBytes = 2048
	MaxPushTestSent      = 5
)

// ValidatePushSubscription checks the schema shape of a browser push
// subscription. The broker additionally applies the push-host allow-list
// and decodes the keys.
func ValidatePushSubscription(request HTTPPushSubscribeRequest) error {
	if err := ValidatePushEndpointText(request.Endpoint); err != nil {
		return err
	}
	if err := validatePushKey(request.Keys.P256Dh, PushPublicKeyText); err != nil {
		return err
	}
	return validatePushKey(request.Keys.Auth, PushAuthText)
}

// ValidatePushEndpointText mirrors the pushEndpoint schema: an https URL of
// printable ASCII, 12..2048 bytes.
func ValidatePushEndpointText(value string) error {
	if len(value) < 12 || len(value) > MaxPushEndpointBytes || !strings.HasPrefix(value, "https://") {
		return errors.New("push endpoint is outside bounds")
	}
	for index := 0; index < len(value); index++ {
		if value[index] <= ' ' || value[index] > '~' {
			return errors.New("push endpoint must be printable ASCII")
		}
	}
	return nil
}

func validatePushKey(value string, size int) error {
	if len(value) != size || size == PushPublicKeyText && value[0] != 'B' {
		return errors.New("push key has an invalid shape")
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && character != '_' && character != '-' {
			return errors.New("push key has an invalid shape")
		}
	}
	return nil
}

// ValidateDeviceLabel bounds untrusted display text. A label is never identity.
func ValidateDeviceLabel(value string) error {
	return validateBrokerRPCText(value, MaxDeviceLabelBytes, "device label")
}

// ValidateVerificationCode accepts only the "NNN-NNN" comparison format.
func ValidateVerificationCode(value string) error {
	if len(value) != 7 || value[3] != '-' {
		return errors.New("verification code must be NNN-NNN")
	}
	for index := 0; index < len(value); index++ {
		if index != 3 && (value[index] < '0' || value[index] > '9') {
			return errors.New("verification code must be NNN-NNN")
		}
	}
	return nil
}

// ValidatePairingOrigin accepts an exact https origin, or loopback http with
// an explicit port for the development profile. Paths, queries, fragments,
// credentials and uppercase spellings are rejected so a printed pairing link
// has exactly one representation.
func ValidatePairingOrigin(value string) error {
	if len(value) < 8 || len(value) > MaxPairingOriginBytes {
		return errors.New("origin is outside bounds")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Opaque != "" || parsed.Path != "" || parsed.RawPath != "" || parsed.ForceQuery ||
		parsed.Host == "" || strings.ToLower(value) != value || parsed.String() != value {
		return errors.New("origin must be scheme://host[:port] with no path, query or fragment")
	}
	host, port := parsed.Hostname(), parsed.Port()
	if port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
			return errors.New("origin port is invalid")
		}
	}
	switch parsed.Scheme {
	case "https":
		if strings.ContainsAny(host, " %") {
			return errors.New("origin host is invalid")
		}
		return nil
	case "http":
		if (host == "127.0.0.1" || host == "localhost") && port != "" {
			return nil
		}
		return errors.New("http origins are allowed only for 127.0.0.1 or localhost with a port")
	default:
		return errors.New("origin must use https")
	}
}

// ValidateDisplayText applies the schema's safeDisplayText rule: non-empty,
// trimmed, valid UTF-8 without control characters, at most maxBytes bytes.
func ValidateDisplayText(value string, maxBytes int) error {
	return validateBrokerRPCText(value, maxBytes, "display text")
}
