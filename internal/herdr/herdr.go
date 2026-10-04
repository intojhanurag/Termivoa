// Package herdr reads a local herdr (https://herdr.dev) for the phone: it
// finds the binary, polls the default session's socket every 2 s, maps the
// snapshot to the generic provider wire contract (docs/herdr-provider.md,
// sections 4 and 5), reports state changes worth a push, and builds the argv
// that attaches one terminal. All herdr knowledge sits here; the broker uses
// one concrete *Provider. Reads come from a cache and never wait on herdr.
package herdr

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	// ID is the provider ID on the wire; Name is its display name.
	ID   = "herdr"
	Name = "herdr"
	// MinProtocol is herdr 0.8.2's socket protocol, the oldest supported.
	MinProtocol = 20

	pollEvery   = 2 * time.Second
	lookupEvery = 30 * time.Second // binary missing, or protocol too old
	retryEvery  = 5 * time.Second  // server not running
	callTimeout = time.Second      // one socket call while polling
	missLimit   = 3                // timeouts in a row before notRunning
	blockedFor  = 2 * time.Second  // blocked this long before a push
	doneAfter   = 20 * time.Second // observed work before a finished push
)

var (
	// ErrUnavailable: the provider is off, not running or unsupported.
	ErrUnavailable = errors.New("herdr: not available")
	// ErrTargetMissing: the target or group is not in the latest snapshot,
	// or that terminal cannot be opened.
	ErrTargetMissing = errors.New("herdr: target is not in the latest snapshot")
)

// Config has no defaults: it names every path the provider may use. cmd/pterm
// builds the production one; tests use temp folders only (guard.go).
type Config struct {
	// BinaryPaths are absolute herdr paths tried in order before PathEnv.
	BinaryPaths []string
	// PathEnv is the PATH searched after BinaryPaths ("" searches nothing).
	// It is also PATH for every herdr process the broker starts.
	PathEnv string
	// Home holds .config/herdr (the socket and config.toml).
	Home string
	// Session names a herdr session. Empty is the default session, the only
	// one the product reads; the opt-in lab test uses its own.
	Session string
	// Enabled is the Integrations switch, read before every poll. Nil is on.
	Enabled func() bool
}

func (config Config) folder() string {
	folder := filepath.Join(config.Home, ".config", "herdr")
	if config.Session != "" {
		folder = filepath.Join(folder, "sessions", config.Session)
	}
	return folder
}

func (config Config) socketPath() string { return filepath.Join(config.folder(), "herdr.sock") }

// Change is one agent transition worth a push: To is "blocked" (for at least
// 2 s) or "done" (done or idle after at least 20 s of observed work). Group
// and Item are cleaned names, Agent the kind; never a title or screen text.
type Change struct {
	Key, Group, Item, Agent, From, To string
	Worked                            time.Duration
}

// Attach starts one terminal of the latest snapshot: argv, never a shell.
type Attach struct {
	Argv      []string // the herdr path first
	Env       []string // fixed list (design S11); ptyhost adds its own allowlist
	Directory string   // the workspace checkout path; "" outside git
	Title     string   // "herdr · shop · silver-meadow"
	Key       string   // the target ID: pane ID + terminal ID
}

// Provider is one herdr on this computer. Run owns the poll loop; every
// other method reads the published view.
type Provider struct {
	config   Config
	onChange func(Change)
	now      func() time.Time
	logf     func(format string, values ...any) // the broker log; Inspect is quiet

	mu   sync.RWMutex
	view view

	// create runs one M2 create at a time on this computer.
	create sync.Mutex
	// wake asks the poll loop for a snapshot now (after a create).
	wake chan struct{}
	// turns counts the poll loop's turns as they start; view.turn is the
	// last turn that ended. A turn above a count taken now saw herdr after now.
	turns atomic.Uint64

	// Owned by the poll loop (step).
	binary     string
	lookupAt   time.Time
	retryAt    time.Time
	discovered bool
	misses     int
	caps       capabilities
	capsFor    string
	baseline   bool
	logged     string // the socket refusal last logged
	tracks     map[string]*track
	since      map[string]stateStamp
}

// view is what readers see; replaced as a whole under mu.
type view struct {
	installed bool
	binary    string
	state     protocol.HTTPProviderState
	version   string
	protocol  int
	caps      capabilities
	response  protocol.HTTPProviderSnapshotResponse
	targets   map[string]target
	groups    map[string]groupRef
	kinds     []string
	turn      uint64 // the last poll turn that ended (Provider.turns)
	// refused is why the socket is not used (design S3), with the fix; ""
	// when it is used or missing. Host only: pterm doctor and the log.
	refused string
}

