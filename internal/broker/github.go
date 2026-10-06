package broker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/github"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// GitHub (docs/github-integration.md): the broker wires one *github.Service,
// authorizes and limits the gateway.github.* routes per device, and answers
// the admin.github.* messages of pterm. Every error leaves with a fixed
// Termivoa text, never GitHub's or git's.

// Requests per device per minute (design, section 7).
var githubLimits = map[string]int{
	"get": 60, "repos": 10,
	string(protocol.Connect): 3, string(protocol.Cancel): 10, string(protocol.Disconnect): 10,
	string(protocol.Setup): 3, string(protocol.Stop): 10,
}

// githubService is the part of *github.Service the broker uses (a test seam).
type githubService interface {
	Info() protocol.HTTPGitHubInfo
	State(device string) protocol.HTTPGitHubResponse
	Connect(ctx context.Context, device string) (protocol.HTTPGitHubResponse, error)
	Cancel(device string) protocol.HTTPGitHubResponse
	Disconnect(ctx context.Context, device string) (protocol.HTTPGitHubResponse, bool, error)
	Repos(ctx context.Context) (protocol.HTTPGitHubReposResponse, error)
	Setup(ctx context.Context, device, repo string) (protocol.HTTPGitHubResponse, error)
	Stop(device string) protocol.HTTPGitHubResponse
	Credential(ctx context.Context) (login, token string, expires time.Time, err error)
	Close()
}

type githubFailure struct{ managementFailure }

func (failure githubFailure) Error() string { return "broker: github: " + failure.message }

var (
	errGitHubOff          = githubFailure{managementFailure{protocol.InvalidRequest, "GitHub is not available on this computer"}}
	errGitHubRate         = githubFailure{managementFailure{protocol.LimitExceeded, "too many requests; wait a minute and try again"}}
	errGitHubNotConnected = githubFailure{managementFailure{protocol.HostUnavailable, "GitHub is not connected on this computer"}}
	errGitHubDown         = githubFailure{managementFailure{protocol.HostUnavailable, "this computer cannot reach GitHub"}}
	errGitHubBusy         = githubFailure{managementFailure{protocol.OperationConflict, "GitHub is busy with another phone's request; try again"}}
	errGitHubNoGit        = githubFailure{managementFailure{protocol.HostUnavailable, "git is not installed on this computer"}}
	errGitHubProjects     = githubFailure{managementFailure{protocol.LimitExceeded, "saved project limit reached"}}
)

func classifyGitHubError(err error) (managementFailure, bool) {
	var failure githubFailure
	if errors.As(err, &failure) {
		return failure.managementFailure, true
	}
	return managementFailure{}, false
}

// githubError maps the service's errors to their fixed public failures.
// Any other error keeps the generic internal text.
func githubError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, github.ErrNotConnected):
		return errGitHubNotConnected
	case errors.Is(err, github.ErrGitHubDown):
		return errGitHubDown
	case errors.Is(err, github.ErrBusy):
		return errGitHubBusy
	case errors.Is(err, github.ErrNoGit):
		return errGitHubNoGit
	case errors.Is(err, github.ErrTooManyProjects):
		return errGitHubProjects
	}
	return err
}

