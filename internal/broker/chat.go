package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"time"
	"uuid"

	"github.com/intojhanurag/phone-terminal/internal/auth"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
	"github.com/intojhanurag/phone-terminal/internal/ptyhost"
	"github.com/intojhanurag/phone-terminal/internal/transcript"
)

// Chat view (docs/chat-view.md, ADR 0014; protocol revision 8). The broker
// reads the agent's own transcript, read-only, only while a non-thumbnail
// viewer has Chat open. Pull model (review F3): the host sends only a tiny,
// coalesced chat.changed; the phone pulls chat.page. Pages are read off the
// session actor (the files.go pattern) with one request in flight per
// viewer and per session (S6), and wait for the writer budget (F4).

const (
	chatEnabledKey      = "chat.enabled"
	maxChatTails        = 8
	chatPollEvery       = 250 * time.Millisecond
	chatRecheckEvery    = 2 * time.Second
	chatLinger          = 10 * time.Second
	chatSearchingFor    = 10 * time.Second
	chatIdleAfter       = 2 * time.Second
	chatClearCheckEvery = 10 * time.Second
	chatChangedSpacing  = 250 * time.Millisecond // at most 4 chat.changed a second
	chatOpensPerMinute  = 10
	chatUpdatesPerMin   = 10
	chatSendTimeout     = 30 * time.Second
	chatPageTimeout     = 10 * time.Second
	maxChatVersions     = 8
)

var (
	errChatFirst   = errors.New("broker: chat is not open on this stream")
	errChatCursor  = errors.New("broker: chat cursor is not valid for this history")
	errChatBusy    = errors.New("broker: a chat request is running")
	errChatRate    = errors.New("broker: chat opened too often")
	errChatThumb   = errors.New("broker: a thumbnail viewer cannot open chat")
	errChatTurnOff = providerFailure{managementFailure{protocol.InvalidRequest, "Chat view can be turned on only at the computer: run pterm chat on"}}
)

// chatHub is the computer's chat state: the switch, and one tail per
// session shared by its subscribers.
type chatHub struct {
	server *ManagementServer
	config *transcript.Config // nil: Chat is not supported here
	log    io.Writer

	// Test seams.
	processes func(context.Context, ManagedSession) ([]transcript.Process, error)
	poll      time.Duration
	recheck   time.Duration
	linger    time.Duration

	mu       sync.Mutex
	enabled  bool
	tails    map[string]*chatTail
	cleared  map[string]map[string]bool // Termivoa session → agent sessions ended by /clear
	versions []string
	unknown  int
	limits   providerLimits
}

func newChatHub(ctx context.Context, server *ManagementServer, config *transcript.Config) (*chatHub, error) {
	hub := &chatHub{server: server, log: os.Stderr, processes: sessionProcesses,
		poll: chatPollEvery, recheck: chatRecheckEvery, linger: chatLinger,
		tails: map[string]*chatTail{}, cleared: map[string]map[string]bool{}}
	if runtime.GOOS != "windows" && config != nil {
		hub.config = config
	}
	value, found, err := server.config.Store.GetSetting(ctx, chatEnabledKey)
	if err != nil {
		return nil, err
	}
	hub.enabled = found && value == "1"
	return hub, nil
}

// sessionProcesses lists the session's process tree with start times.
func sessionProcesses(ctx context.Context, managed ManagedSession) ([]transcript.Process, error) {
	session, ok := managed.(*Session)
	if !ok {
		return nil, nil
	}
	lister, ok := session.process.(interface {
		TreeProcesses(context.Context) ([]ptyhost.TreeProcess, error)
	})
	if !ok {
		return nil, nil
	}
	tree, err := lister.TreeProcesses(ctx)
	processes := make([]transcript.Process, 0, len(tree))
	for _, process := range tree {
		processes = append(processes, transcript.Process{PID: process.PID, Start: process.Start})
	}
	return processes, err
}

func (hub *chatHub) supported() bool { return hub != nil && hub.config != nil }

