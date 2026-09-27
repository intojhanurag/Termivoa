//go:build soak && darwin

package soak

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/broker"
	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/gatewayclient"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// Documented bounds (see docs/evidence/p7/soak.md).
const (
	maxBrokerRSSMiB     = 300                    // plan section 13 combined budget, broker alone here
	maxRSSGrowthMiB     = 32                     // after warm-up, over the rest of the run
	maxStallGrowthMiB   = 16                     // while one viewer is stalled
	maxGoroutineGrowth  = 16                     // final vs. warm-up
	maxInputRTTP95      = 250 * time.Millisecond // busy-host bound; not the plan's 30 ms idle benchmark
	maxOutputToFrameP95 = 250 * time.Millisecond
	minPatchesPerSecond = 20
)

// TestSoakThreeBusySessions runs three flooding sessions, each with a
// controlling phone-equivalent viewer, samples broker RSS/heap/goroutines,
// probes input round trips, and periodically replaces a viewer through a new
// gateway client (the gateway-restart path) while checking child identity.
func TestSoakThreeBusySessions(t *testing.T) {
	duration := envDuration("PTERM_SOAK_DURATION", 2*time.Minute)
	rate := envInt("PTERM_SOAK_RATE", 1<<20)
	reconnectEvery := envDuration("PTERM_SOAK_RECONNECT", 20*time.Second)
	warmUp := min(20*time.Second, duration/4)
	ctx, cancel := context.WithTimeout(context.Background(), duration+2*time.Minute)
	defer cancel()
	h := startSoakHost(t, ctx, rate)
	start := time.Now()
	idle := h.sample(start)
	t.Logf("config: sessions=3 rate=%d B/s/session duration=%s reconnectEvery=%s warmUp=%s", rate, duration, reconnectEvery, warmUp)
	t.Logf("idle broker: %s", idle)

	const count = 3
	var sessions [count]protocol.BrokerRPCSessionRecord
	var viewers [count]*viewer
	var pids [count]int64
	sessionsStarted := time.Now()
	for i := range count {
		session, err := h.createSession(ctx, busyProfileID)
		if err != nil {
			t.Fatal(err)
		}
		sessions[i] = session
		viewers[i] = openViewer(t, ctx, h.gateway, h.credential, session, true)
		waitFor(t, 5*time.Second, "child pid", func() bool { pid, _ := viewers[i].identity(); return pid > 0 })
		pids[i], _ = viewers[i].identity()
	}

	var (
		rtts        [count][]time.Duration
		latencies   [count][]time.Duration
		patches     [count]int64
		wireBytes   [count]int64
		reconnects  int
		samples     []hostSample
		probe       int
		lastSample  time.Time
		lastRotate  = time.Now()
		measureFrom = start.Add(warmUp)
	)
	collect := func(i int) {
		latencies[i] = append(latencies[i], viewers[i].takeLatencies()...)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for time.Since(start) < duration {
		<-ticker.C
		probe++
		for i, v := range viewers {
			rtt, err := v.roundTrip(fmt.Sprintf("P%dx%d", i, probe))
			if err != nil {
				t.Fatalf("session %d probe %d: %v", i, probe, err)
			}
			if time.Now().After(measureFrom) {
				rtts[i] = append(rtts[i], rtt)
			}
		}
		if time.Since(lastSample) >= 5*time.Second {
			lastSample = time.Now()
			s := h.sample(start)
			samples = append(samples, s)
			t.Logf("sample %s", s)
			if s.children != count {
				t.Fatalf("broker children = %d, want %d", s.children, count)
			}
			if time.Now().Before(measureFrom) {
				for i := range count {
					viewers[i].takeLatencies() // discard warm-up
				}
			} else {
				for i := range count {
					collect(i)
				}
			}
		}
		if time.Since(lastRotate) >= reconnectEvery {
			lastRotate = time.Now()
			i := reconnects % count
			old := viewers[i]
			collect(i)
			patches[i] += old.patches.Load()
			wireBytes[i] += old.bytes.Load()
			if n := old.baseMismatches.Load(); n != 0 {
				t.Fatalf("session %d: %d patches did not chain from the displayed revision", i, n)
			}
			_ = old.stream.Close()
			<-old.done
			// A fresh gateway client models a gateway restart: the broker sees
			// every gateway connection close and a new one open.
			gateway, err := gatewayclient.New(gatewayclient.Config{RuntimeDir: h.runtimeDir, ClientBuild: "soak-gateway-restart"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = gateway.Close() })
			viewers[i] = openViewer(t, ctx, gateway, h.credential, sessions[i], true)
			waitFor(t, 5*time.Second, "reconnected status", func() bool { pid, _ := viewers[i].identity(); return pid > 0 })
			if pid, _ := viewers[i].identity(); pid != pids[i] {
				t.Fatalf("session %d child pid changed across reconnect: %d -> %d", i, pids[i], pid)
			}
			reconnects++
		}
	}
	elapsed := time.Since(start)
	final := h.sample(start)
	samples = append(samples, final)
	t.Logf("final  %s", final)

	// Identity and liveness after the run.
	for _, record := range h.sessions(ctx) {
		i := slices.IndexFunc(sessions[:], func(s protocol.BrokerRPCSessionRecord) bool { return s.ID == record.ID })
		if i < 0 || record.Generation != sessions[i].Generation || record.State != protocol.Running {
			t.Fatalf("session after soak = %#v", record)
		}
	}
	failed := false
	for i, v := range viewers {
		collect(i)
		patches[i] += v.patches.Load()
		wireBytes[i] += v.bytes.Load()
		if n := v.baseMismatches.Load(); n != 0 {
			t.Errorf("session %d: %d patches did not chain from the displayed revision", i, n)
		}
		pid, seq := v.identity()
		if pid != pids[i] || !processAlive(int(pid)) {
			t.Errorf("session %d child identity/liveness: pid %d -> %d", i, pids[i], pid)
		}
		rtt, lat := summarize(rtts[i]), summarize(latencies[i])
		fps := float64(patches[i]) / elapsed.Seconds()
		bytesPerSec := float64(seq) * 72 / time.Since(sessionsStarted).Seconds() // 72-byte flood lines
		t.Logf("session %d pid=%d childOutput=%.2f MiB/s patches/s=%.1f viewerWire=%.2f MiB/s", i, pid,
			bytesPerSec/(1<<20), fps, mib(wireBytes[i])/elapsed.Seconds())
		t.Logf("session %d input round trip (text+Enter -> echo frame): %s", i, rtt)
		t.Logf("session %d output-to-frame (child write -> patch received): %s", i, lat)
		if rtt.p95 > maxInputRTTP95 || lat.p95 > maxOutputToFrameP95 || fps < minPatchesPerSecond {
			t.Errorf("session %d exceeded a documented threshold", i)
			failed = true
		}
	}

	// Memory and goroutine bounds after warm-up.
	var warm *hostSample
	var maxRSS, maxGoroutines int64
	for i := range samples {
		if warm == nil && samples[i].at >= warmUp {
			warm = &samples[i]
		}
		maxRSS = max(maxRSS, samples[i].rssKiB)
		if warm != nil {
			maxGoroutines = max(maxGoroutines, samples[i].goroutines)
		}
	}
	if warm == nil {
		t.Fatal("no post-warm-up sample")
	}
	t.Logf("warm   %s", *warm)
	t.Logf("summary: reconnects=%d maxRSS=%.1fMiB rssGrowthAfterWarm=%.1fMiB heapGrowthAfterWarm=%.1fMiB goroutines warm=%d final=%d max=%d brokerCPU=%.0f%%",
		reconnects, float64(maxRSS)/1024, float64(final.rssKiB-warm.rssKiB)/1024, mib(final.heapBytes-warm.heapBytes),
		warm.goroutines, final.goroutines, maxGoroutines, 100*(final.cpu-idle.cpu).Seconds()/elapsed.Seconds())
	if float64(maxRSS)/1024 > maxBrokerRSSMiB {
		t.Errorf("broker RSS %.1f MiB exceeds %d MiB", float64(maxRSS)/1024, maxBrokerRSSMiB)
	}
	if float64(final.rssKiB-warm.rssKiB)/1024 > maxRSSGrowthMiB {
		t.Errorf("broker RSS grew %.1f MiB after warm-up (bound %d)", float64(final.rssKiB-warm.rssKiB)/1024, maxRSSGrowthMiB)
	}
	if final.goroutines > warm.goroutines+maxGoroutineGrowth {
		t.Errorf("goroutines grew %d -> %d", warm.goroutines, final.goroutines)
	}
	if failed {
		return
	}

	// Post-stress resynchronization: pause each child, then a fresh observer's
	// snapshot must equal the long-running controller's patched screen.
	for i, v := range viewers {
		if _, err := v.roundTrip("PAUSE"); err != nil {
			t.Fatal(err)
		}
		fresh := openViewer(t, ctx, h.gateway, h.credential, sessions[i], false)
		_, seq := v.identity()
		want := fmt.Sprintf("L%09d", seq)
		waitFor(t, 5*time.Second, "resync equality", func() bool {
			a, b := v.screenCopy(), fresh.screenCopy()
			return len(a) == childRows && slices.Equal(a[1:], b[1:]) && (seq == 0 || strings.HasPrefix(b[childRows-1], want))
		})
		t.Logf("session %d post-stress snapshot matches patched screen at line %s", i, want)
	}
}

// TestSoakStalledViewer attaches a viewer that never reads while the child
// floods. The child must keep running, broker memory must stay bounded, the
// stalled viewer must be disconnected, and a fresh viewer must get a correct
// snapshot.
func TestSoakStalledViewer(t *testing.T) {
	rate := envInt("PTERM_SOAK_RATE", 1<<20)
	stall := envDuration("PTERM_SOAK_STALL", 20*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), stall+2*time.Minute)
	defer cancel()
	h := startSoakHost(t, ctx, rate)
	start := time.Now()
	session, err := h.createSession(ctx, busyProfileID)
	if err != nil {
		t.Fatal(err)
	}
	healthy := openViewer(t, ctx, h.gateway, h.credential, session, true)
	waitFor(t, 5*time.Second, "child pid", func() bool { pid, _ := healthy.identity(); return pid > 0 })
	pid, _ := healthy.identity()
	time.Sleep(5 * time.Second) // warm-up
	base := h.sample(start)
	t.Logf("before stall %s", base)

	stalled := openStalledViewer(t, ctx, h.gateway, h.credential, session)
	_, seqBefore := healthy.identity()
	patchesBefore := healthy.patches.Load()
	peak := base
	for elapsed := time.Duration(0); elapsed < stall; elapsed += time.Second {
		time.Sleep(time.Second)
		s := h.sample(start)
		if s.rssKiB > peak.rssKiB {
			peak = s
		}
		if int(elapsed/time.Second)%5 == 4 {
			t.Logf("stalled  %s", s)
		}
	}
	after := h.sample(start)
	pidNow, seqAfter := healthy.identity()
	t.Logf("after stall %s", after)
	t.Logf("healthy viewer during stall: patches/s=%.1f childLinesAdvanced=%d err=%v",
		float64(healthy.patches.Load()-patchesBefore)/stall.Seconds(), seqAfter-seqBefore, healthy.Err())
	if pidNow != pid || !processAlive(int(pid)) || seqAfter <= seqBefore {
		t.Fatalf("child did not keep running: pid %d -> %d seq %d -> %d", pid, pidNow, seqBefore, seqAfter)
	}
	if healthy.Err() != nil {
		t.Fatalf("healthy viewer failed: %v", healthy.Err())
	}
	growth := float64(peak.rssKiB-base.rssKiB) / 1024
	t.Logf("peak RSS growth during stall = %.1f MiB (bound %d), heap %.1f -> %.1f MiB",
		growth, maxStallGrowthMiB, mib(base.heapBytes), mib(peak.heapBytes))
	if growth > maxStallGrowthMiB {
		t.Errorf("broker RSS grew %.1f MiB while a viewer was stalled", growth)
	}

	// The stalled stream should only hold what was already buffered, then EOF
	// once the broker's viewer delivery timeout has passed.
	drainCtx, stopDrain := context.WithTimeout(ctx, broker.DefaultViewerTimeout+10*time.Second)
	defer stopDrain()
	var messages, drained int
	var receiveErr error
	for {
		data, err := stalled.stream.Receive(drainCtx)
		if err != nil {
			receiveErr = err
			break
		}
		messages++
		drained += len(data)
	}
	t.Logf("stalled viewer drain: messages=%d bytes=%d then %v", messages, drained, receiveErr)
	if drainCtx.Err() != nil {
		t.Fatalf("stalled viewer was not disconnected: still streaming after %d messages", messages)
	}
	if drained > 2<<20 {
		t.Errorf("stalled viewer had %d bytes queued, above the 2 MiB budget", drained)
	}

	// Fresh viewer: pause the child so the screen is stable, then compare the
	// fresh snapshot with the healthy viewer's patched screen.
	if _, err := healthy.roundTrip("PAUSE"); err != nil {
		t.Fatal(err)
	}
	fresh := openViewer(t, ctx, h.gateway, h.credential, session, false)
	_, seq := healthy.identity()
	want := fmt.Sprintf("L%09d", seq)
	waitFor(t, 5*time.Second, "fresh snapshot equality", func() bool {
		a, b := healthy.screenCopy(), fresh.screenCopy()
		return len(b) == childRows && slices.Equal(a[1:], b[1:]) && strings.HasPrefix(b[childRows-1], want)
	})
	if freshPID, _ := fresh.identity(); freshPID != pid {
		t.Fatalf("fresh viewer pid %d, want %d", freshPID, pid)
	}
	t.Logf("fresh viewer snapshot matches canonical screen at line %s, pid=%d", want, pid)
}

