package gateway

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/platform"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

// startLive serves a development-profile gateway on a real loopback socket.
func startLive(t *testing.T, broker *fakeBroker, configure ...func(*gatewayHandler)) (*gatewayHandler, string) {
	t.Helper()
	server := httptest.NewUnstartedServer(nil)
	bound := server.Listener.Addr().String()
	handler, err := newServer(Config{Profile: platform.DevelopmentProfile, Broker: broker}, bound, uuidC)
	if err != nil {
		t.Fatal(err)
	}
	for _, apply := range configure {
		apply(handler)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(func() {
		handler.stopLive()
		handler.live.Wait()
		server.Close()
	})
	return handler, bound
}

func dialLive(t *testing.T, bound string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws://"+bound+"/api/v1/ws", &websocket.DialOptions{HTTPHeader: header})
}

func liveHeader(bound, credential string) http.Header {
	return http.Header{
		"Origin": {"http://" + bound},
		"Cookie": {developmentCookieName + "=" + credential},
	}
}

func openedStream(t *testing.T, broker *fakeBroker) *fakeStream {
	t.Helper()
	select {
	case stream := <-broker.opened:
		return stream
	case <-time.After(5 * time.Second):
		t.Fatal("broker stream was not opened")
		return nil
	}
}

func waitClosed(t *testing.T, stream *fakeStream) {
	t.Helper()
	select {
	case <-stream.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("broker stream was not closed")
	}
}

func readClose(t *testing.T, conn *websocket.Conn) websocket.StatusCode {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return websocket.CloseStatus(err)
		}
	}
}

func TestLiveRelayRoundTrip(t *testing.T) {
	broker := newFakeBroker()
	_, bound := startLive(t, broker)
	conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	stream := openedStream(t, broker)
	if !broker.called("authenticate") {
		t.Fatal("upgrade did not authenticate first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hello := []byte(`{"version":1,"type":"hello","requestId":"` + uuidA + `","payload":{}}`)
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-stream.fromPhone:
		if !bytes.Equal(got, hello) {
			t.Fatalf("broker received %q", got)
		}
	case <-ctx.Done():
		t.Fatal("hello did not reach the broker")
	}
	welcome := []byte(`{"version":1,"type":"welcome","requestId":"` + uuidA + `","payload":{}}`)
	stream.toPhone <- welcome
	kind, got, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText || !bytes.Equal(got, welcome) {
		t.Fatalf("phone received %v %q %v", kind, got, err)
	}

	// Closing the phone side releases the broker stream.
	_ = conn.Close(websocket.StatusNormalClosure, "")
	waitClosed(t, stream)
}

// An exact configured origin that differs from Host must upgrade even when it
// contains glob metacharacters such as IPv6 brackets.
func TestLiveAcceptsIPv6LoopbackDevOrigin(t *testing.T) {
	broker := newFakeBroker()
	server := httptest.NewUnstartedServer(nil)
	bound := server.Listener.Addr().String()
	handler, err := newServer(Config{Profile: platform.DevelopmentProfile, Broker: broker, Origins: []string{"http://[::1]:5173"}}, bound, uuidC)
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = handler
	server.Start()
	t.Cleanup(func() { handler.stopLive(); handler.live.Wait(); server.Close() })
	header := liveHeader(bound, validCredential)
	header.Set("Origin", "http://[::1]:5173")
	conn, response, err := dialLive(t, bound, header)
	if err != nil {
		t.Fatalf("upgrade = %v %v", response, err)
	}
	conn.CloseNow()
}

func TestLiveRejectsBadUpgrades(t *testing.T) {
	broker := newFakeBroker()
	_, bound := startLive(t, broker)
	for _, test := range []struct {
		name   string
		header http.Header
		status int
	}{
		{"missing origin", http.Header{"Cookie": {developmentCookieName + "=" + validCredential}}, 403},
		{"forged origin", http.Header{"Origin": {"http://evil.example"}, "Cookie": {developmentCookieName + "=" + validCredential}}, 403},
		{"https origin", http.Header{"Origin": {"https://" + bound}, "Cookie": {developmentCookieName + "=" + validCredential}}, 403},
		{"no cookie", http.Header{"Origin": {"http://" + bound}}, 401},
		{"production cookie name", http.Header{"Origin": {"http://" + bound}, "Cookie": {productionCookieName + "=" + validCredential}}, 401},
		{"revoked", liveHeader(bound, revokedCredential), 401},
		{"duplicate cookie", http.Header{"Origin": {"http://" + bound}, "Cookie": {developmentCookieName + "=" + validCredential + "; " + developmentCookieName + "=" + validCredential}}, 401},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn, response, err := dialLive(t, bound, test.header)
			if err == nil {
				conn.CloseNow()
				t.Fatal("upgrade succeeded")
			}
			if response == nil || response.StatusCode != test.status {
				t.Fatalf("response = %v, err = %v", response, err)
			}
		})
	}
	if broker.called("open") && len(broker.opened) != 0 {
		t.Fatal("a rejected upgrade opened a broker stream")
	}
}

