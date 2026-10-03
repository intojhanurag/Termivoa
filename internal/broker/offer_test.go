package broker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/tailnet"
)

// offerIP is every fake peer's Tailscale IP (fakeTailscale).
const offerIP = "100.100.0.1"

// offerHome is an https server that stands in for home at offerIP:443: the
// dial hook records where the production code dials, then connects to the
// server with the production TLS code and a test root.
type offerHome struct {
	server *httptest.Server
	mu     sync.Mutex
	dials  []string // "ip host" per dial
	posts  []*http.Request
	bodies []protocol.HTTPComputerOfferRequest
}

func newOfferHome(t *testing.T, names []string, handler http.HandlerFunc) *offerHome {
	t.Helper()
	home := &offerHome{}
	record := func(response http.ResponseWriter, request *http.Request) {
		var body protocol.HTTPComputerOfferRequest
		_ = json.NewDecoder(io.LimitReader(request.Body, 4096)).Decode(&body)
		home.mu.Lock()
		home.posts, home.bodies = append(home.posts, request), append(home.bodies, body)
		home.mu.Unlock()
		if handler != nil {
			handler(response, request)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}
	home.server = httptest.NewUnstartedServer(http.HandlerFunc(record))
	certificate, roots := testCertificate(t, names)
	home.server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}}
	home.server.StartTLS()
	t.Cleanup(home.server.Close)
	target := netip.MustParseAddrPort(home.server.Listener.Addr().String())
	production := dialHome
	dialHome = func(ctx context.Context, address netip.Addr, host string) (net.Conn, error) {
		home.mu.Lock()
		home.dials = append(home.dials, address.String()+" "+host)
		home.mu.Unlock()
		return dialPinned(ctx, target, host, roots)
	}
	t.Cleanup(func() { dialHome = production })
	return home
}

func (home *offerHome) offered() []protocol.HTTPComputerOfferRequest {
	home.mu.Lock()
	defer home.mu.Unlock()
	return append([]protocol.HTTPComputerOfferRequest{}, home.bodies...)
}

func testCertificate(t *testing.T, names []string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}

var macNode = tailnet.Node{ID: "nMac", Host: "mac.tailx.ts.net", TailscaleIPs: []netip.Addr{netip.MustParseAddr(offerIP)}}

func problemOf(problem *protocol.AdminRPCPairOfferProblem) string {
	if problem == nil {
		return "delivered"
	}
	return string(*problem)
}

// The push dials the node's Tailscale IP, never the host name, through no
// proxy, and sends only {origin, grant} with the CSRF header: no Origin, no
// Sec-Fetch-*, no cookie.
func TestOfferPushDialsThePinnedIPWithTLSName(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9") // Must be ignored.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	home := newOfferHome(t, []string{"mac.tailx.ts.net"}, nil)
	if got := problemOf(pushOffer(context.Background(), macNode, homeOrigin, testOfferGrant)); got != "delivered" {
		t.Fatalf("push = %s", got)
	}
	if len(home.dials) != 1 || home.dials[0] != offerIP+" mac.tailx.ts.net" {
		t.Fatalf("dials = %v", home.dials)
	}
	request := home.posts[0]
	if request.Method != http.MethodPost || request.URL.Path != offerPath || request.Host != "mac.tailx.ts.net" ||
		request.TLS == nil || request.TLS.ServerName != "mac.tailx.ts.net" || request.TLS.Version < tls.VersionTLS12 ||
		request.Header.Get("X-Pterm-Request") != "1" || request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("request = %s %s host=%s headers=%v", request.Method, request.URL, request.Host, request.Header)
	}
	for name := range request.Header {
		if name == "Origin" || name == "Cookie" || name == "Referer" || strings.HasPrefix(name, "Sec-Fetch-") {
			t.Fatalf("request has %s: %v", name, request.Header)
		}
	}
	if body := home.offered()[0]; body.Origin != homeOrigin || body.Grant != testOfferGrant {
		t.Fatalf("body = %+v", body)
	}
}

