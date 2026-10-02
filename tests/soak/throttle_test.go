//go:build soak && darwin

package soak

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/intojhanurag/phone-terminal/internal/gateway"
	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// TestSoakThrottledPhones puts phones on slow links behind the real gateway
// WebSocket relay while one session scrolls continuously (P7-SOAK-1). A slow
// phone must stay connected with fewer, coalesced frames that chain exactly,
// the fast phone must keep its frame rate, the broker must stay bounded, and
// every phone must end on the canonical screen. A phone that is disconnected
// reconnects at once, as the web client does, and the test counts it.
func TestSoakThrottledPhones(t *testing.T) {
	duration := envDuration("PTERM_SOAK_THROTTLE_DURATION", time.Minute)
	rate := envInt("PTERM_SOAK_RATE", 1<<20)
	ctx, cancel := context.WithTimeout(context.Background(), duration+3*time.Minute)
	defer cancel()
	h := startSoakHost(t, ctx, rate)
	address := h.startGateway(ctx)
	start := time.Now()
	session, err := h.createSession(ctx, busyProfileID)
	if err != nil {
		t.Fatal(err)
	}

	deflate := envInt("PTERM_SOAK_DEFLATE", 1) != 0
	phones := []*throttledPhone{
		{name: "fast", rate: 0, deflate: deflate},
		{name: "1MiB/s", rate: 1 << 20, deflate: deflate},
		{name: "100KiB/s", rate: 100 << 10, deflate: deflate},
	}
	for index, phone := range phones {
		phone.connect(t, ctx, address, h.credential, session, index == 0)
	}
	controller := phones[0]
	waitFor(t, 5*time.Second, "child pid", func() bool { pid, _ := controller.current.identity(); return pid > 0 })
	pid, _ := controller.current.identity()
	t.Logf("config: rate=%d B/s duration=%s deflate=%t phones=fast,1MiB/s,100KiB/s via gateway %s", rate, duration, deflate, address)

	time.Sleep(5 * time.Second) // warm-up: history fills, connections settle
	for _, phone := range phones {
		phone.collect()
		phone.patches, phone.snapshots, phone.bytes, phone.latencies = 0, 0, 0, nil
		phone.wire.Store(0)
	}
	base := h.sample(start)
	peak := base
	measureFrom := time.Now()
	for time.Since(measureFrom) < duration {
		time.Sleep(250 * time.Millisecond)
		for index, phone := range phones {
			select {
			case <-phone.current.done:
				phone.disconnected(t, ctx, address, h.credential, session, index == 0)
			default:
			}
		}
		if s := h.sample(start); s.rssKiB > peak.rssKiB {
			peak = s
		}
	}
	elapsed := time.Since(measureFrom)
	final := h.sample(start)
	t.Logf("broker before %s", base)
	t.Logf("broker peak   %s", peak)
	t.Logf("broker final  %s", final)

	for _, phone := range phones {
		phone.collect()
		lat := summarize(phone.latencies)
		t.Logf("phone %-8s disconnects=%d frames/s=%.1f snapshots=%d json=%.3f MiB/s wire=%.3f MiB/s staleness %s lastErr=%v",
			phone.name, phone.disconnects, float64(phone.patches)/elapsed.Seconds(), phone.snapshots,
			mib(phone.bytes)/elapsed.Seconds(), mib(phone.wire.Load())/elapsed.Seconds(), lat, phone.lastErr)
		if phone.mismatches != 0 {
			t.Errorf("phone %s: %d patches did not chain from the displayed revision", phone.name, phone.mismatches)
		}
		if phone.disconnects != 0 {
			t.Errorf("phone %s was disconnected %d times", phone.name, phone.disconnects)
		}
	}
	if fps := float64(phones[0].patches) / elapsed.Seconds(); fps < minPatchesPerSecond {
		t.Errorf("fast phone frames/s = %.1f, want at least %d", fps, minPatchesPerSecond)
	}
	fastWire := float64(phones[0].wire.Load()) / elapsed.Seconds()
	for _, phone := range phones[1:] {
		if phone.patches == 0 {
			t.Errorf("phone %s received no frames", phone.name)
		}
		// Where the fast phone's stream exceeds this link, frames must thin out.
		if fastWire > 1.2*float64(phone.rate) && phone.patches >= phones[0].patches {
			t.Errorf("phone %s frames = %d, fast phone %d: want fewer", phone.name, phone.patches, phones[0].patches)
		}
	}
	if growth := float64(peak.rssKiB-base.rssKiB) / 1024; growth > maxStallGrowthMiB {
		t.Errorf("broker RSS grew %.1f MiB with throttled phones (bound %d)", growth, maxStallGrowthMiB)
	}
	if float64(peak.rssKiB)/1024 > maxBrokerRSSMiB {
		t.Errorf("broker RSS %.1f MiB exceeds %d MiB", float64(peak.rssKiB)/1024, maxBrokerRSSMiB)
	}

	// Pause the child; every phone must converge on the canonical screen.
	if _, err := controller.current.roundTrip("PAUSE"); err != nil {
		t.Fatal(err)
	}
	fresh := openViewer(t, ctx, h.gateway, h.credential, session, false)
	_, seq := controller.current.identity()
	want := fmt.Sprintf("L%09d", seq)
	for _, phone := range phones {
		converged := time.Now()
		waitFor(t, 30*time.Second, "phone "+phone.name+" final screen", func() bool {
			a, b := phone.current.screenCopy(), fresh.screenCopy()
			return len(a) == childRows && slices.Equal(a[1:], b[1:]) && (seq == 0 || strings.HasPrefix(a[childRows-1], want))
		})
		if phonePID, _ := phone.current.identity(); phonePID != pid {
			t.Errorf("phone %s pid %d, want %d", phone.name, phonePID, pid)
		}
		t.Logf("phone %-8s final screen matches canonical snapshot at %s (after %s)", phone.name, want,
			time.Since(converged).Round(time.Millisecond))
	}
}