func TestLiveClosesOnBadInbound(t *testing.T) {
	for _, test := range []struct {
		name string
		kind websocket.MessageType
		data []byte
		want websocket.StatusCode
	}{
		{"oversize", websocket.MessageText, []byte(`{"p":"` + strings.Repeat("x", protocol.MaxInboundMessageBytes) + `"}`), websocket.StatusMessageTooBig},
		{"binary", websocket.MessageBinary, []byte(`{}`), websocket.StatusUnsupportedData},
		{"array", websocket.MessageText, []byte(`[1]`), websocket.StatusInvalidFramePayloadData},
		{"two values", websocket.MessageText, []byte(`{} {}`), websocket.StatusInvalidFramePayloadData},
		{"invalid utf8", websocket.MessageText, []byte("{\"a\":\"\xff\"}"), websocket.StatusInvalidFramePayloadData},
	} {
		t.Run(test.name, func(t *testing.T) {
			broker := newFakeBroker()
			_, bound := startLive(t, broker)
			conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			stream := openedStream(t, broker)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := conn.Write(ctx, test.kind, test.data); err != nil {
				t.Fatal(err)
			}
			if code := readClose(t, conn); code != test.want {
				t.Fatalf("close code = %d, want %d", code, test.want)
			}
			waitClosed(t, stream)
			if len(stream.fromPhone) != 0 {
				t.Fatal("invalid message reached the broker")
			}
		})
	}
}

func TestLiveBrokerEndFlushesThenClosesWithPolicy(t *testing.T) {
	broker := newFakeBroker()
	_, bound := startLive(t, broker)
	conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	stream := openedStream(t, broker)
	revoked := []byte(`{"version":1,"type":"access.revoked","requestId":"` + uuidA + `","payload":{}}`)
	stream.toPhone <- revoked
	stream.finish()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, got, err := conn.Read(ctx)
	if err != nil || !bytes.Equal(got, revoked) {
		t.Fatalf("phone received %q %v", got, err)
	}
	if code := readClose(t, conn); code != websocket.StatusPolicyViolation {
		t.Fatalf("close code = %d", code)
	}
	waitClosed(t, stream)
}

func TestLiveOversizeBrokerMessageClosesSocket(t *testing.T) {
	broker := newFakeBroker()
	_, bound := startLive(t, broker)
	conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	stream := openedStream(t, broker)
	conn.SetReadLimit(-1)
	stream.toPhone <- []byte(`{"p":"` + strings.Repeat("x", protocol.MaxOutboundMessageBytes) + `"}`)
	if code := readClose(t, conn); code != websocket.StatusInternalError {
		t.Fatalf("close code = %d", code)
	}
	waitClosed(t, stream)
}

func TestLiveSlowPhoneIsDisconnectedWithoutBlockingBroker(t *testing.T) {
	broker := newFakeBroker()
	_, bound := startLive(t, broker)
	conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	stream := openedStream(t, broker)
	// The phone never reads. 64 x 512 KiB far exceeds socket buffers plus the
	// 2 MiB outgoing budget; the fake broker's sends never wait on the phone.
	message := []byte(`{"p":"` + strings.Repeat("x", 512*1024) + `"}`)
	start := time.Now()
	for range cap(stream.toPhone) {
		select {
		case stream.toPhone <- message:
		case <-stream.closed:
		case <-time.After(5 * time.Second):
			t.Fatal("broker send blocked")
		}
	}
	waitClosed(t, stream)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("disconnect took %v", elapsed)
	}
}