// The dialer's Control refuses every address but the pinned IP and port,
// also in a real dial.
func TestOfferDialGuardAllowsOnlyThePinnedAddress(t *testing.T) {
	pinned := netip.MustParseAddrPort(offerIP + ":443")
	control := pinnedControl(pinned)
	if err := control("tcp4", offerIP+":443", nil); err != nil {
		t.Fatalf("pinned address refused: %v", err)
	}
	for _, address := range []string{"100.100.0.2:443", offerIP + ":80", offerIP + ":8443", "[::1]:443", "127.0.0.1:443", "mac.tailx.ts.net:443", ""} {
		if err := control("tcp", address, nil); !errors.Is(err, errOfferDial) {
			t.Fatalf("%q allowed: %v", address, err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	dialer := &net.Dialer{Control: pinnedControl(netip.MustParseAddrPort("127.0.0.1:443"))}
	if conn, err := dialer.Dial("tcp", listener.Addr().String()); err == nil || !errors.Is(err, errOfferDial) {
		if conn != nil {
			conn.Close()
		}
		t.Fatalf("dial to another port = %v", err)
	}
	// A node without a Tailscale IP is never dialed.
	home := newOfferHome(t, []string{"mac.tailx.ts.net"}, nil)
	if got := problemOf(pushOffer(context.Background(), tailnet.Node{ID: "nMac", Host: "mac.tailx.ts.net"}, homeOrigin, testOfferGrant)); got != "unreachable" || len(home.dials) != 0 {
		t.Fatalf("no address: %s after %d dials", got, len(home.dials))
	}
}

// A certificate for another name fails, with no retry over http; a redirect
// is an answer (not followed); 429 is busy; any other answer but 204 is
// not_accepted.
func TestOfferPushAnswers(t *testing.T) {
	wrong := newOfferHome(t, []string{"evil.tailx.ts.net"}, nil)
	if got := problemOf(pushOffer(context.Background(), macNode, homeOrigin, testOfferGrant)); got != "unreachable" || len(wrong.posts) != 0 || len(wrong.dials) != 1 {
		t.Fatalf("wrong name: %s, %d posts, %d dials", got, len(wrong.posts), len(wrong.dials))
	}
	for status, want := range map[int]string{
		http.StatusTemporaryRedirect: "not_accepted", http.StatusForbidden: "not_accepted",
		http.StatusOK: "not_accepted", http.StatusNotFound: "not_accepted", http.StatusNoContent: "delivered",
		http.StatusTooManyRequests: "busy",
	} {
		home := newOfferHome(t, []string{"mac.tailx.ts.net"}, func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Path != offerPath {
				t.Errorf("followed to %s", request.URL)
			}
			response.Header().Set("Location", "https://mac.tailx.ts.net/elsewhere")
			response.WriteHeader(status)
			_, _ = io.WriteString(response, "home says \x1b[2J something")
		})
		if got := problemOf(pushOffer(context.Background(), macNode, homeOrigin, testOfferGrant)); got != want || len(home.posts) != 1 {
			t.Fatalf("%d: %s after %d posts", status, got, len(home.posts))
		}
	}
}

// An endless answer is read up to 2 KiB only; a home that does not answer
// ends at 3 s.
func TestOfferPushBodyCapAndTimeout(t *testing.T) {
	newOfferHome(t, []string{"mac.tailx.ts.net"}, func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusOK)
		chunk := strings.Repeat("x", 1024)
		for request.Context().Err() == nil {
			if _, err := io.WriteString(response, chunk); err != nil {
				return
			}
		}
	})
	start := time.Now()
	if got := problemOf(pushOffer(context.Background(), macNode, homeOrigin, testOfferGrant)); got != "not_accepted" || time.Since(start) > 2*time.Second {
		t.Fatalf("endless body: %s after %s", got, time.Since(start))
	}

	newOfferHome(t, []string{"mac.tailx.ts.net"}, func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})
	start = time.Now()
	if got := problemOf(pushOffer(context.Background(), macNode, homeOrigin, testOfferGrant)); got != "unreachable" ||
		time.Since(start) < offerTimeout-100*time.Millisecond || time.Since(start) > offerTimeout+time.Second {
		t.Fatalf("slow home: %s after %s", got, time.Since(start))
	}
}

const testOfferGrant = "Zq9_-abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL"

func (fixture *trustFixture) offer(t *testing.T, via string) (protocol.AdminRPCPairOfferResultPayload, protocol.ErrorCode) {
	t.Helper()
	response := fixture.local.admin(t, protocol.AdminPairOffer, protocol.AdminRPCPairOfferPayload{Via: via})
	if response.Type == protocol.AdminError {
		failure, _ := protocol.DecodeRPCPayload[protocol.BrokerRPCErrorPayload](response)
		return protocol.AdminRPCPairOfferResultPayload{}, failure.Code
	}
	result, err := protocol.DecodeRPCPayload[protocol.AdminRPCPairOfferResultPayload](response)
	if err != nil {
		t.Fatal(err)
	}
	return result, ""
}

func (fixture *trustFixture) endOffer(t *testing.T, link *string) {
	t.Helper()
	if response := fixture.local.admin(t, protocol.AdminPairOfferEnd, protocol.AdminRPCPairOfferEndPayload{Grant: link}); response.Type != protocol.AdminPairOfferEndResult {
		t.Fatalf("end = %s %s", response.Type, response.Payload)
	}
}

