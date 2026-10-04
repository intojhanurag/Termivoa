package protocol

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Terminal workspace providers (docs/herdr-provider.md, section 4). These
// mirror the HTTPProvider* rules of http-api.schema.json.
const (
	MaxProviders          = 4
	MaxProviderGroups     = 64
	MaxProviderItems      = 64
	MaxProviderTargets    = 32
	MaxProviderAgentKinds = 16
	maxProviderNameRunes  = 80
	maxProviderNameBytes  = 240
	maxProviderInfoRunes  = 32
	maxProviderInfoBytes  = 96
	maxProviderPromptRune = 4000
	maxProviderPromptByte = 16000
	maxProviderBranchRune = 64
)

var (
	providerIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	providerHandlePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]{0,63}$`)
	providerWordPattern   = regexp.MustCompile(`^[a-z][a-zA-Z]{0,23}$`)
	providerKindPattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	providerVersion       = regexp.MustCompile(`^[0-9A-Za-z._+-]{1,32}$`)
	providerBranchPattern = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$`)
)

// ValidProviderID reports whether value is a provider path segment.
func ValidProviderID(value string) bool { return providerIDPattern.MatchString(value) }

// ValidProviderHandle reports whether value is a target, item or group ID.
func ValidProviderHandle(value string) bool { return providerHandlePattern.MatchString(value) }

func validateProviderName(value string, maxRunes, maxBytes int) error {
	if err := validateBrokerRPCText(value, maxBytes, "provider name"); err != nil {
		return err
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return errors.New("provider name is too long")
	}
	return nil
}

func validateProviderNotify(notify []HTTPProviderNotifyState) error {
	if len(notify) > 2 {
		return errors.New("notify has more than 2 states")
	}
	for index, state := range notify {
		if state != Blocked && state != Done || slices.Contains(notify[:index], state) {
			return errors.New("notify holds an unknown or repeated state")
		}
	}
	return nil
}

func validProviderState(state HTTPProviderState) bool {
	return state == HTTPProviderStateReady || state == NotRunning || state == Unsupported || state == Off || state == NotInstalled
}

// ValidateProviderInfo checks one entry of HTTPSettingsResponse.providers.
func ValidateProviderInfo(info HTTPProviderInfo) error {
	if !ValidProviderID(info.ID) || !validProviderState(info.State) {
		return errors.New("provider id or state is invalid")
	}
	if err := validateProviderName(info.Name, maxProviderInfoRunes, maxProviderInfoBytes); err != nil {
		return err
	}
	if info.Version != nil && !providerVersion.MatchString(*info.Version) {
		return errors.New("provider version is invalid")
	}
	if info.CanInstall != nil && *info.CanInstall && info.State != NotInstalled ||
		info.CanStart != nil && *info.CanStart && info.State != NotRunning {
		return errors.New("provider canInstall or canStart does not fit its state")
	}
	if info.Notify == nil || info.AgentKinds == nil {
		return errors.New("provider notify and agentKinds are required")
	}
	if err := validateProviderNotify(info.Notify); err != nil {
		return err
	}
	if len(info.AgentKinds) > MaxProviderAgentKinds {
		return errors.New("provider has too many agent kinds")
	}
	for _, kind := range info.AgentKinds {
		if !providerKindPattern.MatchString(kind) {
			return errors.New("provider agent kind is invalid")
		}
	}
	return nil
}

// ValidateProviderSnapshot checks GET /api/v1/providers/{provider}.
func ValidateProviderSnapshot(response HTTPProviderSnapshotResponse) error {
	if !ValidProviderID(response.Provider) || !validProviderState(response.State) {
		return errors.New("snapshot provider or state is invalid")
	}
	if response.Groups == nil || len(response.Groups) > MaxProviderGroups ||
		response.State != HTTPProviderStateReady && len(response.Groups) > 0 {
		return errors.New("snapshot groups are outside bounds")
	}
	for _, group := range response.Groups {
		if !ValidProviderHandle(group.ID) || !providerWordPattern.MatchString(group.State) ||
			group.Items == nil || len(group.Items) > MaxProviderItems {
			return fmt.Errorf("group %q is invalid", group.ID)
		}
		if err := validateProviderName(group.Name, maxProviderNameRunes, maxProviderNameBytes); err != nil {
			return err
		}
		for _, item := range group.Items {
			if err := validateProviderItem(item); err != nil {
				return fmt.Errorf("item %q: %w", item.ID, err)
			}
		}
	}
	return nil
}