func (hub *chatHub) isEnabled() bool {
	if !hub.supported() {
		return false
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.enabled
}

// info is HTTPSettingsResponse.chat; nil where Chat is not supported.
func (hub *chatHub) info() *protocol.HTTPChatInfo {
	if !hub.supported() {
		return nil
	}
	return &protocol.HTTPChatInfo{Enabled: hub.isEnabled()}
}

// setEnabled saves the switch. Off closes every chat at once: each
// subscriber is told chat.state off and every transcript is closed.
func (hub *chatHub) setEnabled(ctx context.Context, enabled bool) error {
	if !hub.supported() {
		return errors.New("broker: chat is not supported on this computer")
	}
	value := "0"
	if enabled {
		value = "1"
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if err := hub.server.config.Store.PutSetting(ctx, chatEnabledKey, value, hub.server.now()); err != nil {
		return err
	}
	hub.enabled = enabled
	if !enabled {
		for _, tail := range hub.tails {
			tail.stop(true)
		}
		clear(hub.tails)
	}
	return nil
}

// status is admin.chat.status.result.
func (hub *chatHub) status() protocol.AdminRPCChatStatusPayload {
	status := protocol.AdminRPCChatStatusPayload{Supported: hub.supported(), Versions: []string{}}
	if hub == nil {
		return status
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	status.Enabled = hub.supported() && hub.enabled
	status.Tails = int64(len(hub.tails))
	status.Versions = append(status.Versions, hub.versions...)
	status.UnknownPercent = int64(hub.unknown)
	return status
}

// noteFormat records the agent version and unknown share for pterm doctor.
func (hub *chatHub) noteFormat(version string, unknown float64) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if version != "" && !slices.Contains(hub.versions, version) {
		hub.versions = append(hub.versions, version)
		if len(hub.versions) > maxChatVersions {
			hub.versions = hub.versions[1:]
		}
	}
	hub.unknown = min(max(int(unknown*100+0.5), 0), 100)
}

// tailFor returns the session's tail, starting one when there is room.
func (hub *chatHub) tailFor(sessionID string, session ManagedSession) (*chatTail, bool) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if tail, ok := hub.tails[sessionID]; ok {
		return tail, true
	}
	if len(hub.tails) >= maxChatTails {
		return nil, false
	}
	ctx, cancel := context.WithCancel(context.Background())
	tail := &chatTail{hub: hub, id: sessionID, session: session, ctx: ctx, cancel: cancel,
		subs: map[*chatSub]bool{}, link: protocol.ChatLinkSearching, bound: make(chan struct{})}
	hub.tails[sessionID] = tail
	go tail.run()
	return tail, true
}

func (hub *chatHub) markCleared(sessionID, agentSession string) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.cleared[sessionID] == nil {
		if len(hub.cleared) >= 64 {
			clear(hub.cleared)
		}
		hub.cleared[sessionID] = map[string]bool{}
	}
	hub.cleared[sessionID][agentSession] = true
}

func (hub *chatHub) wasCleared(sessionID, agentSession string) bool {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	return hub.cleared[sessionID][agentSession]
}

// chatTail follows one session's transcript for its subscribers.
type chatTail struct {
	hub     *chatHub
	id      string
	session ManagedSession
	ctx     context.Context
	cancel  context.CancelFunc
	busy    atomic.Bool // a page request of this session is running
	bound   chan struct{}

	mu        sync.Mutex
	subs      map[*chatSub]bool
	link      string
	reason    string
	file      *transcript.Transcript
	searching time.Time
	stopped   bool
	idleCheck time.Time
	lingering *time.Timer
}

func (tail *chatTail) state() protocol.ChatStatePayload {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	return tail.stateLocked()
}

func (tail *chatTail) stateLocked() protocol.ChatStatePayload {
	if tail.stopped {
		return protocol.ChatStatePayload{Link: protocol.ChatLinkOff}
	}
	state := protocol.ChatStatePayload{Link: tail.link}
	if tail.link == protocol.ChatLinkNone {
		state.Reason = ptrTo(tail.reason)
	}
	if (tail.link == protocol.ChatLinkLinked || tail.link == protocol.ChatLinkEnded) && tail.file != nil {
		state.Agent, state.HistoryID = ptrTo(protocol.Claude), ptrTo(tail.file.History())
	}
	return state
}

func ptrTo[T any](value T) *T { return &value }

// subscribe adds a viewer; nil when the tail stopped meanwhile.
func (tail *chatTail) subscribe(stream *sessionConnection) *chatSub {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	if tail.stopped {
		return nil
	}
	if tail.lingering != nil {
		tail.lingering.Stop()
		tail.lingering = nil
	}
	sub := &chatSub{tail: tail, stream: stream, kick: make(chan struct{}, 1), done: make(chan struct{})}
	tail.subs[sub] = true
	go sub.run()
	return sub
}

