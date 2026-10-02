package tailnet

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	// DefaultMaxAge is how long a snapshot is used before a read starts a refresh.
	DefaultMaxAge = 60 * time.Second
	// DefaultGrace keeps the last good snapshot this long after it was read
	// while refreshes fail. After it, bound-origin requests are refused.
	DefaultGrace = 10 * time.Minute
	// DefaultTimeout is the hard limit of one `tailscale status --json` run.
	DefaultTimeout = 10 * time.Second
	// retryDelay spaces background refreshes after a failure, so a broken
	// tailscale is not run once per request.
	retryDelay = 5 * time.Second
)

// ErrUnavailable means no snapshot within the grace period exists.
var ErrUnavailable = errors.New("tailnet: tailscale status is unavailable")

// Source returns the output of `tailscale status --json`.
type Source func(ctx context.Context) ([]byte, error)

// Config injects the status source and clock. Zero durations use defaults.
type Config struct {
	Source  Source
	Now     func() time.Time
	MaxAge  time.Duration
	Grace   time.Duration
	Timeout time.Duration
	// OnUpdate runs after each successful refresh with the new snapshot,
	// still inside the single refresh, so two calls never overlap.
	OnUpdate func(*Status)
}

// Monitor publishes the latest good Status. Readers never run tailscale and
// never wait: Snapshot returns what is published and, when it is stale, starts
// at most one background refresh with a hard time limit.
type Monitor struct {
	config Config

	mu         sync.Mutex
	current    *Status
	readAt     time.Time     // When current was read.
	startedAt  time.Time     // When the last refresh started.
	refreshing chan struct{} // Closed when the refresh in flight ends; nil when idle.
}

func NewMonitor(config Config) *Monitor {
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.MaxAge <= 0 {
		config.MaxAge = DefaultMaxAge
	}
	if config.Grace <= 0 {
		config.Grace = DefaultGrace
	}
	if config.Timeout <= 0 {
		config.Timeout = DefaultTimeout
	}
	if config.Source == nil {
		config.Source = func(context.Context) ([]byte, error) { return nil, ErrUnavailable }
	}
	return &Monitor{config: config}
}

// Snapshot returns the last good status while it is within the grace period,
// or nil. It never blocks. A status older than MaxAge (or none) starts one
// background refresh.
func (monitor *Monitor) Snapshot() *Status {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	now := monitor.config.Now()
	age := now.Sub(monitor.readAt)
	if (monitor.current == nil || age >= monitor.config.MaxAge || age < 0) &&
		(monitor.startedAt.IsZero() || now.Sub(monitor.startedAt) >= retryDelay || now.Before(monitor.startedAt)) {
		monitor.startLocked()
	}
	if monitor.current == nil || age >= monitor.config.Grace || age < 0 {
		return nil
	}
	return monitor.current
}

// Fresh returns a status no older than MaxAge, waiting for one refresh if
// needed. It is for local administration and rare user actions (pairing,
// adding a computer), never for per-request authorization.
func (monitor *Monitor) Fresh(ctx context.Context) (*Status, error) {
	monitor.mu.Lock()
	if age := monitor.config.Now().Sub(monitor.readAt); monitor.current != nil && age >= 0 && age < monitor.config.MaxAge {
		current := monitor.current
		monitor.mu.Unlock()
		return current, nil
	}
	done := monitor.startLocked()
	monitor.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if age := monitor.config.Now().Sub(monitor.readAt); monitor.current == nil || age < 0 || age >= monitor.config.MaxAge {
		return nil, ErrUnavailable
	}
	return monitor.current, nil
}

// startLocked starts a refresh unless one is in flight and returns its done channel.
func (monitor *Monitor) startLocked() chan struct{} {
	if monitor.refreshing != nil {
		return monitor.refreshing
	}
	done := make(chan struct{})
	monitor.refreshing, monitor.startedAt = done, monitor.config.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), monitor.config.Timeout)
		data, err := monitor.config.Source(ctx)
		cancel()
		var status *Status
		if err == nil {
			status, err = ParseStatus(data)
		}
		// While Tailscale starts, restarts or is logged out its status lists
		// no usable peers. That is a failed read, not a new truth: the last
		// good snapshot stays under the grace rules.
		if err == nil && status.BackendState != "Running" {
			err = ErrUnavailable
		}
		if err == nil {
			monitor.mu.Lock()
			monitor.current, monitor.readAt = status, monitor.config.Now()
			monitor.mu.Unlock()
			if monitor.config.OnUpdate != nil {
				monitor.config.OnUpdate(status)
			}
		}
		monitor.mu.Lock()
		monitor.refreshing = nil
		monitor.mu.Unlock()
		close(done)
	}()
	return done
}

// lookupRetry spaces lookups of a Tailscale CLI that was not found, so one
// installed later is used without a restart.
var lookupRetry = time.Minute

// CommandSource runs `<binary> status --json` with the binary find returns.
// While find returns "", it is asked again at most once per lookupRetry; a
// found binary is kept. The context's deadline kills the process; WaitDelay
// bounds a child that keeps its output pipe open.
func CommandSource(find func() string) Source {
	var mu sync.Mutex
	var binary string
	var lookedAt time.Time
	return func(ctx context.Context) ([]byte, error) {
		mu.Lock()
		if binary == "" && (lookedAt.IsZero() || time.Since(lookedAt) >= lookupRetry) {
			binary, lookedAt = find(), time.Now()
		}
		path := binary
		mu.Unlock()
		if path == "" {
			return nil, ErrUnavailable
		}
		command := exec.CommandContext(ctx, path, "status", "--json")
		command.WaitDelay = time.Second
		return command.Output()
	}
}

// FindBinary returns the Tailscale CLI, or "" when none is usable. Fixed
// install locations come first and PATH last, so a writable folder early on
// PATH cannot plant a fake tailscale. The order is most to least protected:
// /usr/bin and /usr/sbin (owned by the system package manager; read-only
// under macOS SIP), the macOS app bundle (the CLI of the Tailscale app that
// runs the daemon), /usr/local/bin, and Homebrew's /opt/homebrew/bin last on
// macOS (often owned by the user). launchd's PATH has neither Homebrew
// folder, so they must be listed. Every candidate must pass safeBinary.
func FindBinary(goos string, getenv func(string) string, lookPath func(string) (string, error)) string {
	var candidates []string
	switch goos {
	case "darwin":
		candidates = []string{
			"/usr/bin/tailscale", "/usr/sbin/tailscale",
			"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
			"/usr/local/bin/tailscale", "/opt/homebrew/bin/tailscale",
		}
	case "linux":
		candidates = []string{"/usr/bin/tailscale", "/usr/sbin/tailscale", "/usr/local/bin/tailscale"}
	case "windows":
		if programFiles := getenv("ProgramFiles"); filepath.IsAbs(programFiles) {
			candidates = []string{filepath.Join(programFiles, "Tailscale", "tailscale.exe")}
		}
	}
	if path, err := lookPath("tailscale"); err == nil && filepath.IsAbs(path) {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if safeBinary(candidate) {
			return candidate
		}
	}
	return ""
}