// redeemable reports whether mac's app can still redeem grant (it tries).
func (fixture *trustFixture) redeemable(t *testing.T, grant string) bool {
	t.Helper()
	return fixture.code(t, macOrigin, protocol.GatewayPairRedeem, protocol.HTTPPairRedeemRequest{Grant: grant, DeviceLabel: "Phone"}) == ""
}

// One live offered grant: the same grant is re-sent; at 65 s left a new one
// replaces it and the old unredeemed one is deleted once home took the new
// one; a redeemed one stays.
func TestOfferKeepsOneLiveGrant(t *testing.T) {
	fixture := newTrustFixture(t)
	home := newOfferHome(t, []string{"*.tailx.ts.net"}, nil)
	first, code := fixture.offer(t, "mac")
	if code != "" || !first.Delivered || first.Problem != nil || first.AppOrigin != macOrigin || first.AppNode.ID != "nMac" {
		t.Fatalf("first offer = %+v, %s", first, code)
	}
	fixture.clock.Set(fixture.clock.Now().Add(30 * time.Second))
	if again, _ := fixture.offer(t, "mac"); again.ExpiresAt != first.ExpiresAt {
		t.Fatalf("second offer made a new grant: %+v", again)
	}
	offered := home.offered()
	if len(offered) != 2 || offered[0] != offered[1] || offered[0].Origin != homeOrigin || protocol.ValidateSecretText(offered[0].Grant) != nil {
		t.Fatalf("offered = %+v", offered)
	}
	if home.dials[0] != offerIP+" mac.tailx.ts.net" {
		t.Fatalf("dial = %v", home.dials)
	}
	// 60 s left: a new grant; the old one is gone.
	fixture.clock.Set(fixture.clock.Now().Add(30 * time.Second))
	renewed, _ := fixture.offer(t, "mac")
	offered = home.offered()
	if renewed.ExpiresAt == first.ExpiresAt || offered[2].Grant == offered[0].Grant {
		t.Fatalf("no new grant at 60 s left: %+v", renewed)
	}
	if fixture.redeemable(t, offered[0].Grant) {
		t.Fatal("the replaced grant still redeems")
	}
	// A redeemed grant is not deleted when it is replaced: its pairing stays.
	if !fixture.redeemable(t, offered[2].Grant) {
		t.Fatal("the live grant does not redeem")
	}
	fixture.clock.Set(fixture.clock.Now().Add(time.Minute))
	fixture.offer(t, "mac")
	if pending, _ := fixture.service.Pending(context.Background()); len(pending) != 1 {
		t.Fatalf("pending after renewal = %d", len(pending))
	}
	// End deletes the offered grant and the link grant, not a redeemed one.
	link := fixture.grant(t, "mac", false)
	fixture.endOffer(t, &link.Grant)
	if offered = home.offered(); fixture.redeemable(t, offered[len(offered)-1].Grant) || fixture.redeemable(t, link.Grant) {
		t.Fatal("a grant survived the end")
	}
	if pending, _ := fixture.service.Pending(context.Background()); len(pending) != 1 {
		t.Fatalf("pending after end = %d", len(pending))
	}
	fixture.endOffer(t, nil) // Nothing left: still fine.
}

