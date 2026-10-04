package broker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/herdr"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Terminal workspace providers (docs/herdr-provider.md): herdr. The broker
// wires one *herdr.Provider, keeps its Integrations settings in the settings
// table, answers the provider routes from its cache, limits them per device,
// and owns the in-memory provider sessions.

const (
	providerEnabledKey = "provider.herdr.enabled"
	providerNotifyKey  = "provider.herdr.notify"

	providerOpensPerMinute   = 20
	providerCreatesPerMinute = 3
	providerUpdatesPerMinute = 10
)

// providerFailure is a provider error with its fixed public code and text.
// The text is Termivoa's, never herdr's or git's.
type providerFailure struct{ managementFailure }

func (failure providerFailure) Error() string { return "broker: provider: " + failure.message }

var (
	errProviderUnknown     = providerFailure{managementFailure{protocol.InvalidRequest, "this computer has no such provider"}}
	errProviderUnavailable = providerFailure{managementFailure{protocol.HostUnavailable, "herdr is off or not running on this computer"}}
	errProviderTarget      = providerFailure{managementFailure{protocol.SessionMissing, "that terminal is gone"}}
	errProviderGroup       = providerFailure{managementFailure{protocol.SessionMissing, "that project is gone"}}
	errProviderSessions    = providerFailure{managementFailure{protocol.LimitExceeded, "4 herdr terminals are open; close one and try again"}}
	errProviderRate        = providerFailure{managementFailure{protocol.LimitExceeded, "too many requests; wait a minute and try again"}}
	errProviderBusy        = providerFailure{managementFailure{protocol.LimitExceeded, "another worktree is being made; try again"}}
	errProviderKind        = providerFailure{managementFailure{protocol.InvalidRequest, "herdr cannot start that agent here"}}
	errProviderNotGit      = providerFailure{managementFailure{protocol.NotARepository, "this project is not in a git repository"}}
	errProviderExists      = providerFailure{managementFailure{protocol.WorktreeExists, "a worktree or branch with this name already exists"}}
	errProviderWorktree    = providerFailure{managementFailure{protocol.WorktreeFailed, "herdr could not make the worktree."}}
	errProviderFeature     = providerFailure{managementFailure{protocol.InvalidRequest, "this is not available for a herdr terminal"}}
	errProviderChanges     = providerFailure{managementFailure{protocol.InvalidRequest, "this herdr terminal's folder cannot be read"}}
)

func classifyProviderError(err error) (managementFailure, bool) {
	var failure providerFailure
	if errors.As(err, &failure) {
		return failure.managementFailure, true
	}
	return managementFailure{}, false
}

// providerHost is the broker's side of one provider.
type providerHost struct {
	provider *herdr.Provider
	enabled  atomic.Bool
	notify   atomic.Pointer[[]protocol.HTTPProviderNotifyState]
	limits   providerLimits
	settings sync.Mutex // orders setting writes
}

// newProviderHost loads the Integrations settings (absent: on, both states)
// and builds the provider. onChange receives push transitions.
func newProviderHost(ctx context.Context, server *ManagementServer, config herdr.Config) (*providerHost, error) {
	host := &providerHost{}
	enabled, found, err := server.config.Store.GetSetting(ctx, providerEnabledKey)
	if err != nil {
		return nil, err
	}
	host.enabled.Store(!found || enabled != "0")
	notify := []protocol.HTTPProviderNotifyState{protocol.Blocked, protocol.Done}
	if value, found, err := server.config.Store.GetSetting(ctx, providerNotifyKey); err != nil {
		return nil, err
	} else if found {
		notify = parseNotify(value)
	}
	host.notify.Store(&notify)
	config.Enabled = host.enabled.Load
	host.provider = herdr.New(config, nil)
	return host, nil
}

func parseNotify(value string) []protocol.HTTPProviderNotifyState {
	notify := []protocol.HTTPProviderNotifyState{}
	for _, state := range []protocol.HTTPProviderNotifyState{protocol.Blocked, protocol.Done} {
		if slices.Contains(strings.Split(value, ","), string(state)) {
			notify = append(notify, state)
		}
	}
	return notify
}

func (host *providerHost) notifies(state protocol.HTTPProviderNotifyState) bool {
	return slices.Contains(*host.notify.Load(), state)
}

