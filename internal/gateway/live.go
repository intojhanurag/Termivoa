package gateway

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

type liveLimits struct {
	outgoingBudget int
	// readAhead: the relay reads the next broker message only while the
	// outbox (queued plus the message on the wire) holds at most this many
	// bytes. A phone that falls behind stops the broker stream, so the broker's
	// per-viewer worker coalesces to its newest screen instead of the gateway
	// queueing stale ones.
	readAhead int
	// stallTimeout bounds one such pause, and writeTimeout one message write.
	// Either one expiring means the phone made no progress; the relay then
	// disconnects it. Both must stay below broker.DefaultViewerTimeout, so the
	// gateway, not the broker's IPC write deadline, reports a stalled phone.
	stallTimeout time.Duration
	writeTimeout time.Duration
	// sendBuffer caps the kernel send buffer of the phone socket. The gateway
	// listens on loopback only, so a small buffer costs no throughput, and
	// stale frames wait in the broker (which replaces them) instead of in an
	// auto-sized kernel buffer the relay cannot see.
	sendBuffer   int
	pingInterval time.Duration
	pingTimeout  time.Duration
}

var defaultLiveLimits = liveLimits{
	outgoingBudget: 2 * 1024 * 1024,
	readAhead:      64 * 1024,
	stallTimeout:   15 * time.Second,
	writeTimeout:   15 * time.Second,
	sendBuffer:     liveSendBuffer(runtime.GOOS),
	pingInterval:   20 * time.Second,
	pingTimeout:    10 * time.Second,
}

// liveSendBuffer is 16 KiB, except on Linux: its loopback segments are 64
// KiB, and a send buffer below two segments makes every write wait for the
// receiver's delayed ACK (40 ms), which capped a phone at about 2 MB/s.
func liveSendBuffer(goos string) int {
	if goos == "linux" {
		return 128 * 1024
	}
	return 16 * 1024
}

// serveLive authenticates the cookie, binds a broker live stream and relays
// canonical envelope text messages both ways. The broker validates semantics.
func (handler *gatewayHandler) serveLive(response http.ResponseWriter, request *http.Request) {
	if !allowMethod(response, request, http.MethodGet) {
		return
	}
	if !handler.originAllowed(request) {
		writeError(response, http.StatusForbidden, protocol.InvalidRequest, "origin not allowed")
		return
	}
	credential, ok := handler.credential(response, request)
	if !ok {
		return
	}
	if !handler.beginLive() {
		writeError(response, http.StatusServiceUnavailable, protocol.HostUnavailable, "gateway stopping")
		return
	}
	defer handler.live.Done()
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	stopOnShutdown := context.AfterFunc(handler.liveCtx, cancel)
	defer stopOnShutdown()

	if _, err := handler.api.Authenticate(ctx, credential); err != nil {
		handler.respond(response, request, nil, err)
		return
	}
	stream, err := handler.api.OpenLive(ctx, credential)
	if err != nil {
		handler.respond(response, request, nil, err)
		return
	}
	// permessage-deflate without context takeover: every message is
	// compressed alone, so the holder-only lease in control.changed never
	// shares a compression window with attacker-influenced terminal output
	// (a CRIME-style length oracle needs both in one window). The read limit
	// applies to decompressed bytes, and the relay checks outbound sizes
	// before compression.
	conn, err := websocket.Accept(sendBufferLimit{response, handler.liveLimits.sendBuffer}, request,
		&websocket.AcceptOptions{
			OriginPatterns: handler.originPatterns, CompressionMode: websocket.CompressionNoContextTakeover,
		})
	if err != nil {
		_ = stream.Close()
		return
	}
	conn.SetReadLimit(protocol.MaxInboundMessageBytes)
	started := time.Now()
	reason := relay(ctx, conn, stream, handler.liveLimits)
	// One line per phone connection, without identities or terminal content,
	// so unstable connections can be diagnosed from gateway.log.
	// ponytail: the log file is not rotated; one short line per connection.
	log.Printf("live: phone connection ended after %s: %s", time.Since(started).Round(time.Second), reason)
}

