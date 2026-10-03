package broker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

// Multi-computer trust (docs/multi-computer.md, sections 3 and 4). A request
// from this computer's own origin is never affected. A request from another
// origin is accepted only when that origin is the app origin bound to its own
// credential, grant or pending pairing, and the latest Tailscale snapshot
// still shows the pinned node there. Authorization only reads the snapshot.

const (
	// PendingComputerLifetime bounds a pending computer-list entry: long
	// enough to redeem, approve and confirm a pairing; then it is deleted.
	PendingComputerLifetime = 10 * time.Minute
	// trustSweepInterval is how often open streams from app origins are
	// rechecked, and node changes turned into revocations.
	trustSweepInterval = 15 * time.Second
)

var (
	errTrustUnavailable = errors.New("broker: this computer cannot read its Tailscale status")
	errComputerSelf     = errors.New("broker: a computer cannot list itself")
)

// isOwnOrigin: an absent origin was admitted by the gateway as same-origin.
// The configured origin is this computer's own as well, and so, in the
// development profile only, are the gateway's loopback origins. A bound app
// origin is always https.
func (server *ManagementServer) isOwnOrigin(origin *string) bool {
	if origin == nil {
		return true
	}
	if own := server.ownOrigin.Load(); own != nil && *own == *origin {
		return true
	}
	return server.config.Development &&
		(strings.HasPrefix(*origin, "http://127.0.0.1:") || strings.HasPrefix(*origin, "http://localhost:"))
}

// trust is the snapshot's verdict on one app binding.
func trust(status *tailnet.Status, app store.AppBinding) tailnet.Verdict {
	return status.Trust(app.Origin, app.NodeID, app.NodeTags)
}

// appTrust reads the latest snapshot only. A definite node change is a
// revocation; a missing name, another owner or a changed tag set refuses
// without revoking.
func (server *ManagementServer) appTrust(app store.AppBinding) error {
	status := server.config.Tailnet.Snapshot()
	if status == nil {
		return errTrustUnavailable
	}
	switch trust(status, app) {
	case tailnet.Trusted:
		return nil
	case tailnet.Changed:
		return auth.ErrRevoked
	default:
		return auth.ErrOriginRefused
	}
}

// admitDevice applies per-device origin binding to one authenticated request.
func (server *ManagementServer) admitDevice(origin *string, principal auth.Principal) error {
	if server.isOwnOrigin(origin) {
		return nil
	}
	if !principal.App.Bound() || principal.App.Origin != *origin {
		return auth.ErrOriginRefused
	}
	return server.appTrust(principal.App)
}

// admitOwn admits a request only from this computer's own origin: the
// per-device computer list belongs to the app this computer serves.
func (server *ManagementServer) admitOwn(origin *string) error {
	if !server.isOwnOrigin(origin) {
		return auth.ErrOriginRefused
	}
	return nil
}

// pairingCheck admits a redeem or poll only from the origin its own grant or
// pending pairing is bound to (own origin for an unbound one).
func (server *ManagementServer) pairingCheck(origin *string) auth.AppCheck {
	return func(app store.AppBinding) error {
		if !app.Bound() {
			if server.isOwnOrigin(origin) {
				return nil
			}
			return auth.ErrOriginRefused
		}
		if origin == nil || *origin != app.Origin {
			return auth.ErrOriginRefused
		}
		if err := server.appTrust(app); errors.Is(err, auth.ErrRevoked) {
			return auth.ErrOriginRefused // No device exists yet to revoke.
		} else if err != nil {
			return err
		}
		return nil
	}
}

// streamOrigin is the origin a live stream records ("" for own).
func (server *ManagementServer) streamOrigin(origin *string) string {
	if server.isOwnOrigin(origin) {
		return ""
	}
	return *origin
}

// originProbe answers gateway.origin. The gateway uses its origin sets only
// for CORS headers; the broker authorizes every request on its own.
//
// boundOrigins are the app origins of every device ever bound to one,
// revoked and expired devices included, whose pinned node the latest snapshot
// still trusts or that a different node now holds (Changed). An app whose
// credential here was revoked or expired must read this computer's 401 to
// show "pair again" instead of "can't reach"; so must home after it got a new
// node ID. CORS lets an origin read only what the broker answers it: for a
// Changed origin every credential bound to it is revoked (appTrust, 401
// "revoked"), every other one and every grant is refused, so whoever now holds
// the name reads only refusals. A gone name, another owner, a changed tag set
// or an unreadable status gets no CORS. pairingOrigins are those of unexpired
// grants and pending pairings.
func (server *ManagementServer) originProbe(ctx context.Context) (protocol.GatewayRPCOriginResultPayload, error) {
	origin, err := server.pairingOrigin(ctx)
	if err != nil {
		return protocol.GatewayRPCOriginResultPayload{}, err
	}
	result := protocol.GatewayRPCOriginResultPayload{Origin: origin}
	if server.config.Auth == nil {
		return result, nil
	}
	apps, err := server.config.Store.BoundApps(ctx)
	if err != nil {
		return protocol.GatewayRPCOriginResultPayload{}, err
	}
	if len(apps) > 0 {
		if status := server.config.Tailnet.Snapshot(); status != nil {
			for _, app := range apps {
				if verdict := trust(status, app); (verdict == tailnet.Trusted || verdict == tailnet.Changed) &&
					!slices.Contains(result.BoundOrigins, app.Origin) && len(result.BoundOrigins) < protocol.MaxBoundOrigins {
					result.BoundOrigins = append(result.BoundOrigins, app.Origin)
				}
			}
		}
	}
	now := server.now()
	if result.PairingOrigins, err = server.config.Store.PairingOrigins(ctx, now, protocol.MaxPairingOrigins); err != nil {
		return protocol.GatewayRPCOriginResultPayload{}, err
	}
	return result, nil
}