func validateProviderItem(item HTTPProviderItem) error {
	if !ValidProviderHandle(item.ID) || !providerWordPattern.MatchString(item.State) ||
		item.Targets == nil || len(item.Targets) > MaxProviderTargets {
		return errors.New("item fields are invalid")
	}
	if err := validateProviderName(item.Name, maxProviderNameRunes, maxProviderNameBytes); err != nil {
		return err
	}
	if item.StateSince != nil {
		if _, err := parseBrokerRPCTime(*item.StateSince, "stateSince"); err != nil {
			return err
		}
	}
	if item.OpenTarget != nil && !ValidProviderHandle(*item.OpenTarget) {
		return errors.New("openTarget is invalid")
	}
	for _, target := range item.Targets {
		if !ValidProviderHandle(target.ID) || !providerWordPattern.MatchString(target.State) ||
			target.Agent != nil && !providerKindPattern.MatchString(*target.Agent) {
			return errors.New("target fields are invalid")
		}
		if err := validateProviderName(target.Label, maxProviderNameRunes, maxProviderNameBytes); err != nil {
			return err
		}
	}
	return nil
}

// ValidateProviderOpenRequest checks POST /api/v1/providers/{provider}/open.
func ValidateProviderOpenRequest(request HTTPProviderOpenRequest) error {
	if !ValidProviderHandle(request.TargetID) || request.Columns < 20 || request.Columns > 400 ||
		request.Rows < 5 || request.Rows > 200 {
		return errors.New("open request is invalid")
	}
	return nil
}

// ValidateProviderOpenResponse checks the open result and its session.
func ValidateProviderOpenResponse(response HTTPProviderOpenResponse) error {
	session := response.Session
	if !ValidUUID(session.ID) || !ValidUUID(session.Generation) || !ValidProviderID(session.Provider) ||
		!ValidProviderHandle(session.TargetID) || session.Columns < 20 || session.Columns > 400 ||
		session.Rows < 5 || session.Rows > 200 {
		return errors.New("provider session is invalid")
	}
	switch session.State {
	case Starting, Running, Exiting, Exited, Lost:
	default:
		return errors.New("provider session state is invalid")
	}
	return validateProviderName(session.Title, maxProviderNameRunes, maxProviderNameBytes)
}

// ValidateProviderUpdate checks PATCH /api/v1/providers/{provider}.
func ValidateProviderUpdate(request HTTPProviderUpdateRequest) error {
	if request.Enabled == nil && request.Notify == nil {
		return errors.New("update changes nothing")
	}
	return validateProviderNotify(request.Notify)
}

// ValidateProviderPrompt refuses ESC, C0 controls other than tab and line
// feed, DEL and C1, and invalid UTF-8.
func ValidateProviderPrompt(prompt string) error {
	if prompt == "" || !utf8.ValidString(prompt) || len(prompt) > maxProviderPromptByte ||
		utf8.RuneCountInString(prompt) > maxProviderPromptRune {
		return errors.New("prompt is outside bounds")
	}
	for _, character := range prompt {
		if character < 0x20 && character != '\t' && character != '\n' || character >= 0x7f && character <= 0x9f {
			return errors.New("prompt contains a control character")
		}
	}
	return nil
}

// ValidateProviderBranch checks a new worktree name: the schema pattern, at
// most 4 parts, and no part ending in .lock (git refuses those).
func ValidateProviderBranch(name string) error {
	if len(name) > maxProviderBranchRune || !providerBranchPattern.MatchString(name) {
		return errors.New("worktree name is invalid")
	}
	parts := strings.Split(name, "/")
	if len(parts) > 4 {
		return errors.New("worktree name has more than 4 parts")
	}
	for _, part := range parts {
		if strings.HasSuffix(part, ".lock") {
			return errors.New("worktree name ends in .lock")
		}
	}
	return nil
}

// ValidateProviderCreateRequest checks POST /api/v1/providers/{provider}/create.
func ValidateProviderCreateRequest(request HTTPProviderCreateRequest) error {
	if !ValidProviderHandle(request.GroupID) {
		return errors.New("groupId is invalid")
	}
	if err := ValidateProviderBranch(request.Name); err != nil {
		return err
	}
	if request.Agent != nil && !providerKindPattern.MatchString(*request.Agent) {
		return errors.New("agent kind is invalid")
	}
	if request.Prompt != nil {
		return ValidateProviderPrompt(*request.Prompt)
	}
	return nil
}

