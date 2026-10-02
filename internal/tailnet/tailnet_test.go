package tailnet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// statusJSON is a `tailscale status --json` sample: this computer (vps) is
// owned by ada; mac is ada's; pi is bob's; ci is tagged; a shared-in node and
// a node of another tailnet must never resolve. HostName is ignored.
const statusJSON = `{
  "BackendState": "Running",
  "MagicDNSSuffix": "tailx.ts.net",
  "Self": {"ID": "nSelf1CNTRL", "DNSName": "vps.tailx.ts.net.", "UserID": 1},
  "User": {"1": {"LoginName": "ada@example.com"}, "2": {"LoginName": "bob@example.com"}, "9": {"LoginName": "tagged-devices"}},
  "Peer": {
    "k1": {"ID": "nMac1CNTRL", "DNSName": "mac.tailx.ts.net.", "HostName": "laptop", "UserID": 1},
    "k2": {"ID": "nPi1CNTRL", "DNSName": "pi.tailx.ts.net.", "UserID": 2},
    "k3": {"ID": "nCi1CNTRL", "DNSName": "ci.tailx.ts.net.", "UserID": 9, "Tags": ["tag:server"]},
    "k4": {"ID": "nShared1CNTRL", "DNSName": "shared.tailx.ts.net.", "UserID": 1, "ShareeNode": true},
    "k5": {"ID": "nOther1CNTRL", "DNSName": "box.taily.ts.net.", "UserID": 1},
    "k6": {"ID": "bad id!", "DNSName": "broken.tailx.ts.net.", "UserID": 1}
  }
}`