// relay runs until either side ends, then closes both, and returns why it
// ended. Close code 0 means the socket closes without a handshake. coder/websocket closes the connection when
// a Read or Write context ends, so the reader uses no context and only the
// writer's context is tied to relay cancellation (dropping a slow phone at once).
func relay(parent context.Context, conn *websocket.Conn, stream gatewayapi.LiveStream, limits liveLimits) string {
	ctx, cancel := context.WithCancel(parent)
	var (
		once   sync.Once
		code   websocket.StatusCode
		reason string
		wait   sync.WaitGroup
	)
	finish := func(closeCode websocket.StatusCode, closeReason string) {
		once.Do(func() { code, reason = closeCode, closeReason })
		cancel()
	}
	out := &outbox{budget: limits.outgoingBudget, ready: make(chan struct{}, 1), drained: make(chan struct{}, 1)}

	wait.Add(3)
	go func() { // browser -> broker; blocking here only slows this phone.
		defer wait.Done()
		for {
			kind, data, err := conn.Read(context.Background())
			if err != nil {
				finish(0, "phone closed or went away")
				return
			}
			if kind != websocket.MessageText {
				finish(websocket.StatusUnsupportedData, "text messages only")
				return
			}
			if !singleJSONObject(data) {
				finish(websocket.StatusInvalidFramePayloadData, "invalid message")
				return
			}
			if err := stream.Send(ctx, data); err != nil {
				if errors.Is(err, gatewayapi.ErrStreamEnded) {
					// The broker ended the stream (access.revoked). Stop
					// reading; Receive ends the outbox after the notice is
					// queued, and the writer flushes it and closes with 1008.
					return
				}
				var rejected *gatewayapi.Error
				if errors.As(err, &rejected) && rejected.Code == protocol.InvalidRequest {
					// Tell the phone why before the writer closes the socket.
					if notice, noticeErr := invalidRequestNotice(rejected.Message); noticeErr == nil && out.push(notice) {
						out.end(websocket.StatusPolicyViolation, "invalid message")
						return
					}
					finish(websocket.StatusInvalidFramePayloadData, "invalid message")
					return
				}
				finish(websocket.StatusTryAgainLater, "host unavailable")
				return
			}
		}
	}()
	go func() { // broker -> outbox; pauses while the phone is behind.
		defer wait.Done()
		var last []byte
		for {
			if !out.waitRoom(ctx, limits.readAhead, limits.stallTimeout) {
				if ctx.Err() == nil {
					finish(websocket.StatusTryAgainLater, "client too slow")
				}
				return
			}
			data, err := stream.Receive(ctx)
			if err != nil {
				out.end(streamEndStatus(last))
				return
			}
			last = data
			if len(data) > protocol.MaxOutboundMessageBytes {
				finish(websocket.StatusInternalError, "oversized host message")
				return
			}
			if !out.push(data) {
				finish(websocket.StatusTryAgainLater, "client too slow")
				return
			}
		}
	}()
	go func() { // liveness
		defer wait.Done()
		ticker := time.NewTicker(limits.pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(context.Background(), limits.pingTimeout)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					finish(0, "phone missed a ping")
					return
				}
			}
		}
	}()

	// Writer: drains the outbox; after the broker stream ends it flushes what
	// remains (such as access.revoked) and closes with the end status.
	for ctx.Err() == nil {
		data, ended := out.pop()
		if data == nil {
			if ended {
				finish(out.endStatus())
				break
			}
			select {
			case <-ctx.Done():
			case <-out.ready:
			}
			continue
		}
		writeCtx, writeCancel := context.WithTimeout(ctx, limits.writeTimeout)
		err := conn.Write(writeCtx, websocket.MessageText, data)
		writeCancel()
		out.written(len(data))
		if err != nil {
			finish(0, "write to phone failed")
		}
	}
	finish(websocket.StatusGoingAway, "gateway stopping")
	_ = stream.Close()
	if code == 0 {
		_ = conn.CloseNow()
	} else {
		_ = conn.Close(code, reason)
	}
	wait.Wait()
	return reason
}