// info is HTTPProviderInfo with this computer's settings. installed is false
// when herdr is not installed: the computer then lists no provider.
func (host *providerHost) info() (protocol.HTTPProviderInfo, bool) {
	info, installed := host.provider.Info()
	info.Enabled = host.enabled.Load()
	info.Notify = append([]protocol.HTTPProviderNotifyState{}, *host.notify.Load()...)
	if !info.Enabled {
		info.State, info.CanCreate, info.AgentKinds = protocol.Off, false, []string{}
	}
	return info, installed
}

func (host *providerHost) snapshot() protocol.HTTPProviderSnapshotResponse {
	if !host.enabled.Load() {
		return protocol.HTTPProviderSnapshotResponse{Provider: herdr.ID, State: protocol.Off, Groups: []protocol.HTTPProviderGroup{}}
	}
	return host.provider.Snapshot()
}

// update saves the Integrations settings of this computer for all devices.
func (host *providerHost) update(ctx context.Context, server *ManagementServer, request protocol.HTTPProviderUpdateRequest) (protocol.HTTPProviderInfo, error) {
	host.settings.Lock()
	defer host.settings.Unlock()
	now := server.now()
	if request.Enabled != nil {
		value := "0"
		if *request.Enabled {
			value = "1"
		}
		if err := server.config.Store.PutSetting(ctx, providerEnabledKey, value, now); err != nil {
			return protocol.HTTPProviderInfo{}, err
		}
		host.enabled.Store(*request.Enabled)
	}
	if request.Notify != nil {
		var states []string
		for _, state := range request.Notify {
			states = append(states, string(state))
		}
		notify := parseNotify(strings.Join(states, ","))
		if err := server.config.Store.PutSetting(ctx, providerNotifyKey, strings.Join(states, ","), now); err != nil {
			return protocol.HTTPProviderInfo{}, err
		}
		host.notify.Store(&notify)
	}
	info, _ := host.info()
	return info, nil
}

// providerLimits is the per-device limiter of design S10: a fixed one-minute
// window per route and device.
type providerLimits struct {
	mu      sync.Mutex
	windows map[string]uploadWindow
}

func (limits *providerLimits) take(route, device string, perMinute int, now time.Time) bool {
	limits.mu.Lock()
	defer limits.mu.Unlock()
	if limits.windows == nil {
		limits.windows = map[string]uploadWindow{}
	}
	for key, window := range limits.windows {
		if now.Sub(window.start) >= time.Minute || now.Before(window.start) {
			delete(limits.windows, key)
		}
	}
	key := route + "\x00" + device
	window := limits.windows[key]
	if window.count >= perMinute {
		return false
	}
	if window.count == 0 {
		window.start = now
	}
	window.count++
	limits.windows[key] = window
	return true
}

// providerFor returns the host of an installed provider named id.
func (server *ManagementServer) providerFor(id string) (*providerHost, error) {
	host := server.providers
	if host == nil || id != herdr.ID {
		return nil, errProviderUnknown
	}
	if _, installed := host.provider.Info(); !installed {
		return nil, errProviderUnknown
	}
	return host, nil
}

// runProviders starts the poll loop; the returned stop waits for it.
func (server *ManagementServer) runProviders(ctx context.Context) (stop func()) {
	if server.providers == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	var wait sync.WaitGroup
	wait.Go(func() { server.providers.provider.Run(ctx) })
	return func() {
		cancel()
		wait.Wait()
	}
}

// dispatchProvider answers the gateway.providers.* requests.
func (server *ManagementServer) dispatchProvider(
	ctx context.Context, request protocol.GatewayRPCEnvelope, authorize func(string) (auth.Principal, error),
) (any, error) {
	switch request.Type {
	case protocol.GatewayProvidersGet:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCProviderPayload](request)
		if err != nil {
			return nil, err
		}
		if _, err := authorize(payload.Credential); err != nil {
			return nil, err
		}
		host, err := server.providerFor(payload.Provider)
		if err != nil {
			return nil, err
		}
		return host.snapshot(), nil
	case protocol.GatewayProvidersUpdate:
		payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCProviderUpdatePayload](request)
		if err != nil {
			return nil, err
		}
		principal, err := authorize(payload.Credential)
		if err != nil {
			return nil, err
		}
		host, err := server.providerFor(payload.Provider)
		if err != nil {
			return nil, err
		}
		if !host.limits.take("update", principal.DeviceID, providerUpdatesPerMinute, server.now()) {
			return nil, errProviderRate
		}
		return host.update(ctx, server, payload.Request)
	default:
		return nil, errors.New("broker: unsupported provider request")
	}
}
