package protocol

import (
	"errors"
	"regexp"
	"strings"
)

// GitHub (revision 7, docs/github-integration.md). These mirror the
// HTTPGitHub* rules of http-api.schema.json and admin-rpc.schema.json.
const MaxGitHubRepos = 1000

var (
	gitHubRepoPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,39}/[A-Za-z0-9_][A-Za-z0-9_.-]{0,99}$`)
	gitHubLoginPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	gitHubAppPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,33}$`)
	gitHubUserCodePattern = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)
	gitHubTokenPattern    = regexp.MustCompile(`^[A-Za-z0-9_]{20,255}$`)
)

// ValidGitHubRepo reports whether value is owner/name as the schema allows
// it and does not end in .git (the host refuses such a name).
func ValidGitHubRepo(value string) bool {
	return gitHubRepoPattern.MatchString(value) && !strings.HasSuffix(strings.ToLower(value), ".git")
}

// ValidGitHubLogin reports whether value is a GitHub account name.
func ValidGitHubLogin(value string) bool { return gitHubLoginPattern.MatchString(value) }

func validGitHubState(state HTTPGitHubState) bool {
	switch state {
	case NoGit, HTTPGitHubStateNotConnected, Connecting, NeedsInstall, HTTPGitHubStateReady:
		return true
	}
	return false
}

func validateGitHubLogin(login *string) error {
	if login != nil && !ValidGitHubLogin(*login) {
		return errors.New("github login is invalid")
	}
	return nil
}

// ValidateGitHubRequest checks POST /api/v1/github: repo with setup only,
// and setup needs it.
func ValidateGitHubRequest(request HTTPGitHubRequest) error {
	switch request.Action {
	case Connect, Cancel, Disconnect, Stop:
		if request.Repo != nil {
			return errors.New("github repo belongs to setup only")
		}
		return nil
	case Setup:
		if request.Repo == nil || !ValidGitHubRepo(*request.Repo) {
			return errors.New("github setup needs a valid repo")
		}
		return nil
	default:
		return errors.New("github action is invalid")
	}
}

// ValidateGitHubInfo checks HTTPSettingsResponse.github.
func ValidateGitHubInfo(info HTTPGitHubInfo) error {
	if !validGitHubState(info.State) {
		return errors.New("github state is invalid")
	}
	return validateGitHubLogin(info.Login)
}

// ValidateGitHubResponse checks GET /api/v1/github and the POST answer.
func ValidateGitHubResponse(response HTTPGitHubResponse) error {
	if !validGitHubState(response.State) || !gitHubAppPattern.MatchString(response.App) {
		return errors.New("github state or app is invalid")
	}
	if err := validateGitHubLogin(response.Login); err != nil {
		return err
	}
	if connect := response.Connect; connect != nil && (!gitHubUserCodePattern.MatchString(connect.UserCode) || connect.ExpiresAt < 0) {
		return errors.New("github connect is invalid")
	}
	if response.Problem != nil {
		switch *response.Problem {
		case CodeExpired, Denied, OtherProgram, Ended, HTTPGitHubProblemGitHubDown:
		default:
			return errors.New("github problem is invalid")
		}
	}
	if response.Setup != nil {
		return validateGitHubSetup(*response.Setup)
	}
	return nil
}

func validateGitHubSetup(setup HTTPGitHubSetup) error {
	if !gitHubRepoPattern.MatchString(setup.Repo) {
		return errors.New("github setup repo is invalid")
	}
	switch setup.Phase {
	case Cloning, Finished, HTTPGitHubSetupPhaseFailed:
	default:
		return errors.New("github setup phase is invalid")
	}
	if setup.Percent != nil && (*setup.Percent < 0 || *setup.Percent > 100) ||
		setup.Bytes != nil && *setup.Bytes < 0 || setup.StartedAt != nil && *setup.StartedAt < 0 ||
		setup.ProjectID != nil && !ValidUUID(*setup.ProjectID) {
		return errors.New("github setup fields are outside bounds")
	}
	if setup.Problem != nil {
		switch *setup.Problem {
		case NameTaken, BadName, NoSpace, TooManyProjects, NotFound, CloneFailed, Stopped, Timeout,
			HTTPGitHubSetupProblemGitHubDown, HTTPGitHubSetupProblemNotConnected:
		default:
			return errors.New("github setup problem is invalid")
		}
	}
	return nil
}

// ValidateGitHubRepos checks GET /api/v1/github/repos.
func ValidateGitHubRepos(response HTTPGitHubReposResponse) error {
	if response.Repos == nil || len(response.Repos) > MaxGitHubRepos {
		return errors.New("github repos are outside bounds")
	}
	for _, repo := range response.Repos {
		if !gitHubRepoPattern.MatchString(repo.Repo) || repo.SizeKB < 0 || repo.PushedAt != nil && *repo.PushedAt < 0 {
			return errors.New("github repo is invalid")
		}
	}
	return nil
}

// ValidateGitHubCredential checks admin.github.credential.result.
func ValidateGitHubCredential(payload AdminRPCGitHubCredentialPayload) error {
	if !ValidGitHubLogin(payload.Username) || !gitHubTokenPattern.MatchString(payload.Password) || payload.ExpiresAt < 0 {
		return errors.New("github credential is invalid")
	}
	return nil
}

func validateGatewayGitHubPayload(envelope GatewayRPCEnvelope) error {
	switch envelope.Type {
	case GatewayGithubGet, GatewayGithubRepos:
		payload, err := DecodeRPCPayload[GatewayRPCCredentialPayload](envelope)
		if err != nil {
			return err
		}
		return ValidateSecretText(payload.Credential)
	case GatewayGithubAction:
		payload, err := DecodeRPCPayload[GatewayRPCGitHubActionPayload](envelope)
		if err != nil {
			return err
		}
		return errors.Join(ValidateSecretText(payload.Credential), ValidateGitHubRequest(payload.Request))
	case GatewayGithubGetResult, GatewayGithubActionResult:
		payload, err := DecodeRPCPayload[HTTPGitHubResponse](envelope)
		if err != nil {
			return err
		}
		return ValidateGitHubResponse(payload)
	case GatewayGithubReposResult:
		payload, err := DecodeRPCPayload[HTTPGitHubReposResponse](envelope)
		if err != nil {
			return err
		}
		return ValidateGitHubRepos(payload)
	default:
		return errors.New("unknown github message type")
	}
}

func validateAdminGitHubPayload(envelope AdminRPCEnvelope) error {
	switch envelope.Type {
	case AdminGithubStatus, AdminGithubDisconnect, AdminGithubCredential:
		_, err := DecodeRPCPayload[BrokerRPCEmptyPayload](envelope)
		return err
	case AdminGithubStatusResult, AdminGithubDisconnectResult:
		payload, err := DecodeRPCPayload[AdminRPCGitHubStatusPayload](envelope)
		if err != nil {
			return err
		}
		if payload.State != nil && !validGitHubState(*payload.State) || payload.State == nil && payload.Login != nil {
			return errors.New("github status is invalid")
		}
		return validateGitHubLogin(payload.Login)
	case AdminGithubCredentialResult:
		payload, err := DecodeRPCPayload[AdminRPCGitHubCredentialPayload](envelope)
		if err != nil {
			return err
		}
		return ValidateGitHubCredential(payload)
	default:
		return errors.New("unknown github message type")
	}
}