// newGitHub builds the service. A service that cannot start (an unreadable
// token file, say) turns the feature off instead of stopping the broker.
func (server *ManagementServer) newGitHub(config github.Config) {
	service, err := github.New(config, github.Deps{
		Now: server.config.Now,
		Projects: func(ctx context.Context) ([]github.Project, error) {
			projects, err := server.config.Store.ListProjects(ctx, protocol.MaxAdminProjects)
			if err != nil {
				return nil, err
			}
			result := make([]github.Project, 0, len(projects))
			for _, project := range projects {
				result = append(result, github.Project{ID: project.ID, Path: project.CanonicalPath})
			}
			return result, nil
		},
		AddProject: func(ctx context.Context, path string) (string, error) {
			project, err := server.addProject(ctx, protocol.AdminRPCProjectAddPayload{Path: path})
			if errors.Is(err, errProjectLimit) {
				return "", github.ErrTooManyProjects
			}
			return project.ID, err
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "broker: GitHub is off: it could not start")
		return
	}
	if service != nil {
		server.github = service
	}
}

// githubInfo is the settings field; nil when the feature is off.
func (server *ManagementServer) githubInfo() *protocol.HTTPGitHubInfo {
	if server.github == nil {
		return nil
	}
	info := server.github.Info()
	return &info
}

func (server *ManagementServer) closeGitHub() {
	if server.github != nil {
		server.github.Close()
	}
}

// dispatchGitHub answers the gateway.github.* requests.
func (server *ManagementServer) dispatchGitHub(
	ctx context.Context, request protocol.GatewayRPCEnvelope, authorize func(string) (auth.Principal, error),
) (any, error) {
	credential, action, repo := "", "get", ""
	switch request.Type {
	case protocol.GatewayGithubGet, protocol.GatewayGithubRepos:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCCredentialPayload](request)
		if err != nil {
			return nil, err
		}
		credential = payload.Credential
		if request.Type == protocol.GatewayGithubRepos {
			action = "repos"
		}
	case protocol.GatewayGithubAction:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCGitHubActionPayload](request)
		if err != nil {
			return nil, err
		}
		credential, action = payload.Credential, string(payload.Request.Action)
		if payload.Request.Repo != nil {
			repo = *payload.Request.Repo
		}
	default:
		return nil, errors.New("broker: unsupported github request")
	}
	principal, err := authorize(credential)
	if err != nil {
		return nil, err
	}
	service := server.github
	if service == nil {
		return nil, errGitHubOff
	}
	limit, ok := githubLimits[action]
	if !ok {
		return nil, errors.New("broker: unsupported github action")
	}
	if !server.githubLimits.take(action, principal.DeviceID, limit, server.now()) {
		return nil, errGitHubRate
	}
	device := principal.DeviceID
	switch protocol.HTTPGitHubAction(action) {
	case protocol.Connect:
		result, err := service.Connect(ctx, device)
		return result, githubError(err)
	case protocol.Cancel:
		return service.Cancel(device), nil
	case protocol.Disconnect:
		// revokeFailed: the phone then links to GitHub's settings page.
		result, revoked, err := service.Disconnect(ctx, device)
		if err == nil && !revoked {
			result.RevokeFailed = &[]bool{true}[0]
		}
		return result, githubError(err)
	case protocol.Setup:
		result, err := service.Setup(ctx, device, repo)
		return result, githubError(err)
	case protocol.Stop:
		return service.Stop(device), nil
	}
	if action == "repos" {
		result, err := service.Repos(ctx)
		return result, githubError(err)
	}
	return service.State(device), nil
}

// dispatchAdminGitHub answers the admin.github.* messages of pterm. The
// credential goes only to `pterm github-credential`; it is never logged.
func (server *ManagementServer) dispatchAdminGitHub(ctx context.Context, request protocol.AdminRPCEnvelope) (any, error) {
	service := server.github
	switch request.Type {
	case protocol.AdminGithubStatus, protocol.AdminGithubDisconnect:
		if service == nil {
			return protocol.AdminRPCGitHubStatusPayload{}, nil
		}
		if request.Type == protocol.AdminGithubDisconnect {
			if _, _, err := service.Disconnect(ctx, ""); err != nil {
				return nil, githubError(err)
			}
		}
		info := service.Info()
		return protocol.AdminRPCGitHubStatusPayload{State: &info.State, Login: info.Login}, nil
	case protocol.AdminGithubCredential:
		if service == nil {
			return nil, errGitHubOff
		}
		login, token, expires, err := service.Credential(ctx)
		if err != nil {
			return nil, githubError(err)
		}
		// A token without expiry (an app with expiry off) has a zero time: 0 means "no expiry".
		var expiresAt int64
		if !expires.IsZero() {
			expiresAt = expires.Unix()
		}
		return protocol.AdminRPCGitHubCredentialPayload{Username: login, Password: token, ExpiresAt: expiresAt}, nil
	default:
		return nil, errors.New("broker: unsupported github administration request")
	}
}