// A renewal whose push fails keeps the replaced grant: home still lists it,
// so it must still redeem. The next accepted push deletes it.
func TestOfferRenewalKeepsTheListedGrantUntilHomeTakesTheNewOne(t *testing.T) {
	fixture := newTrustFixture(t)
	var refuse sync.Mutex
	down := false
	home := newOfferHome(t, []string{"*.tailx.ts.net"}, func(response http.ResponseWriter, _ *http.Request) {
		refuse.Lock()
		defer refuse.Unlock()
		if down {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})
	first, _ := fixture.offer(t, "mac")
	// 62 s left: inside the renewal margin (65 s), home is down.
	fixture.clock.Set(fixture.clock.Now().Add(58 * time.Second))
	refuse.Lock()
	down = true
	refuse.Unlock()
	renewed, _ := fixture.offer(t, "mac")
	offered := home.offered()
	if renewed.Delivered || renewed.ExpiresAt == first.ExpiresAt || offered[1].Grant == offered[0].Grant {
		t.Fatalf("renewal = %+v", renewed)
	}
	if !fixture.redeemable(t, offered[0].Grant) {
		t.Fatal("the grant home still lists was deleted before home took its successor")
	}
	refuse.Lock()
	down = false
	refuse.Unlock()
	fixture.clock.Set(fixture.clock.Now().Add(time.Second))
	if again, _ := fixture.offer(t, "mac"); !again.Delivered || again.ExpiresAt != renewed.ExpiresAt {
		t.Fatalf("re-send = %+v", again)
	}
	// offered[0] was redeemed above (its pairing stays); a fresh check: the
	// end deletes the live grant and nothing else is left to delete.
	fixture.endOffer(t, nil)
	if offered = home.offered(); fixture.redeemable(t, offered[len(offered)-1].Grant) {
		t.Fatal("the live grant survived the end")
	}
}

// A changed node for home's name ends the offer; no snapshot skips the push.
func TestOfferChecksThePinnedNodeBeforeEachPush(t *testing.T) {
	fixture := newTrustFixture(t)
	home := newOfferHome(t, []string{"*.tailx.ts.net"}, nil)
	fixture.offer(t, "mac")
	fixture.tailscale.set("mac", "nMac2", nil)
	fixture.refresh(t)
	if _, code := fixture.offer(t, "mac"); code != protocol.HomeChanged || len(home.offered()) != 1 {
		t.Fatalf("changed node = %s, %d pushes", code, len(home.offered()))
	}
	if origins, err := fixture.persistence.PairingOrigins(context.Background(), fixture.clock.Now(), 8); err != nil || len(origins) != 0 {
		t.Fatalf("grants left after home_changed: %v, %v", origins, err)
	}
	// The state is cleared: the next offer pins the node again.
	if result, code := fixture.offer(t, "mac"); code != "" || !result.Delivered || result.AppNode.ID != "nMac2" {
		t.Fatalf("after home_changed: %+v %s", result, code)
	}
	// No snapshot (Tailscale stopped past the grace period): no push, and a
	// local error (this computer cannot read its status), not "unreachable".
	fixture.tailscale.set("", "", errors.New("tailscale stopped"))
	fixture.clock.Set(fixture.clock.Now().Add(tailnet.DefaultGrace))
	if _, code := fixture.offer(t, "mac"); code != protocol.HostUnavailable || len(home.offered()) != 2 {
		t.Fatalf("no snapshot: %s, %d pushes", code, len(home.offered()))
	}
}

// Another home replaces the offer; a tagged home or allowTagged is refused.
func TestOfferOneHomeUntaggedOnly(t *testing.T) {
	fixture := newTrustFixture(t)
	home := newOfferHome(t, []string{"*.tailx.ts.net"}, nil)
	fixture.offer(t, "mac")
	if result, code := fixture.offer(t, "pc0"); code != "" || result.AppOrigin != "https://pc0.tailx.ts.net" {
		t.Fatalf("second home = %+v %s", result, code)
	}
	if offered := home.offered(); len(offered) != 2 || fixture.redeemable(t, offered[0].Grant) {
		t.Fatalf("the first home's grant survived: %+v", offered)
	}
	if _, code := fixture.offer(t, "ci"); code != protocol.InvalidRequest || len(home.offered()) != 2 {
		t.Fatalf("tagged home = %s", code)
	}
	if data, err := startRPCTestClient(t, fixture.server, protocol.CLI).exchange(t, string(protocol.AdminPairOffer),
		map[string]any{"via": "mac", "allowTagged": true}); err == nil {
		t.Fatalf("allowTagged answered: %s", data)
	}
}

// Without its own tailnet origin this computer cannot offer.
func TestOfferNeedsThisComputersOrigin(t *testing.T) {
	fixture := newTrustFixture(t)
	if err := fixture.persistence.PutSetting(context.Background(), PairingOriginSetting, "http://127.0.0.1:8787", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, code := fixture.offer(t, "mac"); code != protocol.InvalidRequest {
		t.Fatalf("no origin = %s", code)
	}
}

// The e2e dial hook is compiled only with the e2e tag: no file of a
// production pterm build names it.
func TestProductionBuildHasNoE2EHook(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go tool")
	}
	output, err := exec.Command(goTool, "list", "-deps", "-f", "{{if not .Standard}}{{.Dir}}|{{join .GoFiles \",\"}}{{end}}",
		"github.com/intojhanurag/phone-terminal/cmd/pterm").Output()
	if err != nil {
		t.Fatal(err)
	}
	files := 0
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		dir, names, _ := strings.Cut(line, "|")
		if dir == "" || names == "" {
			continue
		}
		for _, name := range strings.Split(names, ",") {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			files++
			if strings.Contains(string(data), "PTERM_E2E") {
				t.Fatalf("%s is in the production build and names PTERM_E2E", filepath.Join(dir, name))
			}
		}
	}
	if files < 50 {
		t.Fatalf("only %d files listed", files)
	}
}
