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
// The configured origin and the gateway's development loopback origins are
// this computer's own as well; a bound app origin is always https.
func (server *ManagementServer) isOwnOrigin(origin *string) bool {
	if origin == nil {
		return true
	}
	if own := server.ownOrigin.Load(); own != nil && *own == *origin {
		return true
	}
	return strings.HasPrefix(*origin, "http://127.0.0.1:") || strings.HasPrefix(*origin, "http://localhost:")
}

// appTrust reads the latest snapshot only. A definite node change is a
// revocation; a missing name or another owner refuses without revoking.
func (server *ManagementServer) appTrust(origin, nodeID string) error {
	status := server.config.Tailnet.Snapshot()
	if status == nil {
		return errTrustUnavailable
	}
	switch status.Trust(origin, nodeID) {
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
	return server.appTrust(*origin, principal.App.NodeID)
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
		if err := server.appTrust(app.Origin, app.NodeID); errors.Is(err, auth.ErrRevoked) {
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

// originProbe answers gateway.origin. boundOrigins are the app origins of
// live devices that the latest snapshot still trusts; pairingOrigins those of
// unexpired grants and pending pairings. The gateway uses them only for CORS.
func (server *ManagementServer) originProbe(ctx context.Context) (protocol.GatewayRPCOriginResultPayload, error) {
	origin, err := server.pairingOrigin(ctx)
	if err != nil {
		return protocol.GatewayRPCOriginResultPayload{}, err
	}
	result := protocol.GatewayRPCOriginResultPayload{Origin: origin}
	if server.config.Auth == nil {
		return result, nil
	}
	now := server.now()
	devices, err := server.config.Store.ListBoundDevices(ctx, now, auth.BrowserInactivityLifetime, true)
	if err != nil {
		return protocol.GatewayRPCOriginResultPayload{}, err
	}
	if len(devices) > 0 {
		if status := server.config.Tailnet.Snapshot(); status != nil {
			for _, device := range devices {
				if status.Trust(device.Origin, device.NodeID) == tailnet.Trusted &&
					!slices.Contains(result.BoundOrigins, device.Origin) && len(result.BoundOrigins) < protocol.MaxBoundOrigins {
					result.BoundOrigins = append(result.BoundOrigins, device.Origin)
				}
			}
		}
	}
	if result.PairingOrigins, err = server.config.Store.PairingOrigins(ctx, now, protocol.MaxPairingOrigins); err != nil {
		return protocol.GatewayRPCOriginResultPayload{}, err
	}
	return result, nil
}

// resolveVia resolves pterm pair --via in a fresh snapshot, applies the owner
// rule and pins the node.
func (server *ManagementServer) resolveVia(ctx context.Context, via string, allowTagged bool) (store.AppBinding, error) {
	status, err := server.config.Tailnet.Fresh(ctx)
	if err != nil {
		return store.AppBinding{}, errors.Join(errTrustUnavailable, err)
	}
	node, err := status.Resolve(via)
	if err != nil {
		return store.AppBinding{}, err
	}
	if err := status.CheckOwner(node, allowTagged); err != nil {
		return store.AppBinding{}, err
	}
	return store.AppBinding{Origin: node.Origin(), NodeID: node.ID, NodeOwner: node.Owner, NodeTags: node.Tags}, nil
}

// lookupComputer is home's check before it lists a computer: the host must be
// a node of its own tailnet, owned by home's owner or tagged.
func (server *ManagementServer) lookupComputer(ctx context.Context, origin string) (tailnet.Node, error) {
	if own := server.ownOrigin.Load(); own != nil && *own == origin {
		return tailnet.Node{}, errComputerSelf
	}
	status, err := server.config.Tailnet.Fresh(ctx)
	if err != nil {
		return tailnet.Node{}, errors.Join(errTrustUnavailable, err)
	}
	node, ok := status.Peer(strings.TrimPrefix(origin, "https://"))
	if !ok {
		return tailnet.Node{}, tailnet.ErrNotFound
	}
	return node, status.CheckOwner(node, true)
}

func (server *ManagementServer) listComputers(ctx context.Context, principal auth.Principal) (protocol.GatewayRPCComputerListResultPayload, error) {
	computers, err := server.config.Store.ListComputers(ctx, principal.DeviceID, server.now())
	if err != nil {
		return protocol.GatewayRPCComputerListResultPayload{}, err
	}
	result := protocol.GatewayRPCComputerListResultPayload{Computers: []protocol.HTTPComputer{}, PendingOrigins: []string{}}
	for _, computer := range computers {
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
	node, err := server.lookupComputer(ctx, request.Origin)
	if err != nil {
		return protocol.HTTPComputerAddResponse{}, err
	}
	now := server.now()
	saved, err := server.config.Store.PutComputer(ctx, principal.DeviceID, request.Origin, request.Label,
		store.ComputerState(request.State), now, now.Add(PendingComputerLifetime), protocol.MaxComputers)
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
// revocation, once per new snapshot.
func (server *ManagementServer) sweepTrust(ctx context.Context) {
	authority := server.config.Auth
	if authority == nil {
		return
	}
	server.trustMu.Lock()
	defer server.trustMu.Unlock()
	devices, err := server.config.Store.ListBoundDevices(ctx, server.now(), auth.BrowserInactivityLifetime, false)
	if err != nil || len(devices) == 0 && !authority.HasAppStreams() {
		return
	}
	status := server.config.Tailnet.Snapshot()
	if status != nil && status != server.sweptStatus {
		for _, device := range devices {
			if status.Trust(device.Origin, device.NodeID) == tailnet.Changed {
				_, _ = authority.RevokeDevice(ctx, device.DeviceID)
			}
		}
		server.sweptStatus = status
	}
	authority.CloseUntrusted(func(origin, nodeID string) bool {
		return status != nil && status.Trust(origin, nodeID) == tailnet.Trusted
	})
}

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
			server.sweepTrust(ctx)
		}
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
	case errors.Is(err, store.ErrComputerCapacity):
		return managementFailure{protocol.LimitExceeded, "this phone already has the most computers allowed"}, true
	}
	return managementFailure{}, false
}