// resolveVia resolves pterm pair --via in a fresh snapshot, applies the owner
// rule and pins the node. It also returns the node, whose Tailscale IP an
// offer to that app's computer dials.
func (server *ManagementServer) resolveVia(ctx context.Context, via string, allowTagged bool) (store.AppBinding, tailnet.Node, error) {
	status, err := server.config.Tailnet.Fresh(ctx)
	if err != nil {
		return store.AppBinding{}, tailnet.Node{}, errors.Join(errTrustUnavailable, err)
	}
	node, err := status.Resolve(via)
	if err != nil {
		return store.AppBinding{}, tailnet.Node{}, err
	}
	if err := status.CheckOwner(node, allowTagged); err != nil {
		return store.AppBinding{}, tailnet.Node{}, err
	}
	return store.AppBinding{Origin: node.Origin(), NodeID: node.ID, NodeOwner: node.Owner, NodeTags: node.Tags}, node, nil
}

// lookupComputer is home's check before it lists a computer: the host must be
// a node of its own tailnet, owned by home's owner or tagged. It uses Fresh,
// which answers from the cached snapshot while it is under 60 s old and runs
// `tailscale status` (10 s limit) only when it is older. The grace snapshot
// that Snapshot returns can be 10 minutes old and is nil right after start;
// pinning a node for a new computer, a rare user action, needs a recent one.
func (server *ManagementServer) lookupComputer(ctx context.Context, origin string) (tailnet.Node, *tailnet.Status, error) {
	if own := server.ownOrigin.Load(); own != nil && *own == origin {
		return tailnet.Node{}, nil, errComputerSelf
	}
	status, err := server.config.Tailnet.Fresh(ctx)
	if err != nil {
		return tailnet.Node{}, nil, errors.Join(errTrustUnavailable, err)
	}
	node, ok := status.Peer(strings.TrimPrefix(origin, "https://"))
	if !ok {
		return tailnet.Node{}, nil, tailnet.ErrNotFound
	}
	return node, status, status.CheckOwner(node, true)
}

// hiddenComputer reports an entry whose name now belongs to another node. The
// app never sees it, so its user cannot remove it, and a full list drops it
// for room. A name merely missing from status (a machine removed from the
// tailnet, or a peer ParseStatus skipped) stays listed: nothing answers
// there, the app shows it as unreachable, and its user can remove it.
func hiddenComputer(status *tailnet.Status, computer store.Computer) bool {
	node, ok := status.Peer(strings.TrimPrefix(computer.Origin, "https://"))
	return ok && node.ID != computer.NodeID
}

// listComputers lists the device's computers. Once a snapshot exists, an
// entry whose pinned node no longer holds its name is left out, so the app's
// CSP never allows a machine the phone did not add. Without a snapshot yet
// (the first read after start) entries stay: CSP is a second fence, and each
// peer checks its own binding.
func (server *ManagementServer) listComputers(ctx context.Context, principal auth.Principal) (protocol.GatewayRPCComputerListResultPayload, error) {
	computers, err := server.config.Store.ListComputers(ctx, principal.DeviceID, server.now())
	if err != nil {
		return protocol.GatewayRPCComputerListResultPayload{}, err
	}
	result := protocol.GatewayRPCComputerListResultPayload{Computers: []protocol.HTTPComputer{}, PendingOrigins: []string{}}
	var status *tailnet.Status
	if len(computers) > 0 {
		status = server.config.Tailnet.Snapshot()
	}
	for _, computer := range computers {
		if status != nil && hiddenComputer(status, computer) {
			continue
		}
		if computer.State == store.ComputerConfirmed {
			result.Computers = append(result.Computers, protocol.HTTPComputer{Origin: computer.Origin, Label: computer.Label})
		} else {
			result.PendingOrigins = append(result.PendingOrigins, computer.Origin)
		}
	}
	return result, nil
}

