package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

var (
	offerGrantA = strings.Repeat("a", 42) + "_"
	offerGrantB = strings.Repeat("b", 42) + "-"
)

// snapshotOf parses the fake's status (vps, owned by ada) as home's snapshot;
// selfTagged tags home itself.
func snapshotOf(t *testing.T, fake *fakeTailscale, selfTagged bool) *tailnet.Status {
	t.Helper()
	data, err := fake.read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if selfTagged {
		var raw map[string]any
		_ = json.Unmarshal(data, &raw)
		raw["Self"].(map[string]any)["Tags"] = []string{"tag:server"}
		data, _ = json.Marshal(raw)
	}
	status, err := tailnet.ParseStatus(data)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func offerFor(origin, grant string) protocol.HTTPComputerOfferRequest {
	return protocol.HTTPComputerOfferRequest{Origin: origin, Grant: grant}
}

func foundOrigins(list []protocol.HTTPFoundComputer) []string {
	origins := []string{}
	for _, computer := range list {
		origins = append(origins, computer.Origin)
	}
	return origins
}

func TestOfferBookKeepsOnlyUntaggedPeersOfTheOwner(t *testing.T) {
	fake := &fakeTailscale{peers: map[string]string{"mac": "nMac", "ci": "nCi", "pi": "nPi"}}
	status := snapshotOf(t, fake, false)
	own := homeOrigin
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	var book offerBook
	if got := book.found(status, nil, now); got == nil || len(got) != 0 {
		t.Fatalf("a new book (a restart) = %#v", got)
	}
	for _, origin := range []string{
		piOrigin,                    // another owner
		ciOrigin,                    // a tagged node
		"https://gone.tailx.ts.net", // not in the snapshot
		"https://mac.taily.ts.net",  // another tailnet
		homeOrigin,                  // home itself
	} {
		book.put(status, &own, offerFor(origin, offerGrantA), now)
	}
	book.put(nil, &own, offerFor(macOrigin, offerGrantA), now) // no snapshot
	book.put(status, &own, offerFor(macOrigin, offerGrantA), now)
	want := []protocol.HTTPFoundComputer{{Origin: macOrigin, Label: "mac", Grant: offerGrantA, ExpiresAt: "2026-10-03T10:01:00Z"}}
	if got := book.found(status, nil, now); !reflect.DeepEqual(got, want) {
		t.Fatalf("found = %#v", got)
	}
	// Home's own origin is refused even when the snapshot lists it as a peer.
	macOwn := macOrigin
	var other offerBook
	other.put(status, &macOwn, offerFor(macOrigin, offerGrantA), now)
	if got := other.found(status, nil, now); len(got) != 0 {
		t.Fatalf("own origin kept: %#v", got)
	}
	// A tagged home keeps nothing (CheckOwner without allowTagged).
	other.put(snapshotOf(t, fake, true), &own, offerFor(macOrigin, offerGrantA), now)
	if got := other.found(status, nil, now); len(got) != 0 {
		t.Fatalf("tagged home kept an offer: %#v", got)
	}
}

func TestOfferBookExpiryNewestAndCap(t *testing.T) {
	fake := &fakeTailscale{peers: map[string]string{"mac": "nMac"}}
	for index := range MaxOffers + 1 {
		fake.peers[fmt.Sprintf("pc%02d", index)] = fmt.Sprintf("nPc%02d", index)
	}
	status := snapshotOf(t, fake, false)
	own := homeOrigin
	start := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	var book offerBook
	book.put(status, &own, offerFor(macOrigin, offerGrantA), start)
	// The newest offer for an origin wins, with a new expiry.
	later := start.Add(30 * time.Second)
	book.put(status, &own, offerFor(macOrigin, offerGrantB), later)
	if got := book.found(status, nil, later); len(got) != 1 || got[0].Grant != offerGrantB || got[0].ExpiresAt != "2026-10-03T10:01:30Z" {
		t.Fatalf("newest = %#v", got)
	}
	if got := book.found(status, nil, later.Add(OfferLifetime-time.Millisecond)); len(got) != 1 {
		t.Fatalf("dropped before 60 s: %#v", got)
	}
	if got := book.found(status, nil, later.Add(OfferLifetime)); len(got) != 0 {
		t.Fatalf("kept after 60 s: %#v", got)
	}
	// At most 16 origins; the 17th is dropped, a replacement still lands.
	for index := range MaxOffers + 1 {
		book.put(status, &own, offerFor(fmt.Sprintf("https://pc%02d.tailx.ts.net", index), offerGrantA), start)
	}
	book.put(status, &own, offerFor("https://pc00.tailx.ts.net", offerGrantB), start)
	got := book.found(status, nil, start)
	if len(got) != MaxOffers || got[0].Grant != offerGrantB || got[MaxOffers-1].Origin != "https://pc15.tailx.ts.net" {
		t.Fatalf("cap = %v", foundOrigins(got))
	}
	if err := protocol.ValidateFoundComputers(protocol.HTTPComputerFoundResponse{Computers: got}); err != nil {
		t.Fatalf("a full list is outside the protocol: %v", err)
	}
	// Expired entries make room.
	next := start.Add(OfferLifetime)
	book.put(status, &own, offerFor("https://pc16.tailx.ts.net", offerGrantA), next)
	if got := book.found(status, nil, next); !reflect.DeepEqual(foundOrigins(got), []string{"https://pc16.tailx.ts.net"}) {
		t.Fatalf("after expiry = %v", foundOrigins(got))
	}
}

func TestOfferBookHidesChangedNodesAndSkippedOrigins(t *testing.T) {
	fake := &fakeTailscale{peers: map[string]string{"mac": "nMac", "pc0": "nPc0"}, tagged: map[string]bool{}}
	own := homeOrigin
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	var book offerBook
	book.put(snapshotOf(t, fake, false), &own, offerFor(macOrigin, offerGrantA), now)
	book.put(snapshotOf(t, fake, false), &own, offerFor("https://pc0.tailx.ts.net", offerGrantA), now)
	if got := book.found(snapshotOf(t, fake, false), map[string]bool{macOrigin: true}, now); !reflect.DeepEqual(foundOrigins(got), []string{"https://pc0.tailx.ts.net"}) {
		t.Fatalf("skipped origin listed: %v", foundOrigins(got))
	}
	for name, change := range map[string]func(){
		"another node": func() { fake.peers["mac"] = "nOtherMac" },
		"gone":         func() { fake.peers["mac"] = "" },
		"tagged":       func() { fake.tagged["mac"] = true },
	} {
		change()
		if got := book.found(snapshotOf(t, fake, false), nil, now); !reflect.DeepEqual(foundOrigins(got), []string{"https://pc0.tailx.ts.net"}) {
			t.Errorf("%s: listed %v", name, foundOrigins(got))
		}
		fake.peers["mac"], fake.tagged["mac"] = "nMac", false
	}
	if got := book.found(snapshotOf(t, fake, false), nil, now); len(got) != 2 {
		t.Fatalf("the pinned node is back: %v", foundOrigins(got))
	}
}

// Through gateway RPC: an offer never runs tailscale, is answered the same
// whether kept or dropped, and found is own origin only and leaves out the
// device's confirmed computers.
func TestOfferAndFoundRPC(t *testing.T) {
	fixture := newTrustFixture(t)
	var reads atomic.Int32
	monitor := tailnet.NewMonitor(tailnet.Config{Now: fixture.clock.Now, Source: func(ctx context.Context) ([]byte, error) {
		reads.Add(1)
		return fixture.tailscale.read(ctx)
	}})
	if _, err := monitor.Fresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixture.server.config.Tailnet = monitor
	reads.Store(0)
	phone := pairDevice(t, fixture.service, "Phone")
	viaMac := fixture.pairVia(t, "mac", macOrigin, false)
	offer := func(origin, sender, grant string) {
		t.Helper()
		if response := fixture.from(t, origin, protocol.GatewayComputersOffer, offerFor(sender, grant)); response.Type != protocol.GatewayComputersOfferResult ||
			string(response.Payload) != "{}" {
			t.Fatalf("offer %s from %q = %s %s", sender, origin, response.Type, response.Payload)
		}
	}
	found := func(credential string) []protocol.HTTPFoundComputer {
		t.Helper()
		result, err := protocol.DecodeRPCPayload[protocol.HTTPComputerFoundResponse](
			fixture.from(t, "", protocol.GatewayComputersFound, credentialPayload(credential)))
		if err != nil {
			t.Fatal(err)
		}
		return result.Computers
	}
	for _, sender := range []string{piOrigin, ciOrigin, homeOrigin, "https://gone.tailx.ts.net"} {
		offer("", sender, offerGrantA)
	}
	offer(macOrigin, "https://pc0.tailx.ts.net", offerGrantA) // a forwarded origin: dropped
	offer("", macOrigin, offerGrantB)
	offer("", "https://pc1.tailx.ts.net", offerGrantA)
	if got := found(phone.Credential); !reflect.DeepEqual(foundOrigins(got), []string{macOrigin, "https://pc1.tailx.ts.net"}) ||
		got[0].Label != "mac" || got[0].Grant != offerGrantB {
		t.Fatalf("found = %#v", got)
	}
	if reads.Load() != 0 {
		t.Fatalf("offers or found ran tailscale %d times", reads.Load())
	}
	// A confirmed computer leaves this device's list only; a pending one stays.
	for _, state := range []protocol.HTTPComputerState{protocol.HTTPComputerStatePending, protocol.Confirmed} {
		if response := fixture.from(t, "", protocol.GatewayComputersAdd, protocol.GatewayRPCComputerAddPayload{
			Credential: phone.Credential, Request: protocol.HTTPComputerAddRequest{Origin: macOrigin, Label: "mac", State: state},
		}); response.Type != protocol.GatewayComputersAddResult {
			t.Fatalf("add %s = %s", state, response.Payload)
		}
		want := []string{"https://pc1.tailx.ts.net"}
		if state == protocol.HTTPComputerStatePending {
			want = []string{macOrigin, "https://pc1.tailx.ts.net"}
		}
		if got := found(phone.Credential); !reflect.DeepEqual(foundOrigins(got), want) {
			t.Fatalf("after %s: %v", state, foundOrigins(got))
		}
	}
	other := pairDevice(t, fixture.service, "Tablet")
	if got := found(other.Credential); len(got) != 2 {
		t.Fatalf("another device = %v", foundOrigins(got))
	}
	// Only from this computer's own origin, and only with a credential.
	if code := fixture.code(t, macOrigin, protocol.GatewayComputersFound, credentialPayload(viaMac.Credential)); code != protocol.InvalidRequest {
		t.Fatalf("found from a bound app origin = %q", code)
	}
	if code := fixture.code(t, "", protocol.GatewayComputersFound, credentialPayload(strings.Repeat("x", 43))); code != protocol.Unauthorized {
		t.Fatalf("found without a valid credential = %q", code)
	}
	// Without a snapshot: found is host_unavailable, an offer is still {}.
	fixture.server.config.Tailnet = tailnet.NewMonitor(tailnet.Config{Now: fixture.clock.Now,
		Source: func(context.Context) ([]byte, error) { return nil, errors.New("no tailscale") }})
	offer("", "https://pc2.tailx.ts.net", offerGrantA)
	if code := fixture.code(t, "", protocol.GatewayComputersFound, credentialPayload(phone.Credential)); code != protocol.HostUnavailable {
		t.Fatalf("found without a snapshot = %q", code)
	}
}