// unsubscribe removes sub at once; the tail stops 10 s after its last one.
func (tail *chatTail) unsubscribe(sub *chatSub) {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	if !tail.subs[sub] {
		return
	}
	delete(tail.subs, sub)
	sub.closeOnce.Do(func() { close(sub.done) })
	if len(tail.subs) == 0 && !tail.stopped && tail.lingering == nil {
		tail.lingering = time.AfterFunc(tail.hub.linger, tail.expire)
	}
}

func (tail *chatTail) expire() {
	tail.hub.mu.Lock()
	defer tail.hub.mu.Unlock()
	tail.mu.Lock()
	idle := len(tail.subs) == 0 && tail.lingering != nil
	tail.mu.Unlock()
	if idle && tail.hub.tails[tail.id] == tail {
		delete(tail.hub.tails, tail.id)
		tail.stop(false)
	}
}

// stop ends the tail: no more reads, the file is closed. With off, every
// subscriber is sent chat.state off first. The hub lock is held.
func (tail *chatTail) stop(off bool) {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	if tail.stopped {
		return
	}
	tail.stopped = true
	tail.cancel()
	if tail.lingering != nil {
		tail.lingering.Stop()
	}
	for sub := range tail.subs {
		if off {
			sub.final.Store(true)
			sub.wake(false)
		} else {
			sub.closeOnce.Do(func() { close(sub.done) })
		}
	}
	clear(tail.subs)
	if tail.file != nil {
		_ = tail.file.Close()
		tail.file = nil
	}
}

// broadcast tells every subscriber what changed.
func (tail *chatTail) broadcast(state bool) {
	tail.mu.Lock()
	defer tail.mu.Unlock()
	for sub := range tail.subs {
		sub.wake(state)
	}
}

// run is the tail's loop: poll every 250 ms while linked, recheck or bind
// every 2 s. It holds no file and does no work once stopped.
func (tail *chatTail) run() {
	poll := time.NewTicker(tail.hub.poll)
	defer poll.Stop()
	lastCheck := time.Time{}
	for {
		if time.Since(lastCheck) >= tail.hub.recheck {
			lastCheck = time.Now()
			tail.check()
		}
		tail.pollOnce()
		select {
		case <-tail.ctx.Done():
			return
		case <-poll.C:
		}
	}
}

func (tail *chatTail) pollOnce() {
	tail.mu.Lock()
	file, link := tail.file, tail.link
	tail.mu.Unlock()
	if file == nil || link != protocol.ChatLinkLinked && link != protocol.ChatLinkEnded {
		return
	}
	result, err := file.Poll()
	switch {
	case err != nil || result.Truncated:
		tail.rebind()
	case result.Rewound:
		tail.broadcast(true)
	case result.Grew:
		tail.hub.noteFormat(file.Version(), file.Unknown())
		if file.FormatUnknown() {
			tail.setLink(nil, protocol.ChatLinkNone, protocol.ChatReasonFormatUnknown)
			return
		}
		tail.broadcast(false)
	}
}

// check binds when not linked and rechecks when linked (design 4.3, 11).
func (tail *chatTail) check() {
	defer func() {
		select {
		case <-tail.bound:
		default:
			close(tail.bound)
		}
	}()
	tail.mu.Lock()
	file, link, reason := tail.file, tail.link, tail.reason
	tail.mu.Unlock()
	if link == protocol.ChatLinkNone && (reason == protocol.ChatReasonFormatUnknown || reason == protocol.ChatReasonFileRefused) {
		return // fails closed until Chat is opened again
	}
	ctx, cancel := context.WithTimeout(tail.ctx, chatPageTimeout)
	defer cancel()
	processes, err := tail.hub.processes(ctx, tail.session)
	if err != nil {
		return
	}
	if file != nil && link == protocol.ChatLinkLinked {
		now := time.Now()
		idle := file.IdleFor(now) >= chatIdleAfter
		tail.mu.Lock()
		clearCheck := idle && now.Sub(tail.idleCheck) >= chatClearCheckEvery
		if clearCheck {
			tail.idleCheck = now
		}
		tail.mu.Unlock()
		switch file.Recheck(*tail.hub.config, processes, clearCheck) {
		case transcript.RecheckEnded:
			tail.setLink(file, protocol.ChatLinkEnded, "")
		case transcript.RecheckRebind:
			tail.rebind()
		case transcript.RecheckCleared:
			tail.hub.markCleared(tail.id, file.SessionID)
			tail.setLink(nil, protocol.ChatLinkNone, protocol.ChatReasonCleared)
		}
		return
	}
	bound := transcript.Bind(ctx, *tail.hub.config, processes)
	if bound.Transcript != nil && tail.hub.wasCleared(tail.id, bound.Transcript.SessionID) {
		// The pid file still names the conversation /clear ended.
		_ = bound.Transcript.Close()
		bound = transcript.Link{State: protocol.ChatLinkNone, Reason: protocol.ChatReasonCleared}
	}
	if link == protocol.ChatLinkEnded && bound.Transcript == nil {
		return // keep the ended transcript for paging until another agent starts
	}
	if bound.State == protocol.ChatLinkSearching {
		tail.mu.Lock()
		if tail.searching.IsZero() {
			tail.searching = time.Now()
		}
		late := time.Since(tail.searching) >= chatSearchingFor
		tail.mu.Unlock()
		if late {
			bound = transcript.Link{State: protocol.ChatLinkNone, Reason: protocol.ChatReasonNoFile}
		}
	} else {
		tail.mu.Lock()
		tail.searching = time.Time{}
		tail.mu.Unlock()
	}
	tail.setLink(bound.Transcript, bound.State, bound.Reason)
}