// ValidateProviderCreateResponse checks the create result.
func ValidateProviderCreateResponse(response HTTPProviderCreateResponse) error {
	if !ValidProviderHandle(response.ItemID) || !ValidProviderHandle(response.TargetID) {
		return errors.New("create result handles are invalid")
	}
	if response.Problem != nil {
		switch *response.Problem {
		case AgentNotReady, AgentFailed, PromptFailed:
		default:
			return errors.New("create problem is invalid")
		}
	}
	return nil
}

// ValidateProviderManageRequest checks POST /api/v1/providers/{provider}/manage:
// each action takes exactly its own fields (docs/herdr-manage.md, section 3.2).
func ValidateProviderManageRequest(request HTTPProviderManageRequest) error {
	item, name, project := false, false, false
	switch request.Action {
	case CloseItem, RemoveItem:
		item, name = true, true
	case NewTab:
		item = true
	case NewWorkspace:
		project = true
	case StartServer:
	default:
		return errors.New("manage action is invalid")
	}
	if (request.ItemID != nil) != item || (request.Name != nil) != name || (request.ProjectID != nil) != project {
		return errors.New("manage request has a missing field or one that does not belong to the action")
	}
	if item && !ValidProviderHandle(*request.ItemID) || project && !ValidUUID(*request.ProjectID) {
		return errors.New("manage itemId or projectId is invalid")
	}
	if name {
		return validateProviderName(*request.Name, maxProviderNameRunes, maxProviderNameBytes)
	}
	return nil
}

// ValidateProviderManageResponse checks the manage result.
func ValidateProviderManageResponse(response HTTPProviderManageResponse) error {
	if response.ItemID != nil && !ValidProviderHandle(*response.ItemID) ||
		response.TargetID != nil && !ValidProviderHandle(*response.TargetID) {
		return errors.New("manage result handles are invalid")
	}
	if response.Problem != nil && *response.Problem != Uncommitted {
		return errors.New("manage problem is invalid")
	}
	return nil
}

func validateGatewayProviderPayload(envelope GatewayRPCEnvelope) error {
	check := func(credential, provider string) error {
		if err := ValidateSecretText(credential); err != nil {
			return err
		}
		if !ValidProviderID(provider) {
			return errors.New("provider is invalid")
		}
		return nil
	}
	switch envelope.Type {
	case GatewayProvidersGet:
		payload, err := DecodeRPCPayload[GatewayRPCProviderPayload](envelope)
		if err != nil {
			return err
		}
		return check(payload.Credential, payload.Provider)
	case GatewayProvidersUpdate:
		payload, err := DecodeRPCPayload[GatewayRPCProviderUpdatePayload](envelope)
		if err != nil {
			return err
		}
		return errors.Join(check(payload.Credential, payload.Provider), ValidateProviderUpdate(payload.Request))
	case GatewayProvidersOpen:
		payload, err := DecodeRPCPayload[GatewayRPCProviderOpenPayload](envelope)
		if err != nil {
			return err
		}
		return errors.Join(check(payload.Credential, payload.Provider), ValidateProviderOpenRequest(payload.Request))
	case GatewayProvidersCreate:
		payload, err := DecodeRPCPayload[GatewayRPCProviderCreatePayload](envelope)
		if err != nil {
			return err
		}
		return errors.Join(check(payload.Credential, payload.Provider), ValidateProviderCreateRequest(payload.Request))
	case GatewayProvidersManage:
		payload, err := DecodeRPCPayload[GatewayRPCProviderManagePayload](envelope)
		if err != nil {
			return err
		}
		return errors.Join(check(payload.Credential, payload.Provider), ValidateProviderManageRequest(payload.Request))
	case GatewayProvidersManageResult:
		payload, err := DecodeRPCPayload[HTTPProviderManageResponse](envelope)
		if err != nil {
			return err
		}
		return ValidateProviderManageResponse(payload)
	case GatewayProvidersGetResult:
		payload, err := DecodeRPCPayload[HTTPProviderSnapshotResponse](envelope)
		if err != nil {
			return err
		}
		return ValidateProviderSnapshot(payload)
	case GatewayProvidersUpdateResult:
		payload, err := DecodeRPCPayload[HTTPProviderInfo](envelope)
		if err != nil {
			return err
		}
		return ValidateProviderInfo(payload)
	case GatewayProvidersOpenResult:
		payload, err := DecodeRPCPayload[HTTPProviderOpenResponse](envelope)
		if err != nil {
			return err
		}
		return ValidateProviderOpenResponse(payload)
	default:
		payload, err := DecodeRPCPayload[HTTPProviderCreateResponse](envelope)
		if err != nil {
			return err
		}
		return ValidateProviderCreateResponse(payload)
	}
}
