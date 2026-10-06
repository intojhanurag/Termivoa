// Package github connects one computer to GitHub and sets a repository up
// on it (docs/github-integration.md, ADR 0010): the device flow of the
// Termivoa GitHub App, one token pair kept in a 0600 file, the list of
// repositories the app can see, and a clone into ~/<name> that becomes a
// Termivoa project.
//
// The token never leaves this package except through Credential, which the
// broker gives only to `pterm github-credential` on the local admin channel.
package github

import (
	"context"
	"errors"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// ClientID and AppSlug are the public identity of the Termivoa GitHub App.
// Empty means this build has no app: the broker hides every GitHub route and
// the settings field. The owner registers the app (design, section 5); its
// two public values are then set here or with -ldflags -X.
var (
	ClientID = ""
	AppSlug  = ""
)

// Config is built by cmd/pterm.
type Config struct {
	// ClientID and AppSlug default to the package variables.
	ClientID, AppSlug string
	// Home is the user's home folder; New resolves its symlinks.
	Home string
	// DataDir is the broker's private data folder: the token file is
	// DataDir/github.json, mode 0600.
	DataDir string
	// GitPaths are git binaries to try first, then PathEnv. Each must pass
	// tailnet.CheckBinary.
	GitPaths []string
	PathEnv  string
	// Pterm is the absolute path of this pterm executable: git runs
	// "!<Pterm> github-credential" as its credential helper.
	Pterm string
	// Profile is "production" or "development": it is part of the temp
	// folder name, so one profile's start-up sweep never removes the other
	// profile's clone.
	Profile string
}

// Project is a saved Termivoa project.
type Project struct{ ID, Path string }

// Deps are the broker's parts the service uses.
type Deps struct {
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// Projects lists the saved projects (at most protocol.MaxAdminProjects).
	Projects func(ctx context.Context) ([]Project, error)
	// AddProject saves path as a project, or returns the saved one with that
	// path. It returns ErrTooManyProjects at the limit.
	AddProject func(ctx context.Context, path string) (id string, err error)
}

var (
	// ErrTooManyProjects is AddProject's answer at protocol.MaxAdminProjects.
	ErrTooManyProjects = errors.New("github: too many projects")
	// ErrNotConnected: no login on this computer.
	ErrNotConnected = errors.New("github: not connected")
	// ErrGitHubDown: GitHub did not answer, or answered with a server error.
	ErrGitHubDown = errors.New("github: GitHub cannot be reached")
	// ErrBusy: another Connect or set-up runs (from another phone).
	ErrBusy = errors.New("github: busy")
	// ErrNoGit: git is not on this computer.
	ErrNoGit = errors.New("github: git is not installed")
)

// Service is the GitHub side of one broker. Every method is safe for
// concurrent use. It is nil on Windows and in a build without ClientID.
type Service struct {
	config Config
	deps   Deps
}

// New builds the service, or returns nil, nil when the feature is off
// (no ClientID, or Windows). It removes the temp folders of this profile
// that a crash left (design 6.4) and reads the token file.
func New(config Config, deps Deps) (*Service, error) {
	return nil, nil
}

// Info is the settings field: the state and the login.
func (service *Service) Info() protocol.HTTPGitHubInfo {
	return protocol.HTTPGitHubInfo{State: protocol.HTTPGitHubStateNotConnected}
}

// State is GET /api/v1/github for one device: connect only for the device
// that started the Connect. In needsInstall it asks GitHub again in the
// background, at most once in 5 s (design 6.1 step 5).
func (service *Service) State(device string) protocol.HTTPGitHubResponse {
	return protocol.HTTPGitHubResponse{State: protocol.HTTPGitHubStateNotConnected, App: service.config.AppSlug}
}

// Connect starts the device flow for device, or answers the running one
// (ErrBusy when another device started it).
func (service *Service) Connect(ctx context.Context, device string) (protocol.HTTPGitHubResponse, error) {
	return protocol.HTTPGitHubResponse{}, ErrNotConnected
}

// Cancel ends a Connect that is not finished. It never touches a stored login.
func (service *Service) Cancel(device string) protocol.HTTPGitHubResponse {
	return service.State(device)
}

// Disconnect revokes both tokens at GitHub (best effort, 10 s) and deletes
// the token file. revoked is false when the revoke call failed.
func (service *Service) Disconnect(ctx context.Context, device string) (response protocol.HTTPGitHubResponse, revoked bool, err error) {
	return service.State(device), false, nil
}

// Repos is GET /api/v1/github/repos: at most 1000 rows, cached 60 s, one
// shared run, 15 s in total, with here and taken for each row.
func (service *Service) Repos(ctx context.Context) (protocol.HTTPGitHubReposResponse, error) {
	return protocol.HTTPGitHubReposResponse{Repos: []protocol.HTTPGitHubRepo{}}, ErrNotConnected
}

// Setup starts the set-up of repo (owner/name) for device. Every outcome is
// in the answer's setup field: cloning, finished (also "already here"), or
// failed with a problem. ErrBusy when a set-up runs.
func (service *Service) Setup(ctx context.Context, device, repo string) (protocol.HTTPGitHubResponse, error) {
	return service.State(device), ErrNotConnected
}

// Stop stops the running set-up; its result becomes failed/stopped.
func (service *Service) Stop(device string) protocol.HTTPGitHubResponse {
	return service.State(device)
}

// Credential is the answer of `pterm github-credential get`: the login, a
// fresh access token (refreshed when it has less than 5 minutes left) and
// its expiry. Never the refresh token.
func (service *Service) Credential(ctx context.Context) (login, token string, expires time.Time, err error) {
	return "", "", time.Time{}, ErrNotConnected
}

// Close stops the Connect poll and the set-up job and waits for them.
func (service *Service) Close() {}