func (server *ManagementServer) addComputer(
	ctx context.Context, principal auth.Principal, request protocol.HTTPComputerAddRequest,
) (protocol.HTTPComputerAddResponse, error) {
	node, status, err := server.lookupComputer(ctx, request.Origin)
	if err != nil {
		return protocol.HTTPComputerAddResponse{}, err
	}
	now := server.now()
	// A full list makes room by dropping entries the app no longer shows.
	saved, err := server.config.Store.PutComputer(ctx, principal.DeviceID, request.Origin, node.ID, request.Label,
		store.ComputerState(request.State), now, now.Add(PendingComputerLifetime), protocol.MaxComputers,
		func(computer store.Computer) bool { return hiddenComputer(status, computer) })
	if err != nil {
		return protocol.HTTPComputerAddResponse{}, err
	}
	return protocol.HTTPComputerAddResponse{
		Computer: protocol.HTTPComputer{Origin: saved.Origin, Label: saved.Label},
		Node:     node.Wire(), State: protocol.HTTPComputerState(saved.State),
	}, nil
}

// sweepTrust closes open streams whose app origin is no longer trusted and
// revokes the devices bound to an origin whose node changed. It runs off the
// request path; it is the only place that turns a node change into a durable
// revocation, once per new snapshot. Only devices that can still connect are
// read: a device without a usable credential has nothing to revoke.
func (server *ManagementServer) sweepTrust(ctx context.Context) {
	authority := server.config.Auth
	if authority == nil {
		return
	}
	server.trustMu.Lock()
	defer server.trustMu.Unlock()
	devices, err := server.config.Store.ListBoundDevices(ctx, server.now(), auth.BrowserInactivityLifetime, true)
	if err != nil || len(devices) == 0 && !authority.HasAppStreams() {
		return
	}
	status := server.config.Tailnet.Snapshot()
	if status != nil && status != server.sweptStatus {
		for _, device := range devices {
			if trust(status, device.App) == tailnet.Changed {
				_, _ = authority.RevokeDevice(ctx, device.DeviceID)
			}
		}
		server.sweptStatus = status
	}
	authority.CloseUntrusted(func(app store.AppBinding) bool {
		return status != nil && trust(status, app) == tailnet.Trusted
	})
}

// TrustUpdated is the monitor's OnUpdate: a new snapshot wakes the sweep at
// once instead of at the next tick. It never blocks the refresh.
func (server *ManagementServer) TrustUpdated(*tailnet.Status) {
	select {
	case server.trustWake <- struct{}{}:
	default:
	}
}

// watchTrust sweeps on every new snapshot and every trustSweepInterval (the
// tick also starts refreshes and catches the end of the grace period).
func (server *ManagementServer) watchTrust(ctx context.Context, stop <-chan struct{}) {
	ticker := time.NewTicker(trustSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
		case <-server.trustWake:
		}
		server.sweepTrust(ctx)
	}
}

func wireNode(app store.AppBinding) (*string, *protocol.TailnetNode) {
	if !app.Bound() {
		return nil, nil
	}
	origin := app.Origin
	node := tailnet.Node{ID: app.NodeID, Owner: app.NodeOwner, Tags: app.NodeTags}.Wire()
	return &origin, &node
}

func classifyComputerError(err error) (managementFailure, bool) {
	switch {
	case errors.Is(err, auth.ErrOriginRefused):
		return managementFailure{protocol.InvalidRequest, "this app is not allowed to use this computer"}, true
	case errors.Is(err, errTrustUnavailable):
		return managementFailure{protocol.HostUnavailable, "this computer cannot read its Tailscale status; check Tailscale and retry"}, true
	case errors.Is(err, tailnet.ErrNotFound):
		return managementFailure{protocol.InvalidRequest, "no computer with that name is in this tailnet"}, true
	case errors.Is(err, tailnet.ErrAmbiguous):
		return managementFailure{protocol.InvalidRequest, "more than one computer has that name; use the full https origin"}, true
	case errors.Is(err, tailnet.ErrOtherOwner):
		return managementFailure{protocol.InvalidRequest, "that computer belongs to another Tailscale user"}, true
	case errors.Is(err, tailnet.ErrTagged):
		return managementFailure{protocol.InvalidRequest, "that computer is a tagged Tailscale node; check its tags, then use --allow-tagged"}, true
	case errors.Is(err, errComputerSelf):
		return managementFailure{protocol.InvalidRequest, "this is the computer that serves the app"}, true
	case errors.Is(err, store.ErrComputerMissing):
		return managementFailure{protocol.InvalidRequest, "add the computer before confirming it"}, true
	case errors.Is(err, store.ErrComputerNode):
		return managementFailure{protocol.InvalidRequest, "this name now belongs to another Tailscale machine; add the computer again"}, true
	case errors.Is(err, store.ErrComputerCapacity):
		return managementFailure{protocol.LimitExceeded, "this phone already has the most computers allowed"}, true
	}
	return managementFailure{}, false
}