// startGateway serves the real gateway in this process over the test's
// broker client and returns its loopback address.
func (h *soakHost) startGateway(ctx context.Context) string {
	h.t.Helper()
	ready := make(chan gateway.ReadyState, 1)
	gatewayCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- gateway.Run(gatewayCtx, gateway.Config{
			RuntimeDir: h.runtimeDir, HostBuild: "soak", ListenAddress: "127.0.0.1:0",
			Profile: platform.DevelopmentProfile, Broker: h.gateway,
			Ready: func(state gateway.ReadyState) { ready <- state },
		})
	}()
	h.t.Cleanup(func() { stop(); <-done })
	select {
	case state := <-ready:
		return state.Address
	case err := <-done:
		h.t.Fatalf("gateway: %v", err)
	case <-time.After(10 * time.Second):
		h.t.Fatal("gateway did not start")
	}
	return ""
}

// throttledPhone is one phone slot. It reconnects after a disconnect and
// accumulates counters across its connections.
type throttledPhone struct {
	name    string
	rate    int // bytes/s the phone reads; 0 is unthrottled
	deflate bool
	current *viewer
	wire    atomic.Int64 // bytes read from the socket, after compression

	disconnects                           int
	lastErr                               error
	patches, snapshots, bytes, mismatches int64
	latencies                             []time.Duration
}

func (phone *throttledPhone) connect(t *testing.T, ctx context.Context, address, credential string, session protocol.BrokerRPCSessionRecord, control bool) {
	t.Helper()
	stream, err := dialPhone(ctx, address, credential, phone.rate, phone.deflate, &phone.wire)
	if err != nil {
		t.Fatalf("phone %s dial: %v", phone.name, err)
	}
	phone.current = openViewerOn(t, ctx, stream, session, control)
}

func (phone *throttledPhone) disconnected(t *testing.T, ctx context.Context, address, credential string, session protocol.BrokerRPCSessionRecord, control bool) {
	t.Helper()
	phone.disconnects++
	phone.lastErr = phone.current.Err()
	phone.collect()
	t.Logf("phone %s disconnected: %v", phone.name, phone.lastErr)
	phone.connect(t, ctx, address, credential, session, control)
}

func (phone *throttledPhone) collect() {
	v := phone.current
	phone.patches += v.patches.Swap(0)
	phone.snapshots += v.snapshots.Swap(0)
	phone.bytes += v.bytes.Swap(0)
	phone.mismatches += v.baseMismatches.Swap(0)
	phone.latencies = append(phone.latencies, v.takeLatencies()...)
}

// dialPhone opens the phone WebSocket. A throttled phone has a small receive
// buffer and reads at most rate bytes/s, so TCP flow control reaches the
// gateway as it would from a slow cellular link.
func dialPhone(ctx context.Context, address, credential string, rate int, deflate bool, wire *atomic.Int64) (gatewayapi.LiveStream, error) {
	dialer := &net.Dialer{}
	if rate > 0 {
		dialer.Control = func(_, _ string, raw syscall.RawConn) error {
			var sockErr error
			if err := raw.Control(func(fd uintptr) {
				sockErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 32<<10)
			}); err != nil {
				return err
			}
			return sockErr
		}
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &throttledConn{Conn: conn, rate: rate, wire: wire}, nil
	}}
	mode := websocket.CompressionContextTakeover // what browsers offer
	if !deflate {
		mode = websocket.CompressionDisabled
	}
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(dialCtx, "ws://"+address+"/api/v1/ws", &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport}, CompressionMode: mode,
		HTTPHeader: http.Header{"Origin": {"http://" + address}, "Cookie": {"pterm_dev_session=" + credential}},
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(protocol.MaxOutboundMessageBytes)
	return wsStream{conn: conn}, nil
}

// throttledConn counts wire bytes and, with a nonzero rate, reads at most
// rate bytes/s.
type throttledConn struct {
	net.Conn
	rate int
	wire *atomic.Int64
	next time.Time
}

func (conn *throttledConn) Read(buffer []byte) (int, error) {
	if conn.rate == 0 {
		n, err := conn.Conn.Read(buffer)
		conn.wire.Add(int64(n))
		return n, err
	}
	if len(buffer) > 16<<10 {
		buffer = buffer[:16<<10]
	}
	n, err := conn.Conn.Read(buffer)
	conn.wire.Add(int64(n))
	// Up to 50 ms of credit absorbs sleep overshoot without allowing bursts.
	conn.next = later(conn.next, time.Now().Add(-50*time.Millisecond)).Add(time.Duration(n) * time.Second / time.Duration(conn.rate))
	time.Sleep(time.Until(conn.next))
	return n, err
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// wsStream adapts a phone WebSocket to the viewer's stream interface.
type wsStream struct{ conn *websocket.Conn }

func (stream wsStream) Send(ctx context.Context, data []byte) error {
	return stream.conn.Write(ctx, websocket.MessageText, data)
}

func (stream wsStream) Receive(ctx context.Context) ([]byte, error) {
	_, data, err := stream.conn.Read(ctx)
	return data, err
}

func (stream wsStream) Close() error { return stream.conn.CloseNow() }