func mustParse(t *testing.T, data string) *Status {
	t.Helper()
	status, err := ParseStatus([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func TestParseStatusKeepsOnlyOwnTailnetPeers(t *testing.T) {
	status := mustParse(t, statusJSON)
	if status.BackendState != "Running" || status.Suffix != "tailx.ts.net" || status.Self.Host != "vps.tailx.ts.net" ||
		status.Self.Owner != "ada@example.com" {
		t.Fatalf("status = %+v", status)
	}
	var hosts []string
	for _, node := range status.Peers() {
		hosts = append(hosts, node.Host)
	}
	if want := []string{"ci.tailx.ts.net", "mac.tailx.ts.net", "pi.tailx.ts.net"}; len(hosts) != 3 ||
		hosts[0] != want[0] || hosts[1] != want[1] || hosts[2] != want[2] {
		t.Fatalf("peers = %v, want %v (no shared-in, other-tailnet or invalid nodes)", hosts, want)
	}
	ci, _ := status.Peer("ci.tailx.ts.net")
	if !ci.Tagged() || ci.Owner != "" || ci.Wire().Owner != nil || ci.Wire().Tags[0] != "tag:server" {
		t.Fatalf("tagged node = %+v", ci)
	}
	if mac, _ := status.Peer("mac.tailx.ts.net"); mac.Wire().Owner == nil || *mac.Wire().Owner != "ada@example.com" {
		t.Fatalf("user node = %+v", mac)
	}
	// A logged-out client still parses for setup and doctor.
	if empty := mustParse(t, `{"BackendState":"NeedsLogin"}`); empty.BackendState != "NeedsLogin" || len(empty.Peers()) != 0 {
		t.Fatalf("logged out = %+v", empty)
	}
	if _, err := ParseStatus([]byte(`{`)); err == nil {
		t.Fatal("malformed JSON parsed")
	}
}

func TestResolveMatchesOnlyFirstDNSLabelsAndRefusesAmbiguity(t *testing.T) {
	status := mustParse(t, statusJSON)
	for via, want := range map[string]string{
		"mac": "nMac1CNTRL", "https://mac.tailx.ts.net": "nMac1CNTRL", "ci": "nCi1CNTRL",
	} {
		if node, err := status.Resolve(via); err != nil || node.ID != want {
			t.Errorf("Resolve(%q) = %+v, %v", via, node, err)
		}
	}
	for _, via := range []string{
		"laptop",        // HostName is chosen by each node: never used
		"shared", "box", // shared-in and other-tailnet nodes
		"vps",                          // this computer itself
		"https://mac.tailx.ts.net:443", // explicit port
		"https://mac.tailx.ts.net.",    // trailing dot
		"https://xmac.tailx.ts.net", "https://mac.tailx.ts.net.evil.com", "http://mac.tailx.ts.net",
	} {
		if _, err := status.Resolve(via); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) = %v, want not found", via, err)
		}
	}
	ambiguous := mustParse(t, `{"MagicDNSSuffix":"tailx.ts.net","Self":{"ID":"nS","DNSName":"vps.tailx.ts.net.","UserID":1},
		"User":{"1":{"LoginName":"ada@example.com"}},
		"Peer":{"a":{"ID":"nA","DNSName":"mac.tailx.ts.net.","UserID":1},"b":{"ID":"nB","DNSName":"mac.lab.tailx.ts.net.","UserID":1}}}`)
	if _, err := ambiguous.Resolve("mac"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("ambiguous short name = %v", err)
	}
	if node, err := ambiguous.Resolve("https://mac.lab.tailx.ts.net"); err != nil || node.ID != "nB" {
		t.Fatalf("full origin beats ambiguity = %+v, %v", node, err)
	}
}

func TestOwnerRuleAndTrustVerdicts(t *testing.T) {
	status := mustParse(t, statusJSON)
	mac, _ := status.Peer("mac.tailx.ts.net")
	pi, _ := status.Peer("pi.tailx.ts.net")
	ci, _ := status.Peer("ci.tailx.ts.net")
	if status.CheckOwner(mac, false) != nil || !errors.Is(status.CheckOwner(pi, true), ErrOtherOwner) {
		t.Fatal("user owners are not compared")
	}
	if !errors.Is(status.CheckOwner(ci, false), ErrTagged) || status.CheckOwner(ci, true) != nil {
		t.Fatal("tagged node needs allowTagged, and its owner is never compared")
	}
	taggedSelf := mustParse(t, `{"MagicDNSSuffix":"tailx.ts.net","Self":{"ID":"nS","DNSName":"vps.tailx.ts.net.","UserID":9,"Tags":["tag:server"]},
		"User":{"1":{"LoginName":"ada@example.com"}},"Peer":{"a":{"ID":"nA","DNSName":"mac.tailx.ts.net.","UserID":1}}}`)
	macOfTagged, _ := taggedSelf.Peer("mac.tailx.ts.net")
	if !errors.Is(taggedSelf.CheckOwner(macOfTagged, false), ErrTagged) || taggedSelf.CheckOwner(macOfTagged, true) != nil {
		t.Fatal("a tagged computer never compares owners and needs allowTagged")
	}

	for name, test := range map[string]struct {
		origin, node string
		want         Verdict
	}{
		"same node":      {"https://mac.tailx.ts.net", "nMac1CNTRL", Trusted},
		"tagged pinned":  {"https://ci.tailx.ts.net", "nCi1CNTRL", Trusted},
		"node replaced":  {"https://mac.tailx.ts.net", "nOldMacCNTRL", Changed},
		"name gone":      {"https://old.tailx.ts.net", "nMac1CNTRL", Untrusted},
		"other owner":    {"https://pi.tailx.ts.net", "nPi1CNTRL", Untrusted},
		"near miss":      {"https://mac.tailx.ts.net.evil.com", "nMac1CNTRL", Untrusted},
		"shared-in node": {"https://shared.tailx.ts.net", "nShared1CNTRL", Untrusted},
	} {
		if got := status.Trust(test.origin, test.node); got != test.want {
			t.Errorf("%s: Trust = %v, want %v", name, got, test.want)
		}
	}
}

type fakeSource struct {
	mu    sync.Mutex
	data  string
	err   error
	block chan struct{}
	calls atomic.Int32
}

func (source *fakeSource) set(data string, err error) {
	source.mu.Lock()
	source.data, source.err = data, err
	source.mu.Unlock()
}

func (source *fakeSource) read(ctx context.Context) ([]byte, error) {
	source.calls.Add(1)
	if source.block != nil {
		select {
		case <-source.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	return []byte(source.data), source.err
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) Add(step time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(step)
	clock.mu.Unlock()
}

func TestMonitorRefreshesAsyncSingleFlightWithGrace(t *testing.T) {
	source := &fakeSource{data: statusJSON, block: make(chan struct{})}
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	monitor := NewMonitor(Config{Source: source.read, Now: clock.Now})

	// Readers never wait: with nothing published they get nil at once, and
	// many concurrent reads start exactly one refresh.
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			if monitor.Snapshot() != nil {
				t.Error("snapshot before the first refresh")
			}
		})
	}
	group.Wait()
	close(source.block)
	status, err := monitor.Fresh(context.Background())
	if err != nil || status.Self.Host != "vps.tailx.ts.net" || source.calls.Load() != 1 {
		t.Fatalf("fresh = %v, %v after %d runs", status, err, source.calls.Load())
	}
	if monitor.Snapshot() != status {
		t.Fatal("published snapshot not returned")
	}

	// Reads fail from now on: the last good snapshot stays for the grace
	// period, then readers get nil (refuse) until a refresh succeeds.
	source.set("", errors.New("tailscale is not running"))
	clock.Add(DefaultMaxAge)
	if monitor.Snapshot() != status {
		t.Fatal("stale snapshot dropped before the grace period")
	}
	waitIdle(t, monitor)
	clock.Add(DefaultGrace - DefaultMaxAge - time.Second)
	if monitor.Snapshot() != status {
		t.Fatal("snapshot dropped inside the grace period")
	}
	waitIdle(t, monitor)
	clock.Add(time.Second)
	if monitor.Snapshot() != nil {
		t.Fatal("snapshot used after the grace period")
	}
	waitIdle(t, monitor)
	if _, err := monitor.Fresh(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("fresh while failing = %v", err)
	}
	source.set(statusJSON, nil)
	if recovered, err := monitor.Fresh(context.Background()); err != nil || monitor.Snapshot() != recovered {
		t.Fatalf("recovery = %v", err)
	}
}

