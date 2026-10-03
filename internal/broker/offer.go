package broker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"syscall"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

// Found on your network, the new computer's side (docs/found-on-network.md,
// N1 and N2). While `pterm pair --via <home>` waits, the CLI calls
// admin.pair.offer every 30 s. The broker keeps one live grant bound to home
// and pushes it to home, so home can list this computer under "Found on your
// network". The CLI never sees the offered grant.

const (
	// offerRenewAt: a grant with this much time or less left is replaced, so
	// home (which keeps an offer 60 s) never lists a grant that expires first.
	offerRenewAt = 60 * time.Second
	// offerTimeout bounds one push: dial, TLS, request and answer.
	offerTimeout = 3 * time.Second
	// maxOfferAnswer is how much of home's answer is read before it is
	// dropped. Only the status code is used; home's text is never shown.
	maxOfferAnswer = 2 << 10
	offerPath      = "/api/v1/computers/offer"
)

var (
	errHomeChanged   = errors.New("broker: the app's computer is now a different Tailscale machine")
	errNoOfferOrigin = errors.New("broker: this computer has no tailnet https origin to offer")
	errOfferDial     = errors.New("broker: an offer dials only the pinned Tailscale IP on port 443")
)

// offerState is the one live offer: the home named by via, its pinned node,
// and the grant offered to it.
type offerState struct {
	via     string
	app     store.AppBinding
	node    tailnet.Node
	grant   string
	expires time.Time
}

type offerBook struct {
	mu    sync.Mutex // held for a whole offer, push included
	state *offerState
}

// offerBooks holds each server's offer state.
// ponytail: keyed by server here because ManagementServer's struct belongs to
// another track's file (rpc_server.go); make it a field when both merge.
var offerBooks sync.Map // *ManagementServer -> *offerBook

func (server *ManagementServer) offers() *offerBook {
	book, _ := offerBooks.LoadOrStore(server, &offerBook{})
	return book.(*offerBook)
}

// offerPairing answers admin.pair.offer (N1).
func (server *ManagementServer) offerPairing(ctx context.Context, authority *auth.Service, via string) (
	protocol.AdminRPCPairOfferResultPayload, error,
) {
	book := server.offers()
	book.mu.Lock()
	defer book.mu.Unlock()
	origin, err := server.pairingOrigin(ctx)
	if err != nil {
		return protocol.AdminRPCPairOfferResultPayload{}, err
	}
	if origin == nil || protocol.ValidateComputerOrigin(*origin) != nil {
		return protocol.AdminRPCPairOfferResultPayload{}, errNoOfferOrigin
	}
	// Another home replaces the offer; its grant goes.
	if book.state != nil && book.state.via != via {
		if err := server.dropOffer(ctx, authority, book); err != nil {
			return protocol.AdminRPCPairOfferResultPayload{}, err
		}
	}
	state := book.state
	if state == nil {
		// Never allowTagged: home drops every offer when either node is tagged.
		app, node, err := server.resolveVia(ctx, via, false)
		if err != nil {
			return protocol.AdminRPCPairOfferResultPayload{}, err
		}
		grant, expires, err := authority.CreateBoundGrant(ctx, app)
		if err != nil {
			return protocol.AdminRPCPairOfferResultPayload{}, err
		}
		state = &offerState{via: via, app: app, node: node, grant: grant, expires: expires}
		book.state = state
	} else {
		status := server.config.Tailnet.Snapshot()
		if status == nil {
			return offerResult(state, unreachable()), nil
		}
		if node, ok := status.Peer(state.node.Host); !ok || node.ID != state.node.ID {
			return protocol.AdminRPCPairOfferResultPayload{}, errors.Join(errHomeChanged, server.dropOffer(ctx, authority, book))
		}
		if state.expires.Sub(server.now()) <= offerRenewAt {
			if err := authority.DeleteGrant(ctx, state.grant); err != nil {
				return protocol.AdminRPCPairOfferResultPayload{}, err
			}
			grant, expires, err := authority.CreateBoundGrant(ctx, state.app)
			if err != nil {
				book.state = nil
				return protocol.AdminRPCPairOfferResultPayload{}, err
			}
			state.grant, state.expires = grant, expires
		}
	}
	return offerResult(state, pushOffer(ctx, state.node, *origin, state.grant)), nil
}