type capabilities struct {
	attachAny bool     // `terminal attach` exists: tabs without an agent open
	kinds     []string // `agent start --kind` values
}

// New builds a provider. It does nothing until Run. onChange receives push
// transitions on the poll goroutine and must return promptly.
func New(config Config, onChange func(Change)) *Provider {
	guardTestConfig(config)
	if onChange == nil {
		onChange = func(Change) {}
	}
	provider := &Provider{config: config, onChange: onChange, now: time.Now, logf: log.Printf, wake: make(chan struct{}, 1)}
	provider.view = view{state: protocol.NotRunning, response: emptyResponse(protocol.NotRunning)}
	return provider
}

// Run polls until ctx ends.
func (provider *Provider) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-provider.wake:
			timer.Stop() // Go 1.23+ timers: no stale tick follows Stop.
		}
		turn := provider.turns.Add(1)
		wait := provider.step(ctx)
		provider.publish(func(current *view) { current.turn = turn })
		timer.Reset(wait)
	}
}

// Info is the detection part of HTTPProviderInfo (the broker adds enabled
// and notify). installed is false when no usable herdr binary was found:
// then this computer lists no provider at all.
func (provider *Provider) Info() (info protocol.HTTPProviderInfo, installed bool) {
	provider.mu.RLock()
	current := provider.view
	provider.mu.RUnlock()
	info = protocol.HTTPProviderInfo{
		ID: ID, Name: Name, State: current.state, AgentKinds: []string{}, Notify: []protocol.HTTPProviderNotifyState{},
	}
	if current.version != "" && validVersion(current.version) {
		version := current.version
		info.Version = &version
	}
	if current.state == protocol.HTTPProviderStateReady {
		info.CanCreate = true
		info.AgentKinds = append(info.AgentKinds, current.kinds...)
	}
	return info, current.installed
}

// Snapshot returns the cached response; it never waits on herdr.
func (provider *Provider) Snapshot() protocol.HTTPProviderSnapshotResponse {
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	return provider.view.response
}

// Has reports whether key (a target ID) is in the latest ready snapshot.
// Provider sessions end when it turns false.
func (provider *Provider) Has(key string) bool {
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	_, ok := provider.view.targets[key]
	return ok && provider.view.state == protocol.HTTPProviderStateReady
}

// Attach checks targetID against the cache and returns how to attach it.
func (provider *Provider) Attach(targetID string) (Attach, error) {
	provider.mu.RLock()
	current := provider.view
	provider.mu.RUnlock()
	if current.state != protocol.HTTPProviderStateReady {
		return Attach{}, ErrUnavailable
	}
	found, ok := current.targets[targetID]
	if !ok || !found.canOpen {
		return Attach{}, ErrTargetMissing
	}
	argv := provider.command(current.binary)
	if found.agent {
		argv = append(argv, "agent", "attach", found.pane)
	} else {
		argv = append(argv, "terminal", "attach", found.terminal)
	}
	return Attach{
		Argv: argv, Env: provider.environment(true), Directory: found.directory,
		Title: Title(found.group, found.item, 32), Key: targetID,
	}, nil
}

// command is argv up to the subcommand: the binary and the session flag.
func (provider *Provider) command(binary string) []string {
	argv := []string{binary}
	if provider.config.Session != "" {
		argv = append(argv, "--session", provider.config.Session)
	}
	return argv
}

// environment is the fixed list of design S11. attach adds the terminal
// variables for an attach client.
func (provider *Provider) environment(attach bool) []string {
	env := []string{
		"HOME=" + provider.config.Home, "PATH=" + provider.config.PathEnv,
		"HERDR_SOCKET_PATH=" + provider.config.socketPath(),
	}
	if attach {
		env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	}
	return env
}

func (provider *Provider) publish(change func(*view)) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	next := provider.view
	change(&next)
	provider.view = next
}

// setState publishes a state without groups, and forgets per-target state so
// the next ready snapshot is a new baseline (no push burst).
func (provider *Provider) setState(state protocol.HTTPProviderState, version string, protocolNumber int) {
	provider.discovered = false
	provider.tracks, provider.since = nil, nil
	provider.publish(func(current *view) {
		current.installed, current.binary = provider.binary != "", provider.binary
		current.state, current.version, current.protocol = state, version, protocolNumber
		current.response = emptyResponse(state)
		current.targets, current.groups, current.kinds, current.refused = nil, nil, nil, ""
	})
}