func TestLiveShutdownClosesSockets(t *testing.T) {
	broker := newFakeBroker()
	handler, bound := startLive(t, broker)
	conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	stream := openedStream(t, broker)
	handler.stopLive()
	if code := readClose(t, conn); code != websocket.StatusGoingAway {
		t.Fatalf("close code = %d", code)
	}
	waitClosed(t, stream)
	handler.live.Wait()
	if _, response, err := dialLive(t, bound, liveHeader(bound, validCredential)); err == nil ||
		response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("upgrade after stop = %v, %v", response, err)
	}
}

func TestLivePingKeepsResponsivePhone(t *testing.T) {
	broker := newFakeBroker()
	_, bound := startLive(t, broker, func(handler *gatewayHandler) {
		handler.liveLimits.pingInterval = 20 * time.Millisecond
		handler.liveLimits.pingTimeout = time.Second
	})
	conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	stream := openedStream(t, broker)
	readCtx := conn.CloseRead(context.Background()) // answers pings
	select {
	case <-stream.closed:
		t.Fatal("responsive phone was disconnected")
	case <-readCtx.Done():
		t.Fatal("socket closed")
	case <-time.After(200 * time.Millisecond):
	}
}

// A message the broker boundary rejects as malformed reaches the phone as a
// typed error before the socket closes, not as a generic host failure.
func TestLiveRejectedMessageSendsTypedErrorThenCloses(t *testing.T) {
	broker := newFakeBroker()
	_, bound := startLive(t, broker)
	conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	stream := openedStream(t, broker)
	stream.sendErr.Store(&gatewayapi.Error{Code: protocol.InvalidRequest, Message: "message does not match protocol v1"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"version":1,"type":"input.key","payload":{}}`)); err != nil {
		t.Fatal(err)
	}
	kind, data, err := conn.Read(ctx)
	if err != nil || kind != websocket.MessageText {
		t.Fatalf("read notice: %v %v", kind, err)
	}
	envelope, err := protocol.DecodeHost(data)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := protocol.DecodePayload[protocol.ErrorPayload](envelope)
	if err != nil || envelope.Type != protocol.Error || payload.Code != protocol.InvalidRequest {
		t.Fatalf("notice = %s (%v)", data, err)
	}
	if _, _, err := conn.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("close = %v, want policy violation", err)
	}
	waitClosed(t, stream)
}

// A phone message sent after the broker ended the stream must not drop the
// queued backlog and access.revoked: the phone still gets them, then 1008.
func TestLiveSendAfterRevokeStillFlushesNotice(t *testing.T) {
	broker := newFakeBroker()
	_, bound := startLive(t, broker, func(handler *gatewayHandler) {
		handler.liveLimits.outgoingBudget = 64 * 1024 * 1024
	})
	conn, _, err := dialLive(t, bound, liveHeader(bound, validCredential))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(-1)
	stream := openedStream(t, broker)
	// 16 MiB of backlog outlasts loopback socket buffers while the phone
	// does not read, so the writer is still busy when the heartbeat arrives.
	backlog := []byte(`{"p":"` + strings.Repeat("x", 1000*1024) + `"}`)
	for range 16 {
		stream.toPhone <- backlog
	}
	revoked := []byte(`{"version":1,"type":"access.revoked","requestId":"` + uuidA + `","payload":{}}`)
	stream.toPhone <- revoked
	stream.finish()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	heartbeat := []byte(`{"version":1,"type":"heartbeat","requestId":"` + uuidB + `","payload":{}}`)
	if err := conn.Write(ctx, websocket.MessageText, heartbeat); err != nil {
		t.Fatal(err)
	}
	for stream.sends.Load() == 0 {
		if ctx.Err() != nil {
			t.Fatal("heartbeat did not reach the broker stream")
		}
		time.Sleep(time.Millisecond)
	}
	for received := 0; ; received++ {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read after %d backlog messages: %v (close %d)", received, err, websocket.CloseStatus(err))
		}
		if bytes.Equal(data, revoked) {
			if received != 16 {
				t.Fatalf("access.revoked after %d of 16 backlog messages", received)
			}
			break
		}
	}
	if code := readClose(t, conn); code != websocket.StatusPolicyViolation {
		t.Fatalf("close code = %d, want 1008", code)
	}
	waitClosed(t, stream)
}
