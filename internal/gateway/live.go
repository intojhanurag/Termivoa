package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"

	"github.com/intojhanurag/phone-terminal/internal/gatewayapi"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

type liveLimits struct {
	outgoingBudget int
	writeTimeout   time.Duration
	pingInterval   time.Duration
	pingTimeout    time.Duration
}

var defaultLiveLimits = liveLimits{
	outgoingBudget: 2 * 1024 * 1024,
	writeTimeout:   10 * time.Second,
	pingInterval:   20 * time.Second,
	pingTimeout:    10 * time.Second,
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
	conn, err := websocket.Accept(response, request, &websocket.AcceptOptions{OriginPatterns: handler.originPatterns})
	if err != nil {
		_ = stream.Close()
		return
	}
	conn.SetReadLimit(protocol.MaxInboundMessageBytes)
	relay(ctx, conn, stream, handler.liveLimits)
}

// relay runs until either side ends, then closes both. Close code 0 means the
// socket closes without a handshake. coder/websocket closes the connection when
// a Read or Write context ends, so the reader uses no context and only the
// writer's context is tied to relay cancellation (dropping a slow phone at once).
func relay(parent context.Context, conn *websocket.Conn, stream gatewayapi.LiveStream, limits liveLimits) {
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
	out := &outbox{budget: limits.outgoingBudget, ready: make(chan struct{}, 1)}

	wait.Add(3)
	go func() { // browser -> broker; blocking here only slows this phone.
		defer wait.Done()
		for {
			kind, data, err := conn.Read(context.Background())
			if err != nil {
				finish(0, "")
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
				var rejected *gatewayapi.Error
				if errors.As(err, &rejected) && rejected.Code == protocol.InvalidRequest {
					// Tell the phone why before the writer closes the socket.
					if notice, noticeErr := invalidRequestNotice(rejected.Message); noticeErr == nil && out.push(notice) {
						out.end()
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
	go func() { // broker -> outbox; never blocks on the phone.
		defer wait.Done()
		for {
			data, err := stream.Receive(ctx)
			if err != nil {
				out.end()
				return
			}
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
					finish(0, "")
					return
				}
			}
		}
	}()

	// Writer: drains the outbox; after the broker stream ends it flushes what
	// remains (such as access.revoked) and closes with a policy code.
	for ctx.Err() == nil {
		data, ended := out.pop()
		if data == nil {
			if ended {
				finish(websocket.StatusPolicyViolation, "access ended")
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
		if err != nil {
			finish(0, "")
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

// singleJSONObject accepts one UTF-8 JSON object within the inbound limit.
func singleJSONObject(data []byte) bool {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	return len(data) <= protocol.MaxInboundMessageBytes && utf8.Valid(data) &&
		len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(data)
}

// outbox holds broker messages not yet written to the phone, bounded by
// total bytes. Exceeding the budget disconnects the viewer.
type outbox struct {
	mu     sync.Mutex
	queue  [][]byte
	bytes  int
	budget int
	ended  bool
	ready  chan struct{}
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

func (box *outbox) end() {
	box.mu.Lock()
	defer box.mu.Unlock()
	box.ended = true
	box.signal()
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
	box.bytes -= len(data)
	return data, false
}

func (box *outbox) signal() {
	select {
	case box.ready <- struct{}{}:
	default:
	}
}