// TestSoakSessionLimitAndBrokerKill fills the default eight-session limit,
// expects limit_exceeded for the ninth, frees a slot, then SIGKILLs the
// broker and checks that a new broker reports every prior session lost and
// that no child outlives the dead broker.
func TestSoakSessionLimitAndBrokerKill(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h := startSoakHost(t, ctx, envInt("PTERM_SOAK_RATE", 1<<20))
	start := time.Now()
	t.Logf("idle broker: %s", h.sample(start))
	var created []protocol.BrokerRPCSessionRecord
	for range 8 {
		session, err := h.createSession(ctx, busyProfileID)
		if err != nil {
			t.Fatalf("session %d: %v", len(created)+1, err)
		}
		created = append(created, session)
	}
	_, err := h.createSession(ctx, busyProfileID)
	var remote *gatewayapi.Error
	if !errors.As(err, &remote) || remote.Code != protocol.LimitExceeded {
		t.Fatalf("ninth session error = %v, want limit_exceeded", err)
	}
	t.Logf("ninth session: %v", err)
	waitFor(t, 5*time.Second, "eight children", func() bool { return len(childPIDs(h.broker.Process.Pid)) == 8 })
	time.Sleep(10 * time.Second) // let every history reach its row cap
	t.Logf("8 busy sessions, no viewers: %s", h.sample(start))
	if n := len(h.sessions(ctx)); n != 8 {
		t.Fatalf("listed sessions after rejection = %d", n)
	}

	victim := created[0]
	if _, err := h.gateway.TerminateSession(ctx, h.credential, victim.ID, protocol.HTTPSessionTerminateRequest{
		Generation: victim.Generation, OperationID: newUUID(t),
	}); err != nil {
		t.Fatal(err)
	}
	var replacement protocol.BrokerRPCSessionRecord
	waitFor(t, 10*time.Second, "slot freed", func() bool {
		replacement, err = h.createSession(ctx, busyProfileID)
		return err == nil
	})
	created = append(created[1:], replacement)
	t.Logf("slot reused after terminate: %s", replacement.ID)

	children := childPIDs(h.broker.Process.Pid)
	if len(children) != 8 {
		t.Fatalf("children before kill = %v", children)
	}
	if err := h.broker.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	<-h.brokerDone
	killed := time.Now()
	waitFor(t, 10*time.Second, "children exit after broker death", func() bool {
		return !slices.ContainsFunc(children, processAlive)
	})
	t.Logf("all %d children exited %s after broker SIGKILL", len(children), time.Since(killed).Round(time.Millisecond))
	_ = h.gateway.Close()

	_ = os.Remove(h.metricsPath)
	h.startBroker(ctx)
	states := map[string]protocol.State{}
	for _, record := range h.sessions(ctx) {
		states[record.ID] = record.State
	}
	for _, session := range created {
		if states[session.ID] != protocol.Lost {
			t.Errorf("session %s after broker restart = %q, want lost", session.ID, states[session.ID])
		}
	}
	if n := len(childPIDs(h.broker.Process.Pid)); n != 0 {
		t.Errorf("restarted broker spawned %d children; prior commands must not rerun", n)
	}
	t.Logf("restarted broker lists %d prior live sessions as lost; no children rerun", len(created))
}
