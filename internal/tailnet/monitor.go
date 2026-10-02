package tailnet

import (
	"context"
	"errors"
	"os"
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

// CommandSource runs `<binary> status --json`. The context's deadline kills
// the process; WaitDelay bounds a child that keeps its output pipe open.
func CommandSource(binary string) Source {
	return func(ctx context.Context) ([]byte, error) {
		if binary == "" {
			return nil, ErrUnavailable
		}
		command := exec.CommandContext(ctx, binary, "status", "--json")
		command.WaitDelay = time.Second
		return command.Output()
	}
}

// FindBinary returns the Tailscale CLI: on PATH, else where its installers put
// it. Homebrew paths are checked because launchd's PATH has neither
// /opt/homebrew/bin nor /usr/local/bin. It returns "" when none exists.
func FindBinary(goos string, getenv func(string) string, lookPath func(string) (string, error)) string {
	if path, err := lookPath("tailscale"); err == nil && path != "" {
		return path
	}
	var candidates []string
	switch goos {
	case "darwin":
		candidates = []string{
			"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
			"/opt/homebrew/bin/tailscale", "/usr/local/bin/tailscale",
		}
	case "windows":
		if programFiles := getenv("ProgramFiles"); filepath.IsAbs(programFiles) {
			candidates = []string{filepath.Join(programFiles, "Tailscale", "tailscale.exe")}
		}
	case "linux":
		// Distribution packages install here; sbin is often not on a user's PATH.
		candidates = []string{"/usr/bin/tailscale", "/usr/sbin/tailscale", "/usr/local/bin/tailscale"}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
