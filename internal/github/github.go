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
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	// RuntimeDir is this broker's runtime folder. A development broker
	// names it in the helper line (`--dev --runtime-dir`); a production
	// helper finds it from HOME or XDG_RUNTIME_DIR, which the clone puts back
	// in front of the helper (git runs with an empty temp HOME there).
	RuntimeDir string
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
// concurrent use. It is nil on Windows and in a build without ClientID;
// every method answers a nil Service.
type Service struct {
	config    Config
	deps      Deps
	opts      options
	home      string
	client    *http.Client
	freeSpace func(path string) (uint64, error)

	ctx    context.Context // ends on Close
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// tokenMu serializes every token change (refresh, login, disconnect).
	// Order: tokenMu before mu.
	tokenMu sync.Mutex
	// generation counts Disconnects (guarded by tokenMu).
	generation uint64

	mu             sync.Mutex
	closed         bool // Close ran: no new background work
	account        *account
	problem        *protocol.HTTPGitHubProblem
	connect        *connectRun
	setup          *setupRun
	installKnown   bool
	needsInstall   bool
	installChecked time.Time
	installRunning bool
	cache          *reposCache
	cacheGen       uint64 // guarded by mu: dropCache counts up, so a list run that started before it never writes its old result
	reposRun       *reposRun
	origins        map[string]originEntry

	gitMu     sync.Mutex
	gitPath   string
	gitLooked time.Time
}

// New builds the service, or returns nil, nil when the feature is off
// (no ClientID, or not macOS or Linux). It removes the temp folders of this
// profile that a crash left (design 6.4) and reads the token file. It runs
// `git --version` once (at most 2 s for each candidate).
func New(config Config, deps Deps) (*Service, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, nil
	}
	return newService(config, deps, defaultOptions)
}

var errConfig = errors.New("github: Home and DataDir must be absolute folders")

func newService(config Config, deps Deps, opts options) (*Service, error) {
	if config.ClientID == "" {
		config.ClientID = ClientID
	}
	if config.AppSlug == "" {
		config.AppSlug = AppSlug
	}
	// The helper value quotes both paths for the shell; a path with a
	// single quote cannot be quoted that way, so the feature is off.
	if config.ClientID == "" || strings.Contains(config.Pterm, "'") || strings.Contains(config.RuntimeDir, "'") || strings.Contains(config.Home, "'") || xdgQuote() {
		return nil, nil
	}
	if !filepath.IsAbs(config.Home) || !filepath.IsAbs(config.DataDir) {
		return nil, errConfig
	}
	home, err := filepath.EvalSymlinks(config.Home)
	if err != nil {
		return nil, err
	}
	// A damaged or foreign token file reads as "not connected": the broker
	// still starts, and the next Connect replaces the file.
	acc, _ := loadAccount(config.DataDir)
	ctx, cancel := context.WithCancel(context.Background())
	service := &Service{
		config: config, deps: deps, opts: opts, home: home,
		client: newHTTPClient(opts), freeSpace: freeSpace,
		ctx: ctx, cancel: cancel, account: acc, origins: map[string]originEntry{},
	}
	service.sweep()
	service.gitBinary()
	return service, nil
}

func (service *Service) now() time.Time {
	if service.deps.Now != nil {
		return service.deps.Now()
	}
	return time.Now()
}

func (service *Service) current() *account {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.account
}

// Info is the settings field: the state and the login.
func (service *Service) Info() protocol.HTTPGitHubInfo {
	if service == nil {
		return protocol.HTTPGitHubInfo{State: protocol.HTTPGitHubStateNotConnected}
	}
	response := service.State("")
	return protocol.HTTPGitHubInfo{State: response.State, Login: response.Login}
}

// State is GET /api/v1/github for one device: connect only for the device
// that started the Connect. In needsInstall (or before the first look) it
// asks GitHub again in the background, at most once in 5 s (design 6.1
// step 5). It does not block on GitHub; without git it may run
// `git --version` (at most once in 10 s).
func (service *Service) State(device string) protocol.HTTPGitHubResponse {
	if service == nil {
		return protocol.HTTPGitHubResponse{State: protocol.HTTPGitHubStateNotConnected}
	}
	hasGit := service.gitBinary() != ""
	service.mu.Lock()
	defer service.mu.Unlock()
	response := protocol.HTTPGitHubResponse{App: service.config.AppSlug}
	if acc := service.account; acc != nil {
		login := acc.login
		response.Login = &login
	}
	if service.problem != nil {
		problem := *service.problem
		response.Problem = &problem
	}
	if run := service.setup; run != nil && (run.running || service.now().Sub(run.ended) < resultFor) {
		view := run.view
		response.Setup = &view
	}
	switch {
	case !hasGit:
		response.State = protocol.NoGit
	case service.connect != nil:
		response.State = protocol.Connecting
		if run := service.connect; run.device == device && device != "" && run.userCode != "" {
			response.Connect = &protocol.HTTPGitHubConnect{UserCode: run.userCode, ExpiresAt: run.expires.UnixMilli()}
		}
	case service.account == nil:
		response.State = protocol.HTTPGitHubStateNotConnected
	case service.installKnown && service.needsInstall:
		response.State = protocol.NeedsInstall
	default:
		response.State = protocol.HTTPGitHubStateReady
	}
	if !service.closed && service.account != nil && (!service.installKnown || service.needsInstall) &&
		!service.installRunning && service.now().Sub(service.installChecked) >= installRecheck {
		service.installRunning, service.installChecked = true, service.now()
		service.wg.Add(1)
		go func() {
			defer service.wg.Done()
			service.checkInstalls(service.ctx)
			service.mu.Lock()
			service.installRunning = false
			service.mu.Unlock()
		}()
	}
	return response
}

// Close stops the Connect poll, the list run and the set-up job and waits
// for them. A clone that Close stops leaves no temp folder. After Close,
// Connect, Repos and Setup answer ErrGitHubDown.
func (service *Service) Close() {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.closed = true
	service.mu.Unlock()
	service.cancel()
	service.wg.Wait()
}

// xdgQuote is true when an XDG folder that the clone's helper gets back holds
// a single quote: the helper value quotes it for the shell, so the feature
// is off, as for a home folder with a quote.
func xdgQuote() bool {
	for _, name := range xdgNames {
		if strings.Contains(os.Getenv(name), "'") {
			return true
		}
	}
	return false
}
