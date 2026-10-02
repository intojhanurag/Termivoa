//go:build soak && darwin

// Package soak holds P7 reliability probes. They are build-tagged so the
// ordinary `go test ./...` and CI stay fast; run them with scripts/soak.sh.
package soak

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/metrics"
	"runtime/pprof"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/gatewayclient"
	"github.com/intojhanurag/phone-terminal/internal/host"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/localclient"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/store"
)

const (
	childMarker    = "pterm-soak-child"
	brokerMarker   = "pterm-soak-broker"
	soakProjectID  = "e1111111-1111-4111-8111-111111111111"
	busyProfileID  = "e2222222-2222-4222-8222-222222222222"
	seedBrokerID   = "e4444444-4444-4444-8444-444444444444"
	childRows      = 24
	statusInterval = 10 * time.Millisecond
)

// ---------------------------------------------------------------------------
// Helper subprocesses. Both are this test binary re-executed with a marker.

// TestSoakChild is the busy PTY child. Row 1 is a status line outside the
// scroll region (rows 2..24), rewritten every 10 ms with the child's own pid,
// last flood line number, last echoed input token and a wall-clock timestamp.
// Rows 2..24 scroll a synthetic flood at the configured bytes/second.
func TestSoakChild(t *testing.T) {
	index := slices.Index(os.Args, childMarker)
	if index < 0 || index+1 >= len(os.Args) {
		t.Skip("helper subprocess only")
	}
	rate, _ := strconv.Atoi(os.Args[index+1])
	if err := runSoakChild(rate); err != nil {
		fmt.Fprintln(os.Stderr, "SOAK_CHILD_ERROR:", err)
		os.Exit(7)
	}
	os.Exit(0)
}

func runSoakChild(rate int) error {
	state, err := term.MakeRaw(os.Stdin.Fd())
	if err != nil {
		return err
	}
	defer term.Restore(os.Stdin.Fd(), state) //nolint:errcheck
	echoes := make(chan string, 16)
	go func() {
		defer close(echoes)
		input := bufio.NewReader(os.Stdin)
		for {
			line, err := input.ReadString('\r')
			if err != nil {
				return
			}
			echoes <- strings.TrimSuffix(line, "\r")
		}
	}()
	out := bufio.NewWriterSize(os.Stdout, 64<<10)
	fmt.Fprintf(out, "\x1b[2J\x1b[2;%dr\x1b[%d;1H", childRows, childRows)
	const filler = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWX" // 60 bytes
	perTick := rate / int(time.Second/statusInterval)
	ticker := time.NewTicker(statusInterval)
	defer ticker.Stop()
	pid, echo, paused, seq := os.Getpid(), "-", false, 0
	for {
		select {
		case token, ok := <-echoes:
			if !ok {
				return nil
			}
			switch token {
			case "PAUSE":
				paused = true
			case "RESUME":
				paused = false
			}
			echo = token
		case <-ticker.C:
			for written := 0; !paused && written < perTick; {
				seq++
				n, _ := fmt.Fprintf(out, "\r\nL%09d %s", seq, filler)
				written += n
			}
		}
		fmt.Fprintf(out, "\x1b7\x1b[1;1H\x1b[2KS pid=%d seq=%d echo=%s t=%d\x1b8", pid, seq, echo, time.Now().UnixNano())
		if err := out.Flush(); err != nil {
			return err // PTY hangup after broker death ends the child.
		}
	}
}