func emptyResponse(state protocol.HTTPProviderState) protocol.HTTPProviderSnapshotResponse {
	return protocol.HTTPProviderSnapshotResponse{Provider: ID, State: state, Groups: []protocol.HTTPProviderGroup{}}
}

// step is one turn of the poll loop and returns the wait before the next.
// It never holds mu while it talks to herdr.
func (provider *Provider) step(ctx context.Context) time.Duration {
	now := provider.now()
	if provider.binary == "" && !now.Before(provider.lookupAt) {
		provider.binary = provider.findBinary()
		provider.lookupAt = now.Add(lookupEvery)
		provider.retryAt = time.Time{}
	}
	if provider.binary == "" {
		provider.setState(protocol.NotRunning, "", 0)
		return pollEvery
	}
	if provider.config.Enabled != nil && !provider.config.Enabled() {
		provider.setState(protocol.Off, "", 0)
		return pollEvery
	}
	if !provider.discovered {
		if now.Before(provider.retryAt) {
			return pollEvery
		}
		if !provider.discover(ctx, now) {
			return pollEvery
		}
	}
	snapshot, err := provider.readSnapshot(ctx)
	switch {
	case err == nil:
		provider.misses = 0
	case errors.Is(err, errUnsupported):
		provider.setState(protocol.Unsupported, provider.view.version, provider.view.protocol)
		provider.retryAt = now.Add(lookupEvery)
		return pollEvery
	case timedOut(err):
		if provider.misses++; provider.misses < missLimit {
			return pollEvery
		}
		fallthrough
	default:
		provider.notRunning(now)
		return pollEvery
	}
	provider.apply(snapshot, now)
	return pollEvery
}

// Poll asks the poll loop for a turn now.
func (provider *Provider) Poll() {
	select {
	case provider.wake <- struct{}{}:
	default:
	}
}

// settle asks for a turn now and waits, at most timeout, until a turn that
// started after the call ended with targetID in its view. It reports whether
// that happened.
func (provider *Provider) settle(ctx context.Context, timeout time.Duration, targetID string) bool {
	after := provider.turns.Load()
	deadline := time.Now().Add(timeout)
	for {
		provider.Poll()
		provider.mu.RLock()
		_, ok := provider.view.targets[targetID]
		fresh := provider.view.turn > after
		provider.mu.RUnlock()
		if ok && fresh {
			return true
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// discover pings the server and probes a new binary or server version once.
func (provider *Provider) discover(ctx context.Context, now time.Time) bool {
	pong, err := provider.ping(ctx)
	if err != nil {
		provider.notRunning(now)
		provider.refuse(err)
		return false
	}
	provider.logged = ""
	if pong.Protocol < MinProtocol {
		provider.setState(protocol.Unsupported, pong.Version, pong.Protocol)
		provider.retryAt = now.Add(lookupEvery)
		return false
	}
	if key := provider.binary + "\x00" + pong.Version; key != provider.capsFor {
		provider.caps, provider.capsFor = provider.probe(ctx), key
	}
	provider.discovered, provider.misses, provider.baseline = true, 0, true
	provider.tracks, provider.since = map[string]*track{}, map[string]stateStamp{}
	provider.publish(func(current *view) {
		current.version, current.protocol, current.caps = pong.Version, pong.Protocol, provider.caps
	})
	return true
}

// notRunning also rechecks the binary at the next lookup, so an uninstalled
// herdr disappears from the settings.
func (provider *Provider) notRunning(now time.Time) {
	if provider.findBinaryAgain() {
		provider.retryAt = now.Add(retryEvery)
	} else {
		provider.binary, provider.lookupAt = "", now.Add(lookupEvery)
	}
	provider.setState(protocol.NotRunning, "", 0)
}

// refuse publishes why the socket was refused, if it was, and logs each new
// reason once (it is not logged again on every 5 s retry).
func (provider *Provider) refuse(err error) {
	var refused *socketRefused
	if !errors.As(err, &refused) {
		provider.logged = ""
		return
	}
	if refused.reason != provider.logged {
		provider.logged = refused.reason
		provider.logf("herdr: %s is not used: %s", provider.config.socketPath(), refused.reason)
	}
	provider.publish(func(current *view) { current.refused = refused.reason })
}

func timedOut(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errDeadline)
}
