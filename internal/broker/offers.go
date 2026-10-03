package broker

import (
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

// Found on your network, home side (docs/found-on-network.md, H2 and H3). A
// new computer that waits in `pterm pair --via <this computer>` posts its own
// bound grant here; the app lists it under Found on your network.

const (
	// MaxOffers bounds the offer book: at most this many origins.
	MaxOffers = protocol.MaxFoundComputers
	// OfferLifetime is how long home lists one offer. The new computer sends
	// every 30 s and replaces its grant at 65 s left (60 s plus its 3 s push
	// limit, rounded up), deleting the replaced grant only after home
	// accepted the new one, so a listed grant is valid for at least as long
	// as it is listed.
	OfferLifetime = 60 * time.Second
)

// offer is one kept offer. The grant is a secret: it is never logged, never
// stored on disk, and leaves the broker only in a found list.
type offer struct {
	grant   string
	nodeID  string
	expires time.Time
}

// offerBook holds offers in memory only, by origin. A broker restart empties
// it; a waiting computer sends again within 30 s.
type offerBook struct {
	mu     sync.Mutex
	offers map[string]offer
}

// put keeps request only when status shows its origin as a peer of this
// computer's own tailnet (never home itself: ParseStatus leaves Self out of the
// peers, and own is refused too) with home's owner and neither node tagged.
// Anything else is dropped without a trace: the sender always gets the same
// answer. The newest offer for an origin replaces the older one; an offer for
// a new origin when MaxOffers unexpired origins are kept is dropped.
func (book *offerBook) put(status *tailnet.Status, own *string, request protocol.HTTPComputerOfferRequest, now time.Time) {
	if status == nil || own != nil && *own == request.Origin {
		return
	}
	node, ok := status.Peer(strings.TrimPrefix(request.Origin, "https://"))
	if !ok || status.CheckOwner(node, false) != nil {
		return
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	book.dropExpired(now)
	if _, replaces := book.offers[request.Origin]; !replaces && len(book.offers) >= MaxOffers {
		return
	}
	if book.offers == nil {
		book.offers = map[string]offer{}
	}
	book.offers[request.Origin] = offer{grant: request.Grant, nodeID: node.ID, expires: now.Add(OfferLifetime)}
}

// found lists the unexpired offers whose host status still shows as the same
// node under the owner rule, leaving out the origins in skip, sorted by origin.
// The label is the host's first DNS label from status, never from the sender.
func (book *offerBook) found(status *tailnet.Status, skip map[string]bool, now time.Time) []protocol.HTTPFoundComputer {
	book.mu.Lock()
	defer book.mu.Unlock()
	book.dropExpired(now)
	list := []protocol.HTTPFoundComputer{}
	for origin, kept := range book.offers {
		node, ok := status.Peer(strings.TrimPrefix(origin, "https://"))
		if skip[origin] || !ok || node.ID != kept.nodeID || status.CheckOwner(node, false) != nil {
			continue
		}
		label, _, _ := strings.Cut(node.Host, ".")
		list = append(list, protocol.HTTPFoundComputer{
			Origin: origin, Label: label, Grant: kept.grant, ExpiresAt: formatRPCTime(kept.expires),
		})
	}
	slices.SortFunc(list, func(first, second protocol.HTTPFoundComputer) int {
		return strings.Compare(first.Origin, second.Origin)
	})
	return list
}

func (book *offerBook) dropExpired(now time.Time) {
	for origin, kept := range book.offers {
		if !now.Before(kept.expires) {
			delete(book.offers, origin)
		}
	}
}

// offerComputer answers gateway.computers.offer. It reads only the latest
// snapshot: an unauthenticated request never waits for a `tailscale status`
// run. The gateway never forwards an origin on this route, so one that
// carries an origin is dropped.
func (server *ManagementServer) offerComputer(origin *string, request protocol.HTTPComputerOfferRequest) {
	if origin != nil {
		return
	}
	server.offers.put(server.config.Tailnet.Snapshot(), server.ownOrigin.Load(), request, server.now())
}

// foundComputers answers gateway.computers.found for an own-origin device:
// the offers, minus the computers already confirmed for that device.
func (server *ManagementServer) foundComputers(ctx context.Context, principal auth.Principal) (protocol.HTTPComputerFoundResponse, error) {
	status := server.config.Tailnet.Snapshot()
	if status == nil {
		return protocol.HTTPComputerFoundResponse{}, errTrustUnavailable
	}
	now := server.now()
	computers, err := server.config.Store.ListComputers(ctx, principal.DeviceID, now)
	if err != nil {
		return protocol.HTTPComputerFoundResponse{}, err
	}
	confirmed := map[string]bool{}
	for _, computer := range computers {
		if computer.State == store.ComputerConfirmed {
			confirmed[computer.Origin] = true
		}
	}
	return protocol.HTTPComputerFoundResponse{Computers: server.offers.found(status, confirmed, now)}, nil
}
