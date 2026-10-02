package tailnet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
	for host := range status.peers {
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)
	if want := []string{"ci.tailx.ts.net", "mac.tailx.ts.net", "pi.tailx.ts.net"}; !slices.Equal(hosts, want) {
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
	if empty := mustParse(t, `{"BackendState":"NeedsLogin"}`); empty.BackendState != "NeedsLogin" || len(empty.peers) != 0 {
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

	server := []string{"tag:server"}
	for name, test := range map[string]struct {
		origin, node string
		tags         []string
		want         Verdict
	}{
		"same node":      {"https://mac.tailx.ts.net", "nMac1CNTRL", nil, Trusted},
		"tagged pinned":  {"https://ci.tailx.ts.net", "nCi1CNTRL", server, Trusted},
		"node replaced":  {"https://mac.tailx.ts.net", "nOldMacCNTRL", nil, Changed},
		"name gone":      {"https://old.tailx.ts.net", "nMac1CNTRL", nil, Untrusted},
		"other owner":    {"https://pi.tailx.ts.net", "nPi1CNTRL", nil, Untrusted},
		"near miss":      {"https://mac.tailx.ts.net.evil.com", "nMac1CNTRL", nil, Untrusted},
		"shared-in node": {"https://shared.tailx.ts.net", "nShared1CNTRL", nil, Untrusted},
		// The tag set must match the pinned one (SL4), as a set.
		"tagged since pairing":   {"https://ci.tailx.ts.net", "nCi1CNTRL", nil, Untrusted},
		"untagged since pairing": {"https://mac.tailx.ts.net", "nMac1CNTRL", server, Untrusted},
		"tag swapped":            {"https://ci.tailx.ts.net", "nCi1CNTRL", []string{"tag:dev"}, Untrusted},
		"tag added":              {"https://ci.tailx.ts.net", "nCi1CNTRL", []string{"tag:server", "tag:dev"}, Untrusted},
	} {
		if got := status.Trust(test.origin, test.node, test.tags); got != test.want {
			t.Errorf("%s: Trust = %v, want %v", name, got, test.want)
		}
	}
	if !SameTags([]string{"tag:b", "tag:a"}, []string{"tag:a", "tag:b"}) || !SameTags(nil, []string{}) {
		t.Error("tag order or nil changed the set")
	}
}

// Owners are compared by stable user ID. Two identity providers can give
// two different users the same login name.
func TestOwnerComparedByUserIDNotLoginName(t *testing.T) {
	status := mustParse(t, `{"BackendState":"Running","MagicDNSSuffix":"tailx.ts.net",
		"Self":{"ID":"nS","DNSName":"vps.tailx.ts.net.","UserID":1},
		"User":{"1":{"LoginName":"ada@example.com"},"2":{"LoginName":"ada@example.com"}},
		"Peer":{"a":{"ID":"nA","DNSName":"mac.tailx.ts.net.","UserID":2},"b":{"ID":"nB","DNSName":"pi.tailx.ts.net.","UserID":1}}}`)
	mac, _ := status.Peer("mac.tailx.ts.net")
	if mac.Owner != "ada@example.com" || !errors.Is(status.CheckOwner(mac, false), ErrOtherOwner) {
		t.Fatalf("same login name, other user ID = %+v, %v", mac, status.CheckOwner(mac, false))
	}
	if got := status.Trust("https://mac.tailx.ts.net", "nA", nil); got != Untrusted {
		t.Fatalf("trust of another user with the same login name = %v", got)
	}
	pi, _ := status.Peer("pi.tailx.ts.net")
	if status.CheckOwner(pi, false) != nil || status.Trust("https://pi.tailx.ts.net", "nB", nil) != Trusted {
		t.Fatal("same user ID refused")
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

// A Tailscale restart or logout is a failed refresh: the last good snapshot
// stays (under the grace rules) instead of an empty one that untrusts all.
func TestMonitorKeepsLastGoodSnapshotWhileTailscaleIsNotRunning(t *testing.T) {
	source := &fakeSource{data: statusJSON}
	clock := &fakeClock{now: time.Unix(1_800_000_000, 0)}
	updates := atomic.Int32{}
	monitor := NewMonitor(Config{Source: source.read, Now: clock.Now, OnUpdate: func(*Status) { updates.Add(1) }})
	good, err := monitor.Fresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"Starting", "NeedsLogin", "Stopped"} {
		source.set(`{"BackendState":"`+state+`","MagicDNSSuffix":"tailx.ts.net"}`, nil)
		clock.Add(DefaultMaxAge)
		if monitor.Snapshot() != good {
			t.Fatalf("%s: last good snapshot dropped", state)
		}
		waitIdle(t, monitor)
		if monitor.Snapshot() != good {
			t.Fatalf("%s published as a good snapshot", state)
		}
	}
	if updates.Load() != 1 {
		t.Fatalf("OnUpdate ran %d times; a status that is not Running is no update", updates.Load())
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

// writeBinary creates an executable file in a fresh folder.
func writeBinary(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindBinaryPrefersFixedInstallOverPath(t *testing.T) {
	programFiles := t.TempDir()
	cli := filepath.Join(programFiles, "Tailscale", "tailscale.exe")
	onPath := writeBinary(t, "tailscale")
	env := func(value string) func(string) string { return func(string) string { return value } }
	path := func(string) (string, error) { return onPath, nil }
	if got, _ := FindBinary("windows", env(programFiles), path); got != onPath {
		t.Fatalf("PATH fallback = %q, want %q", got, onPath)
	}
	if err := os.MkdirAll(filepath.Dir(cli), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cli, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, _ := FindBinary("windows", env(programFiles), path); got != cli {
		t.Fatalf("installed = %q, want %q before PATH", got, cli)
	}
	missing := func(string) (string, error) { return "", errors.New("not found") }
	if got, refused := FindBinary("windows", env("relative"), missing); got != "" || refused != nil {
		t.Fatalf("relative ProgramFiles = %q, %q", got, refused)
	}
	relative := func(string) (string, error) { return "tailscale", nil }
	if got, _ := FindBinary("plan9", os.Getenv, relative); got != "" {
		t.Fatalf("relative PATH result = %q", got)
	}
}

func TestFindBinaryRefusesWritableOrForeignFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	find := func(path string) (string, []string) {
		return FindBinary("plan9", os.Getenv, func(string) (string, error) { return path, nil })
	}
	safe := writeBinary(t, "tailscale")
	if got, refused := find(safe); got != safe || refused != nil {
		t.Fatalf("a private binary was refused: %q", refused)
	}
	groupWritable := writeBinary(t, "tailscale")
	if err := os.Chmod(groupWritable, 0o775); err != nil {
		t.Fatal(err)
	}
	openFolder := writeBinary(t, "tailscale")
	if err := os.Chmod(filepath.Dir(openFolder), 0o777); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "tailscale")
	if err := os.Symlink(groupWritable, link); err != nil {
		t.Fatal(err)
	}
	// A parent folder further up that others can write lets them rename the
	// whole tree away and plant another.
	openGrandparent := t.TempDir()
	deep := filepath.Join(openGrandparent, "bin", "tailscale")
	if err := os.Mkdir(filepath.Dir(deep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deep, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(openGrandparent, 0o777); err != nil {
		t.Fatal(err)
	}
	// A link in the middle of the chain that sits in an open folder.
	middle := filepath.Join(openFolder[:len(openFolder)-len("tailscale")], "middle")
	if err := os.Symlink(safe, middle); err != nil {
		t.Fatal(err)
	}
	chain := filepath.Join(t.TempDir(), "tailscale")
	if err := os.Symlink(middle, chain); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"group-writable file": groupWritable, "world-writable folder": openFolder,
		"symlink to a writable file": link, "folder": t.TempDir(),
		"world-writable grandparent": deep, "link in a world-writable folder": chain,
	} {
		got, refused := find(path)
		if got != "" {
			t.Errorf("%s accepted: %q", name, got)
		}
		if len(refused) != 1 || !strings.HasPrefix(refused[0], "tailscale at "+path+": ") ||
			!strings.HasSuffix(refused[0], "; Termivoa will not run it") {
			t.Errorf("%s: reason = %q", name, refused)
		}
	}
	if _, refused := find(deep); len(refused) != 1 || !strings.Contains(refused[0], openGrandparent+" is writable by group or others") {
		t.Errorf("grandparent reason = %q", refused)
	}
	// The sticky bit (as on /tmp) keeps others from replacing entries they
	// do not own, so such a folder passes.
	if err := os.Chmod(openGrandparent, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if got, refused := find(deep); got != deep {
		t.Errorf("binary under a sticky folder refused: %q", refused)
	}
	// Root-owned system files pass (the owner rule allows root or this user).
	if _, err := os.Stat("/bin/sh"); err == nil {
		if got, refused := find("/bin/sh"); got != "/bin/sh" {
			t.Errorf("root-owned /bin/sh refused: %q", refused)
		}
	}
	// A missing candidate is not a refusal.
	if got, refused := find(filepath.Join(t.TempDir(), "tailscale")); got != "" || refused != nil {
		t.Errorf("missing = %q, %q", got, refused)
	}
}

// The binary is checked again before every run: one that turned unsafe
// after it was found is not run.
func TestCommandSourceChecksTheBinaryBeforeEachRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits")
	}
	path := writeBinary(t, "tailscale")
	source := CommandSource(func() string {
		found, _ := FindBinary("plan9", os.Getenv, func(string) (string, error) { return path, nil })
		return found
	})
	// The empty file is found and run (and fails to start).
	if _, err := source(context.Background()); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("safe binary was not run: %v", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := source(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("binary in a folder that became writable = %v", err)
	}
}