func TestMonitorRetriesFailuresAtMostEveryRetryDelay(t *testing.T) {
	source := &fakeSource{err: errors.New("down")}
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	monitor := NewMonitor(Config{Source: source.read, Now: clock.Now})
	for range 50 {
		monitor.Snapshot()
		waitIdle(t, monitor)
	}
	if calls := source.calls.Load(); calls != 1 {
		t.Fatalf("tailscale ran %d times for 50 reads within the retry delay", calls)
	}
	clock.Add(retryDelay)
	monitor.Snapshot()
	waitIdle(t, monitor)
	if calls := source.calls.Load(); calls != 2 {
		t.Fatalf("runs after the retry delay = %d", calls)
	}
}

func TestMonitorHardTimeLimit(t *testing.T) {
	source := &fakeSource{data: statusJSON, block: make(chan struct{})} // never unblocks
	monitor := NewMonitor(Config{Source: source.read, Timeout: 20 * time.Millisecond})
	start := time.Now()
	if _, err := monitor.Fresh(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("hung refresh = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("refresh outlived its time limit: %v", elapsed)
	}
}

func waitIdle(t *testing.T, monitor *Monitor) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		monitor.mu.Lock()
		idle := monitor.refreshing == nil
		monitor.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("refresh did not finish")
}

func TestFindBinary(t *testing.T) {
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if got := FindBinary("linux", os.Getenv, func(string) (string, error) { return "/x/tailscale", nil }); got != "/x/tailscale" {
		t.Fatalf("PATH lookup = %q", got)
	}
	programFiles := t.TempDir()
	cli := filepath.Join(programFiles, "Tailscale", "tailscale.exe")
	env := func(value string) func(string) string { return func(string) string { return value } }
	if got := FindBinary("windows", env(programFiles), missing); got != "" {
		t.Fatalf("missing install = %q", got)
	}
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindBinary("windows", env(programFiles), missing); got != cli {
		t.Fatalf("installed = %q, want %q", got, cli)
	}
	if got := FindBinary("windows", env("relative"), missing); got != "" {
		t.Fatalf("relative ProgramFiles = %q", got)
	}
}

func TestCommandSourceWithoutBinaryIsUnavailable(t *testing.T) {
	if _, err := CommandSource("")(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no binary = %v", err)
	}
}