// TestSoakBroker runs the real host.RunBroker (the same entry point as
// `pterm __broker --dev`) in its own process and appends runtime/metrics
// samples (heap objects bytes, total mapped bytes, goroutines) once a second.
func TestSoakBroker(t *testing.T) {
	index := slices.Index(os.Args, brokerMarker)
	if index < 0 || index+3 >= len(os.Args) {
		t.Skip("helper subprocess only")
	}
	dataDir, runtimeDir, metricsPath := os.Args[index+1], os.Args[index+2], os.Args[index+3]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go sampleRuntime(ctx, metricsPath)
	if path := os.Getenv("PTERM_SOAK_CPUPROFILE"); path != "" { // optional diagnosis aid
		if file, err := os.Create(path); err == nil {
			_ = pprof.StartCPUProfile(file)
		}
	}
	err := host.RunBroker(ctx, host.BrokerConfig{
		DataDir: dataDir, RuntimeDir: runtimeDir, HostBuild: "soak", Profile: platform.DevelopmentProfile,
	})
	pprof.StopCPUProfile() // os.Exit below skips defers
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "SOAK_BROKER_ERROR:", err)
		os.Exit(7)
	}
	os.Exit(0)
}

func sampleRuntime(ctx context.Context, path string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/sched/goroutines:goroutines"},
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		metrics.Read(samples)
		fmt.Fprintf(file, "%d %d %d\n", samples[0].Value.Uint64(), samples[1].Value.Uint64(), samples[2].Value.Uint64())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ---------------------------------------------------------------------------
// Host harness: temp dirs, seeded store, broker process, paired credential.

type soakHost struct {
	t                   *testing.T
	dataDir, runtimeDir string
	metricsPath         string
	executable          string
	busyRate            int
	broker              *exec.Cmd
	brokerDone          chan struct{}
	brokerLog           *lockedWriter
	gateway             *gatewayclient.Client
	credential          string
}

func startSoakHost(t *testing.T, ctx context.Context, busyRate int) *soakHost {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pterm-soak-") // short Unix socket path
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	h := &soakHost{
		t: t, dataDir: filepath.Join(root, "data"), runtimeDir: filepath.Join(root, "runtime"),
		metricsPath: filepath.Join(root, "broker-metrics"), executable: executable, busyRate: busyRate,
	}
	h.seed(ctx)
	h.startBroker(ctx)
	h.pair(ctx)
	return h
}

func (h *soakHost) seed(ctx context.Context) {
	t := h.t
	if err := os.MkdirAll(h.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	persistence, err := store.Open(ctx, filepath.Join(h.dataDir, "phone-terminal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer persistence.Close()
	now := time.Now().UTC()
	if _, err := persistence.BeginBrokerGeneration(ctx, seedBrokerID, now); err != nil {
		t.Fatal(err)
	}
	if err := persistence.PutProject(ctx, store.Project{
		ID: soakProjectID, DisplayName: "Soak", CanonicalPath: h.dataDir, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.PutShellProfile(ctx, store.ShellProfile{
		ID: busyProfileID, DisplayName: "soak", Platform: store.PlatformDarwin, ExecutablePath: h.executable,
		Arguments: []string{"-test.run=^TestSoakChild$", "--", childMarker, strconv.Itoa(h.busyRate)},
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := persistence.Close(); err != nil {
		t.Fatal(err)
	}
}

func (h *soakHost) startBroker(ctx context.Context) {
	t := h.t
	command := exec.Command(h.executable, "-test.run=^TestSoakBroker$", "--",
		brokerMarker, h.dataDir, h.runtimeDir, h.metricsPath)
	output := &lockedWriter{}
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{}) // closed when the broker process has exited
	go func() { _ = command.Wait(); close(done) }()
	h.broker, h.brokerDone, h.brokerLog = command, done, output
	t.Cleanup(func() {
		select {
		case <-done:
			return
		default:
		}
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Errorf("broker did not stop on interrupt\n%s", output.String())
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		probe, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		_, _, err := ipc.ValidateBrokerEndpoint(probe, h.runtimeDir, protocol.CLI, "soak")
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("broker handshake: %v\n%s", err, output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	gateway, err := gatewayclient.New(gatewayclient.Config{RuntimeDir: h.runtimeDir, ClientBuild: "soak-gateway"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gateway.Close() })
	h.gateway = gateway
}

func (h *soakHost) pair(ctx context.Context) {
	t := h.t
	admin, err := localclient.DialAdmin(ctx, h.runtimeDir, "soak-admin")
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	grant, err := admin.CreatePairingGrant(ctx)
	if err != nil {
		t.Fatal(err)
	}
	redeemed, err := h.gateway.RedeemPairing(ctx, protocol.HTTPPairRedeemRequest{Grant: grant.Grant, DeviceLabel: "Soak phone"})
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.ApprovePairing(ctx, redeemed.PendingID); err != nil {
		t.Fatal(err)
	}
	issued, err := h.gateway.PollPairing(ctx, protocol.HTTPPairPollRequest{PendingID: redeemed.PendingID, PollSecret: redeemed.PollSecret})
	if err != nil || issued.Credential == "" {
		t.Fatalf("pairing poll = %#v, %v", issued, err)
	}
	h.credential = issued.Credential
}

func (h *soakHost) createSession(ctx context.Context, profileID string) (protocol.BrokerRPCSessionRecord, error) {
	created, err := h.gateway.CreateSession(ctx, h.credential, protocol.HTTPSessionCreateRequest{
		OperationID: newUUID(h.t), ProjectID: soakProjectID, ShellProfileID: profileID,
	})
	return created.Session, err
}

func (h *soakHost) sessions(ctx context.Context) []protocol.BrokerRPCSessionRecord {
	h.t.Helper()
	list, err := h.gateway.ListSessions(ctx, h.credential)
	if err != nil {
		h.t.Fatal(err)
	}
	return list.Sessions
}

// sample reads broker RSS/CPU from ps and the latest in-process runtime sample.
type hostSample struct {
	at                    time.Duration
	rssKiB                int64
	cpu                   time.Duration
	heapBytes, totalBytes int64
	goroutines            int64
	children              int
}

func (h *soakHost) sample(start time.Time) hostSample {
	h.t.Helper()
	pid := strconv.Itoa(h.broker.Process.Pid)
	out, err := exec.Command("ps", "-o", "rss=,time=", "-p", pid).Output()
	if err != nil {
		h.t.Fatalf("ps broker: %v", err)
	}
	fields := strings.Fields(string(out))
	s := hostSample{at: time.Since(start)}
	s.rssKiB, _ = strconv.ParseInt(fields[0], 10, 64)
	s.cpu = parsePSTime(fields[1])
	s.children = len(childPIDs(h.broker.Process.Pid))
	if data, err := os.ReadFile(h.metricsPath); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		fmt.Sscanf(lines[len(lines)-1], "%d %d %d", &s.heapBytes, &s.totalBytes, &s.goroutines)
	}
	return s
}

func (s hostSample) String() string {
	return fmt.Sprintf("t=%5.1fs rss=%6.1fMiB cpu=%6.2fs heap=%6.1fMiB goMapped=%6.1fMiB goroutines=%4d children=%d",
		s.at.Seconds(), float64(s.rssKiB)/1024, s.cpu.Seconds(), mib(s.heapBytes), mib(s.totalBytes), s.goroutines, s.children)
}

func childPIDs(parent int) []int {
	out, _ := exec.Command("pgrep", "-P", strconv.Itoa(parent)).Output()
	var pids []int
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func processAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// parsePSTime parses macOS ps TIME ("m:ss.cc" or "h:mm:ss.cc").
func parsePSTime(value string) time.Duration {
	var total float64
	for _, part := range strings.Split(value, ":") {
		n, _ := strconv.ParseFloat(part, 64)
		total = total*60 + n
	}
	return time.Duration(total * float64(time.Second))
}

// ---------------------------------------------------------------------------
// Viewer: a phone-equivalent live stream through the real gateway client.

type viewer struct {
	t       *testing.T
	ctx     context.Context
	stream  gatewayapi.LiveStream
	session protocol.BrokerRPCSessionRecord
	done    chan struct{}

	mu            sync.Mutex
	err           error
	lease         string
	inputSeq      int
	screen        []string // snapshot + applied patches, by row text
	revision      string
	snapshotReady bool
	pendingSnap   []string
	pid, seq      int64
	echo          string
	echoWaiters   map[string]chan time.Time
	outputLatency []time.Duration

	patches, snapshots, baseMismatches, bytes atomic.Int64
	ready                                     chan struct{}
	readyOnce                                 sync.Once
}

// openViewer performs hello, attach, snapshot apply and optionally takes
// control, then keeps consuming in the background.
func openViewer(t *testing.T, ctx context.Context, gateway *gatewayclient.Client, credential string, session protocol.BrokerRPCSessionRecord, control bool) *viewer {
	t.Helper()
	stream, err := gateway.OpenLive(ctx, credential)
	if err != nil {
		t.Fatal(err)
	}
	return openViewerOn(t, ctx, stream, session, control)
}

// openViewerOn runs the viewer protocol on any live stream, such as a phone
// WebSocket through the real gateway.
func openViewerOn(t *testing.T, ctx context.Context, stream gatewayapi.LiveStream, session protocol.BrokerRPCSessionRecord, control bool) *viewer {
	t.Helper()
	v := &viewer{t: t, ctx: ctx, stream: stream, session: session, done: make(chan struct{}),
		echoWaiters: map[string]chan time.Time{}, ready: make(chan struct{})}
	t.Cleanup(func() { _ = stream.Close(); <-v.done })
	v.send(protocol.Hello, false, protocol.HelloPayload{
		ClientKind: protocol.Browser, ClientBuild: "soak", CapabilityProfile: protocol.PhoneTerminalV1,
		SupportedVersions: []int64{protocol.Version},
	})
	v.send(protocol.SessionAttach, true, protocol.EmptyPayload{})
	go v.run()
	v.waitReady(10 * time.Second)
	if control {
		v.send(protocol.ControlAcquire, true, protocol.EmptyPayload{})
		waitFor(t, 10*time.Second, "control lease", func() bool { return v.currentLease() != "" })
		go v.heartbeat()
	}
	return v
}

// openStalledViewer attaches and then never reads.
func openStalledViewer(t *testing.T, ctx context.Context, gateway *gatewayclient.Client, credential string, session protocol.BrokerRPCSessionRecord) *viewer {
	t.Helper()
	stream, err := gateway.OpenLive(ctx, credential)
	if err != nil {
		t.Fatal(err)
	}
	v := &viewer{t: t, ctx: ctx, stream: stream, session: session, done: make(chan struct{})}
	close(v.done)
	t.Cleanup(func() { _ = stream.Close() })
	v.send(protocol.Hello, false, protocol.HelloPayload{
		ClientKind: protocol.Browser, ClientBuild: "soak-stalled", CapabilityProfile: protocol.PhoneTerminalV1,
		SupportedVersions: []int64{protocol.Version},
	})
	v.send(protocol.SessionAttach, true, protocol.EmptyPayload{})
	return v
}

func (v *viewer) send(kind protocol.MessageType, sessionScoped bool, payload any) {
	v.t.Helper()
	if err := v.trySend(kind, sessionScoped, payload); err != nil {
		v.t.Fatalf("send %s: %v", kind, err)
	}
}

func (v *viewer) trySend(kind protocol.MessageType, sessionScoped bool, payload any) error {
	body, _ := json.Marshal(payload)
	envelope := protocol.Envelope{Version: protocol.Version, Type: kind, RequestID: newUUID(v.t), Payload: body}
	if sessionScoped {
		envelope.SessionID, envelope.Generation = &v.session.ID, &v.session.Generation
	}
	data, _ := json.Marshal(envelope)
	ctx, cancel := context.WithTimeout(v.ctx, 5*time.Second)
	defer cancel()
	return v.stream.Send(ctx, data)
}

func (v *viewer) run() {
	defer close(v.done)
	for {
		data, err := v.stream.Receive(v.ctx)
		if err != nil {
			v.fail(err)
			return
		}
		v.bytes.Add(int64(len(data)))
		received := time.Now()
		envelope, err := protocol.DecodeHost(data)
		if err != nil {
			v.fail(err)
			return
		}
		if err := v.handle(envelope, received); err != nil {
			v.fail(err)
			return
		}
	}
}

func (v *viewer) handle(envelope protocol.Envelope, received time.Time) error {
	switch envelope.Type {
	case protocol.SnapshotBegin:
		payload, _ := payloadOf[protocol.SnapshotBeginPayload](envelope)
		v.mu.Lock()
		v.pendingSnap = make([]string, payload.Screen.Rows)
		v.mu.Unlock()
	case protocol.SnapshotRows:
		payload, _ := payloadOf[protocol.SnapshotRowsPayload](envelope)
		v.mu.Lock()
		for i, row := range payload.Rows {
			if index := int(payload.StartRow) + i; index < len(v.pendingSnap) {
				v.pendingSnap[index] = rowText(row)
			}
		}
		v.mu.Unlock()
	case protocol.SnapshotEnd:
		payload, _ := payloadOf[protocol.SnapshotEndPayload](envelope)
		v.mu.Lock()
		if int(payload.RowCount) != len(v.pendingSnap) {
			v.mu.Unlock()
			return fmt.Errorf("snapshot rowCount=%d, begin rows=%d", payload.RowCount, len(v.pendingSnap))
		}
		v.screen, v.revision, v.snapshotReady = v.pendingSnap, payload.Revision, true
		v.mu.Unlock()
		v.snapshots.Add(1)
		v.observeStatus(received)
		if err := v.trySend(protocol.SnapshotApplied, true, protocol.SnapshotAppliedPayload{SnapshotID: payload.SnapshotID, Revision: payload.Revision}); err != nil {
			return err
		}
		v.readyOnce.Do(func() { close(v.ready) })
	case protocol.ScreenPatch:
		payload, _ := payloadOf[protocol.ScreenPatchPayload](envelope)
		v.mu.Lock()
		if !v.snapshotReady || payload.BaseRevision != v.revision {
			v.baseMismatches.Add(1)
		}
		for _, changed := range payload.Rows {
			if int(changed.Index) < len(v.screen) {
				v.screen[changed.Index] = rowText(changed.Row)
			}
		}
		v.revision = payload.Revision
		v.mu.Unlock()
		v.patches.Add(1)
		v.observeStatus(received)
	case protocol.ControlChanged:
		payload, _ := payloadOf[protocol.ControlChangedPayload](envelope)
		if payload.Lease != nil {
			v.mu.Lock()
			v.lease = *payload.Lease
			v.mu.Unlock()
		}
	case protocol.Error, protocol.InputRejected:
		return fmt.Errorf("host sent %s: %s", envelope.Type, envelope.Payload)
	}
	return nil
}

// observeStatus parses row 1 ("S pid=.. seq=.. echo=.. t=..").
func (v *viewer) observeStatus(received time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.screen) == 0 {
		return
	}
	fields := map[string]string{}
	for _, field := range strings.Fields(v.screen[0]) {
		if key, value, ok := strings.Cut(field, "="); ok {
			fields[key] = value
		}
	}
	if ts, err := strconv.ParseInt(fields["t"], 10, 64); err == nil {
		v.outputLatency = append(v.outputLatency, received.Sub(time.Unix(0, ts)))
	}
	v.pid, _ = strconv.ParseInt(fields["pid"], 10, 64)
	v.seq, _ = strconv.ParseInt(fields["seq"], 10, 64)
	if echo := fields["echo"]; echo != v.echo {
		v.echo = echo
		if waiter := v.echoWaiters[echo]; waiter != nil {
			waiter <- received
			delete(v.echoWaiters, echo)
		}
	}
}

func (v *viewer) heartbeat() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-v.done:
			return
		case <-ticker.C:
			lease := v.currentLease()
			_ = v.trySend(protocol.Heartbeat, true, protocol.HeartbeatPayload{Lease: &lease})
		}
	}
}

// roundTrip sends token+Enter as the controller and returns the time until a
// frame showing the child's echo of that token arrives.
func (v *viewer) roundTrip(token string) (time.Duration, error) {
	waiter := make(chan time.Time, 1)
	v.mu.Lock()
	v.echoWaiters[token] = waiter
	lease := v.lease
	v.inputSeq += 2
	textSeq, keySeq := strconv.Itoa(v.inputSeq-1), strconv.Itoa(v.inputSeq)
	v.mu.Unlock()
	sent := time.Now()
	if err := v.trySend(protocol.InputText, true, protocol.InputTextPayload{Lease: lease, InputSequence: textSeq, Text: token}); err != nil {
		return 0, err
	}
	if err := v.trySend(protocol.InputKey, true, protocol.InputKeyPayload{Lease: lease, InputSequence: keySeq, Key: protocol.Enter}); err != nil {
		return 0, err
	}
	select {
	case at := <-waiter:
		return at.Sub(sent), nil
	case <-v.done:
		return 0, fmt.Errorf("viewer ended: %v", v.Err())
	case <-time.After(5 * time.Second):
		return 0, fmt.Errorf("echo %s not seen within 5s", token)
	}
}

func (v *viewer) waitReady(timeout time.Duration) {
	v.t.Helper()
	select {
	case <-v.ready:
	case <-v.done:
		v.t.Fatalf("viewer ended before snapshot: %v", v.Err())
	case <-time.After(timeout):
		v.t.Fatal("viewer snapshot did not arrive")
	}
}

func (v *viewer) fail(err error) {
	v.mu.Lock()
	if v.err == nil {
		v.err = err
	}
	v.mu.Unlock()
}

func (v *viewer) Err() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.err
}

func (v *viewer) currentLease() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.lease
}

func (v *viewer) identity() (pid, seq int64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.pid, v.seq
}

func (v *viewer) screenCopy() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return slices.Clone(v.screen)
}

func (v *viewer) takeLatencies() []time.Duration {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := v.outputLatency
	v.outputLatency = nil
	return out
}

// payloadOf decodes a payload that DecodeHost already validated strictly;
// validating it a second time made the viewer, not the link, the bottleneck.
func payloadOf[T any](envelope protocol.Envelope) (T, error) {
	var payload T
	err := json.Unmarshal(envelope.Payload, &payload)
	return payload, err
}

func rowText(row protocol.TerminalRow) string {
	var cells []string
	for _, run := range row.Runs {
		for i, cell := range run.Cells {
			index := int(run.Column) + i
			for len(cells) <= index {
				cells = append(cells, " ")
			}
			if !cell.Continuation {
				cells[index] = cell.Text
			}
		}
	}
	return strings.TrimRight(strings.Join(cells, ""), " ")
}

// ---------------------------------------------------------------------------
// Small utilities.

type lockedWriter struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func newUUID(t *testing.T) string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
}

func waitFor(t *testing.T, timeout time.Duration, name string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type stats struct {
	n                  int
	p50, p95, p99, max time.Duration
}

func summarize(values []time.Duration) stats {
	if len(values) == 0 {
		return stats{}
	}
	sorted := slices.Clone(values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(q float64) time.Duration { return sorted[min(len(sorted)-1, int(q*float64(len(sorted))))] }
	return stats{n: len(sorted), p50: at(0.50), p95: at(0.95), p99: at(0.99), max: sorted[len(sorted)-1]}
}

func (s stats) String() string {
	return fmt.Sprintf("n=%d p50=%s p95=%s p99=%s max=%s", s.n, s.p50.Round(10*time.Microsecond),
		s.p95.Round(10*time.Microsecond), s.p99.Round(10*time.Microsecond), s.max.Round(10*time.Microsecond))
}

func mib(bytes int64) float64 { return float64(bytes) / (1 << 20) }

func envDuration(name string, fallback time.Duration) time.Duration {
	if value, err := time.ParseDuration(os.Getenv(name)); err == nil && value > 0 {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	if value, err := strconv.Atoi(os.Getenv(name)); err == nil && value >= 0 {
		return value
	}
	return fallback
}