func (tail *chatTail) rebind() {
	tail.setLink(nil, protocol.ChatLinkSearching, "")
	tail.check()
}

// setLink replaces the link; a changed state or file is broadcast.
func (tail *chatTail) setLink(file *transcript.Transcript, link, reason string) {
	tail.mu.Lock()
	if tail.stopped {
		tail.mu.Unlock()
		if file != nil {
			_ = file.Close()
		}
		return
	}
	changed := tail.link != link || tail.reason != reason || tail.file != file
	if tail.file != nil && tail.file != file {
		_ = tail.file.Close()
	}
	tail.file, tail.link, tail.reason = file, link, reason
	tail.mu.Unlock()
	if file != nil {
		tail.hub.noteFormat(file.Version(), file.Unknown())
	}
	if changed {
		tail.broadcast(true)
	}
}

// page reads one page through the linked transcript.
func (tail *chatTail) page(request transcript.PageRequest) (protocol.ChatPagePayload, bool, error) {
	tail.mu.Lock()
	file := tail.file
	linked := file != nil && (tail.link == protocol.ChatLinkLinked || tail.link == protocol.ChatLinkEnded)
	tail.mu.Unlock()
	if !linked {
		return protocol.ChatPagePayload{}, false, nil
	}
	page, err := file.Page(request)
	if errors.Is(err, transcript.ErrCursor) {
		return page, true, errChatCursor
	}
	return page, true, err
}

// chatSub is one viewer's subscription. Its sender goroutine sends the
// coalesced chat.state and chat.changed: at most one of each waits.
type chatSub struct {
	tail      *chatTail
	stream    *sessionConnection
	kick      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	final     atomic.Bool // send chat.state off, then end
	state     atomic.Bool
	changed   atomic.Bool
}

func (sub *chatSub) wake(state bool) {
	if state {
		sub.state.Store(true)
	} else if !sub.final.Load() {
		sub.changed.Store(true)
	}
	select {
	case sub.kick <- struct{}{}:
	default:
	}
}

func (sub *chatSub) run() {
	stream := sub.stream
	var revoked <-chan struct{}
	if stream.access != nil {
		revoked = stream.access.Revoked()
	}
	viewer := stream.viewer
	for {
		select {
		case <-sub.kick:
		case <-sub.done:
			return
		case <-viewer.ctx.Done():
			sub.tail.unsubscribe(sub)
			return
		case <-revoked:
			sub.tail.unsubscribe(sub) // in step with revocation (review S8)
			return
		}
		if stream.revoked() {
			sub.tail.unsubscribe(sub)
			return
		}
		envelope := protocol.Envelope{RequestID: uuid.New().String(), SessionID: &stream.sessionID, Generation: &stream.generation}
		if sub.final.Load() {
			_ = stream.sendWhenRoom(viewer.ctx, envelope, protocol.ChatState, protocol.ChatStatePayload{Link: protocol.ChatLinkOff})
			sub.closeOnce.Do(func() { close(sub.done) })
			return
		}
		if sub.state.Swap(false) {
			if stream.sendWhenRoom(viewer.ctx, envelope, protocol.ChatState, sub.tail.state()) != nil {
				continue
			}
			envelope.RequestID = uuid.New().String()
		}
		if sub.changed.Swap(false) {
			state := sub.tail.state()
			if state.HistoryID == nil {
				continue
			}
			if stream.revoked() {
				continue
			}
			_ = stream.sendWhenRoom(viewer.ctx, envelope, protocol.ChatChanged, protocol.ChatChangedPayload{HistoryID: *state.HistoryID})
			select {
			case <-time.After(chatChangedSpacing):
			case <-sub.done:
				return
			}
		}
	}
}