// streamEndStatus maps the broker's last message before its stream ended to
// the phone's close status: 1008 only when access ended (revocation, logout
// or expiry), 1000 when the session ended, and 1013 otherwise.
func streamEndStatus(last []byte) (websocket.StatusCode, string) {
	// The broker stream validated this message; only its type and state matter.
	var envelope struct {
		Type    protocol.MessageType `json:"type"`
		Payload struct {
			State protocol.State `json:"state"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(last, &envelope)
	switch {
	case envelope.Type == protocol.AccessRevoked:
		return websocket.StatusPolicyViolation, "access ended"
	case envelope.Type == protocol.SessionState &&
		(envelope.Payload.State == protocol.Exited || envelope.Payload.State == protocol.Lost):
		return websocket.StatusNormalClosure, "session ended"
	}
	return websocket.StatusTryAgainLater, "host unavailable"
}

// invalidRequestNotice is a canonical host error for a rejected phone message.
// Its request ID is fresh because the rejected message is untrusted.
func invalidRequestNotice(message string) ([]byte, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, err
	}
	id[6], id[8] = id[6]&0x0f|0x40, id[8]&0x3f|0x80
	payload, err := json.Marshal(protocol.ErrorPayload{Code: protocol.InvalidRequest, Message: message})
	if err != nil {
		return nil, err
	}
	requestID := fmt.Sprintf("%x-%x-%x-%x-%x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16])
	notice, err := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: protocol.Error, RequestID: requestID, Payload: payload})
	if err != nil {
		return nil, err
	}
	if _, err := protocol.DecodeHost(notice); err != nil {
		return nil, err
	}
	return notice, nil
}

// sendBufferLimit sets the send buffer of the connection that
// websocket.Accept hijacks.
type sendBufferLimit struct {
	http.ResponseWriter
	bytes int
}

func (writer sendBufferLimit) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("gateway: response does not support hijacking")
	}
	conn, buffered, err := hijacker.Hijack()
	if tcp, ok := conn.(*net.TCPConn); ok && err == nil && writer.bytes > 0 {
		_ = tcp.SetWriteBuffer(writer.bytes)
	}
	return conn, buffered, err
}

// singleJSONObject accepts one UTF-8 JSON object within the inbound limit.
func singleJSONObject(data []byte) bool {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	return len(data) <= protocol.MaxInboundMessageBytes && utf8.Valid(data) &&
		len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(data)
}

// outbox holds broker messages not yet written to the phone, bounded by
// total bytes. Exceeding the budget disconnects the viewer.
type outbox struct {
	mu        sync.Mutex
	queue     [][]byte
	bytes     int // queued plus the message being written
	budget    int
	ended     bool
	endCode   websocket.StatusCode
	endReason string
	ready     chan struct{} // wakes the writer
	drained   chan struct{} // wakes a paused broker reader
}

func (box *outbox) push(data []byte) bool {
	box.mu.Lock()
	defer box.mu.Unlock()
	if box.bytes+len(data) > box.budget {
		return false
	}
	box.queue = append(box.queue, data)
	box.bytes += len(data)
	box.signal()
	return true
}

// end lets the writer flush what is queued, then close with code.
func (box *outbox) end(code websocket.StatusCode, reason string) {
	box.mu.Lock()
	defer box.mu.Unlock()
	if !box.ended {
		box.ended, box.endCode, box.endReason = true, code, reason
	}
	box.signal()
}

func (box *outbox) endStatus() (websocket.StatusCode, string) {
	box.mu.Lock()
	defer box.mu.Unlock()
	return box.endCode, box.endReason
}

func (box *outbox) pop() ([]byte, bool) {
	box.mu.Lock()
	defer box.mu.Unlock()
	if len(box.queue) == 0 {
		return nil, box.ended
	}
	data := box.queue[0]
	box.queue[0] = nil
	box.queue = box.queue[1:]
	return data, false
}

// written releases a popped message once its write has finished or failed.
func (box *outbox) written(size int) {
	box.mu.Lock()
	box.bytes -= size
	box.mu.Unlock()
	select {
	case box.drained <- struct{}{}:
	default:
	}
}

// waitRoom blocks while the outbox holds more than limit bytes. It reports
// false if ctx ends or the outbox does not drain within timeout.
func (box *outbox) waitRoom(ctx context.Context, limit int, timeout time.Duration) bool {
	if box.room(limit) {
		return true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for !box.room(limit) {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-box.drained:
		}
	}
	return true
}

func (box *outbox) room(limit int) bool {
	box.mu.Lock()
	defer box.mu.Unlock()
	return box.bytes <= limit
}

func (box *outbox) signal() {
	select {
	case box.ready <- struct{}{}:
	default:
	}
}