// endOffer answers admin.pair.offer.end: it deletes the offered grant and
// the link's grant, each only when it was not redeemed.
func (server *ManagementServer) endOffer(ctx context.Context, authority *auth.Service, link *string) error {
	book := server.offers()
	book.mu.Lock()
	defer book.mu.Unlock()
	err := server.dropOffer(ctx, authority, book)
	if link != nil {
		err = errors.Join(err, authority.DeleteGrant(ctx, *link))
	}
	return err
}

// dropOffer deletes the offered grant (unless redeemed) and clears the state.
func (server *ManagementServer) dropOffer(ctx context.Context, authority *auth.Service, book *offerBook) error {
	if book.state == nil {
		return nil
	}
	grant := book.state.grant
	book.state = nil
	return authority.DeleteGrant(ctx, grant)
}

func offerResult(state *offerState, problem *protocol.AdminRPCPairOfferProblem) protocol.AdminRPCPairOfferResultPayload {
	return protocol.AdminRPCPairOfferResultPayload{
		AppOrigin: state.app.Origin, AppNode: state.node.Wire(), ExpiresAt: formatRPCTime(state.expires),
		Delivered: problem == nil, Problem: problem,
	}
}

func unreachable() *protocol.AdminRPCPairOfferProblem {
	problem := protocol.Unreachable
	return &problem
}

// pushOffer posts {origin, grant} to home (N2). nil means home answered 204.
// No network answer is unreachable; any other answer, a redirect included,
// is not_accepted. Home's body is read up to maxOfferAnswer and dropped.
func pushOffer(ctx context.Context, home tailnet.Node, origin, grant string) *protocol.AdminRPCPairOfferProblem {
	if len(home.TailscaleIPs) == 0 {
		return unreachable()
	}
	address := home.TailscaleIPs[0]
	body, err := json.Marshal(protocol.HTTPComputerOfferRequest{Origin: origin, Grant: grant})
	if err != nil {
		return unreachable()
	}
	ctx, cancel := context.WithTimeout(ctx, offerTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+home.Host+offerPath, bytes.NewReader(body))
	if err != nil {
		return unreachable()
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Pterm-Request", "1")
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: nil,
			// Every connection is TLS to the pinned address; plain http never dials.
			DialContext: func(context.Context, string, string) (net.Conn, error) { return nil, errOfferDial },
			DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialHome(ctx, address, home.Host)
			},
			DisableKeepAlives:      true,
			DisableCompression:     true,
			MaxResponseHeaderBytes: 16 << 10,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       offerTimeout,
	}
	response, err := client.Do(request)
	if err != nil {
		return unreachable()
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxOfferAnswer))
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		problem := protocol.NotAccepted
		return &problem
	}
	return nil
}

// dialHome opens the TLS connection of one offer push to home's pinned
// Tailscale IP on port 443, checked against home's certificate name with the
// system roots. This is the production dial; only an e2e build replaces it
// (peerdial_e2e.go).
var dialHome = func(ctx context.Context, address netip.Addr, host string) (net.Conn, error) {
	return dialPinned(ctx, netip.AddrPortFrom(address, 443), host, nil)
}

// dialPinned connects to target only (the dialer's Control refuses any other
// address) and runs TLS 1.2 or later for host. nil roots are the system's.
func dialPinned(ctx context.Context, target netip.AddrPort, host string, roots *x509.CertPool) (net.Conn, error) {
	dialer := &net.Dialer{Control: pinnedControl(target)}
	raw, err := dialer.DialContext(ctx, "tcp", target.String())
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12, RootCAs: roots})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return conn, nil
}

// pinnedControl allows a connection to target only.
func pinnedControl(target netip.AddrPort) func(string, string, syscall.RawConn) error {
	return func(_, address string, _ syscall.RawConn) error {
		got, err := netip.ParseAddrPort(address)
		if err != nil || got.Addr().Unmap() != target.Addr() || got.Port() != target.Port() {
			return errOfferDial
		}
		return nil
	}
}