// chatRequest handles chat.open, chat.read and chat.close.
func (stream *sessionConnection) chatRequest(ctx context.Context, request protocol.Envelope) error {
	hub := stream.server.chat
	if request.Type == protocol.ChatClose {
		stream.closeChat()
		return nil
	}
	if stream.thumbnail {
		return stream.chatFailure(ctx, request, errChatThumb)
	}
	if request.Type == protocol.ChatOpen && !hub.limits.take("open", stream.deviceID, chatOpensPerMinute, time.Now()) {
		return stream.chatFailure(ctx, request, errChatRate)
	}
	if request.Type == protocol.ChatRead && stream.chatSub == nil {
		return stream.chatFailure(ctx, request, errChatFirst)
	}
	if !stream.chatBusy.CompareAndSwap(false, true) {
		return stream.chatFailure(ctx, request, errChatBusy)
	}
	if request.Type == protocol.ChatOpen {
		return stream.openChat(ctx, request)
	}
	tail := stream.chatSub.tail
	if !tail.busy.CompareAndSwap(false, true) {
		stream.chatBusy.Store(false)
		return stream.chatFailure(ctx, request, errChatBusy)
	}
	payload, _ := protocol.DecodePayload[protocol.ChatReadPayload](request)
	pageRequest := transcript.PageRequest{Turns: int(payload.Limit)}
	if payload.Before != nil {
		pageRequest.Before = *payload.Before
	} else if payload.After != nil {
		pageRequest.After = *payload.After
	}
	go stream.sendPage(tail, request, pageRequest)
	return nil
}

func (stream *sessionConnection) openChat(ctx context.Context, request protocol.Envelope) error {
	hub := stream.server.chat
	payload, _ := protocol.DecodePayload[protocol.ChatOpenPayload](request)
	_, provider := stream.session.(*providerSession)
	if !hub.supported() || provider || !hub.isEnabled() {
		stream.chatBusy.Store(false)
		state := protocol.ChatStatePayload{Link: protocol.ChatLinkUnsupported}
		if hub.supported() && !provider {
			state.Link = protocol.ChatLinkOff
		}
		return stream.send(ctx, request, protocol.ChatState, state)
	}
	stream.closeChat()
	var tail *chatTail
	// A tail that ended its linger right after the lookup is replaced.
	for attempt := 0; attempt < 2 && stream.chatSub == nil; attempt++ {
		var ok bool
		if tail, ok = hub.tailFor(stream.sessionID, stream.session); !ok {
			stream.chatBusy.Store(false)
			return stream.send(ctx, request, protocol.ChatState,
				protocol.ChatStatePayload{Link: protocol.ChatLinkNone, Reason: ptrTo(protocol.ChatReasonBusy)})
		}
		stream.chatSub = tail.subscribe(stream)
	}
	if stream.chatSub == nil {
		stream.chatBusy.Store(false)
		return stream.chatFailure(ctx, request, errChatBusy)
	}
	// The audit line (review S10): device, Termivoa session, time. Never a
	// path, a pid or the agent's session ID.
	fmt.Fprintf(hub.log, "broker: chat open device=%s session=%s at=%s\n",
		stream.deviceID, stream.sessionID, time.Now().UTC().Format(time.RFC3339))
	if !tail.busy.CompareAndSwap(false, true) {
		stream.chatBusy.Store(false)
		return stream.chatFailure(ctx, request, errChatBusy)
	}
	go func() {
		select {
		case <-tail.bound:
		case <-stream.viewer.ctx.Done():
		case <-time.After(chatPageTimeout):
		}
		state := tail.state()
		envelope := request
		if stream.sendWhenRoom(stream.viewer.ctx, envelope, protocol.ChatState, state) != nil ||
			state.Link != protocol.ChatLinkLinked && state.Link != protocol.ChatLinkEnded {
			tail.busy.Store(false)
			stream.chatBusy.Store(false)
			return
		}
		stream.sendPage(tail, request, transcript.PageRequest{Turns: int(payload.Limit)})
	}()
	return nil
}

// sendPage reads and sends one page off the actor, then frees both slots.
func (stream *sessionConnection) sendPage(tail *chatTail, request protocol.Envelope, pageRequest transcript.PageRequest) {
	page, linked, err := tail.page(pageRequest)
	// Free the slots before replying: the client may ask again at once.
	tail.busy.Store(false)
	stream.chatBusy.Store(false)
	ctx := stream.viewer.ctx
	if ctx.Err() != nil || stream.revoked() {
		return
	}
	switch {
	case errors.Is(err, errChatCursor):
		_ = stream.chatFailure(ctx, request, err)
	case err != nil:
		_ = stream.send(ctx, request, protocol.Error, protocol.ErrorPayload{Code: protocol.HostUnavailable, Message: "the transcript could not be read"})
	case !linked:
		_ = stream.sendWhenRoom(ctx, request, protocol.ChatState, tail.state())
	default:
		if err := stream.sendWhenRoom(ctx, request, protocol.ChatPage, page); err != nil && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) {
			_ = stream.send(ctx, request, protocol.Error, protocol.ErrorPayload{Code: protocol.InternalError, Message: "the chat page could not be sent"})
		}
	}
}

func (stream *sessionConnection) closeChat() {
	if sub := stream.chatSub; sub != nil {
		stream.chatSub = nil
		sub.tail.unsubscribe(sub)
	}
}

func (stream *sessionConnection) chatFailure(ctx context.Context, request protocol.Envelope, err error) error {
	code, message := protocol.LimitExceeded, "a chat request is already running"
	switch {
	case errors.Is(err, errChatThumb):
		code, message = protocol.NotController, "a thumbnail viewer is read-only; attach without thumbnail to open Chat"
	case errors.Is(err, errChatRate):
		message = "Chat was opened too often; wait a minute"
	case errors.Is(err, errChatFirst):
		code, message = protocol.InvalidRequest, "open Chat first"
	case errors.Is(err, errChatCursor):
		code, message = protocol.HistoryChanged, "the chat changed; open Chat again"
	}
	return stream.send(ctx, request, protocol.Error, protocol.ErrorPayload{Code: code, Message: message})
}

// sendWhenRoom encodes and validates a host message, then waits for room in
// the viewer's byte budget instead of failing as a slow viewer (review F4).
func (stream *sessionConnection) sendWhenRoom(ctx context.Context, request protocol.Envelope, kind protocol.MessageType, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	message, err := json.Marshal(protocol.Envelope{Version: protocol.Version, Type: kind, RequestID: request.RequestID,
		SessionID: request.SessionID, Generation: request.Generation, Payload: data})
	if err != nil {
		return err
	}
	if _, err := protocol.DecodeHost(message); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, chatSendTimeout)
	defer cancel()
	return stream.writer.SendWhenRoom(ctx, message)
}

// dispatchChatUpdate is gateway.chat.update: any paired device may turn
// Chat off on this computer, never on (review S1).
func (server *ManagementServer) dispatchChatUpdate(
	ctx context.Context, request protocol.GatewayRPCEnvelope, authorize func(string) (auth.Principal, error),
) (any, error) {
	payload, err := protocol.DecodeRPCPayload[protocol.GatewayRPCChatUpdatePayload](request)
	if err != nil {
		return nil, err
	}
	principal, err := authorize(payload.Credential)
	if err != nil {
		return nil, err
	}
	if payload.Request.Enabled {
		return nil, errChatTurnOff
	}
	if !server.chat.supported() {
		return nil, errProviderUnknown
	}
	if !server.chat.limits.take("update", principal.DeviceID, chatUpdatesPerMin, server.now()) {
		return nil, errProviderRate
	}
	if err := server.chat.setEnabled(ctx, false); err != nil {
		return nil, err
	}
	return *server.chat.info(), nil
}

// dispatchAdminChat is admin.chat.status and admin.chat.set (pterm chat).
func (server *ManagementServer) dispatchAdminChat(ctx context.Context, request protocol.AdminRPCEnvelope) (any, error) {
	if request.Type == protocol.AdminChatSet {
		payload, err := protocol.DecodeRPCPayload[protocol.AdminRPCChatSetPayload](request)
		if err != nil {
			return nil, err
		}
		if !server.chat.supported() {
			return server.chat.status(), nil
		}
		if err := server.chat.setEnabled(ctx, payload.Enabled); err != nil {
			return nil, err
		}
	}
	return server.chat.status(), nil
}
