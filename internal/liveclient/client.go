// Package liveclient implements the desktop observer over canonical broker IPC.
// A Client owns only its connection and display, never the managed process.
package liveclient

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/desktop"
	"github.com/intojhanurag/phone-terminal/internal/ipc"
	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	heartbeatInterval  = 5 * time.Second
	requestTimeout     = 5 * time.Second
	maxFrameQueueBytes = 2 * 1024 * 1024
)

var (
	ErrClosed            = errors.New("liveclient: connection closed")
	ErrNotReady          = errors.New("liveclient: current screen is not ready")
	ErrNotController     = errors.New("liveclient: take control before sending input")
	ErrBusy              = errors.New("liveclient: another user action is awaiting delivery")
	ErrDeliveryUncertain = errors.New("Delivery uncertain—check the terminal before sending again")
	ErrGenerationChanged = errors.New("liveclient: session generation changed")
	ErrProtocol          = errors.New("liveclient: invalid host message")
	ErrSlowDisplay       = errors.New("liveclient: display exceeded its bounded queue")
)

// Output must honor cancellation even during WriteContext. Close waits for
// painting to finish before the caller restores its outer terminal. An ordinary
// blocking io.Writer is deliberately not accepted by this boundary.
type Output interface {
	WriteContext(context.Context, []byte) (int, error)
}

type Config struct {
	RuntimeDir  string
	ClientBuild string
	SessionID   string
	Generation  string
	Output      Output
}

// State is public connection state. It never exposes a lease secret.
type State struct {
	Connected              bool
	Ready                  bool
	Controller             bool
	ControllerConnectionID string
	ControllerLabel        string
	LeaseEpoch             string
	Lifecycle              protocol.State
	ExitCode               *int64
	LastError              *RemoteError
}

type RemoteError struct {
	Code    protocol.ErrorCode
	Message string
}

func (err *RemoteError) Error() string {
	return fmt.Sprintf("liveclient: %s: %s", err.Code, displayText(err.Message))
}

func remoteError(code protocol.ErrorCode, message string) *RemoteError {
	return &RemoteError{Code: code, Message: displayText(message)}
}

func displayText(value string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r >= 127 && r < 160 {
			return '\uFFFD'
		}
		return r
	}, value)
}

type pendingRequest struct {
	id        string
	kind      protocol.MessageType
	sequence  string
	epoch     uint64
	lease     string
	result    chan error
	completed bool
	attempted bool
}

type sentRequest struct {
	id    string
	kind  protocol.MessageType
	epoch uint64
	lease string
}
type frameMessage struct {
	envelope protocol.Envelope
	size     int
	serial   uint64
}

type Client struct {
	config       Config
	conn         net.Conn
	reader       *ipc.FrameReader
	writer       *ipc.FrameWriter
	frames       *desktop.FrameClient
	ctx          context.Context
	cancel       context.CancelFunc
	stopParent   func() bool
	wg           sync.WaitGroup
	once         sync.Once
	done         chan struct{}
	changed      chan struct{}
	writeGate    chan struct{}
	userGate     chan struct{}
	frameQueue   chan frameMessage
	readFinished chan error

	mu          sync.Mutex
	state       State
	statePulse  chan struct{}
	err         error
	lease       string
	leaseEpoch  uint64
	sequence    uint64
	pending     *pendingRequest
	recent      []sentRequest
	queuedBytes int
	frameSerial uint64
	detaching   bool
}

// Dial negotiates restricted local CLI access and starts one observer. Use
// WaitReady before explicitly acquiring control. ctx owns the connection's
// lifetime; cancellation interrupts network I/O and the supplied Output.
func Dial(ctx context.Context, config Config) (*Client, error) {
	if err := validateConfig(ctx, config); err != nil {
		return nil, err
	}
	conn, _, welcome, err := ipc.DialBroker(ctx, config.RuntimeDir, protocol.CLI, config.ClientBuild)
	if err != nil {
		return nil, err
	}
	return start(ctx, conn, welcome, config)
}

func validateConfig(ctx context.Context, config Config) error {
	if ctx == nil || config.Output == nil {
		return errors.New("liveclient: context and cancellable output are required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := encode(protocol.Hello, "", "", protocol.HelloPayload{ClientKind: protocol.Desktop,
		ClientBuild: config.ClientBuild, CapabilityProfile: protocol.PhoneTerminalV1, SupportedVersions: []int64{1}})
	if err != nil {
		return err
	}
	_, err = encode(protocol.SessionAttach, config.SessionID, config.Generation, protocol.EmptyPayload{})
	return err
}

// start receives an already peer-checked, IPC-negotiated connection from
// DialBroker. Tests use the same handshake with a synthetic local peer.
func start(parent context.Context, conn net.Conn, welcome protocol.IPCWelcomeClass, config Config) (*Client, error) {
	if err := validateConfig(parent, config); err != nil {
		_ = conn.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	client := &Client{config: config, conn: conn, ctx: ctx, cancel: cancel,
		done: make(chan struct{}), changed: make(chan struct{}, 1), statePulse: make(chan struct{}),
		writeGate: make(chan struct{}, 1), userGate: make(chan struct{}, 1), frameQueue: make(chan frameMessage, 64), readFinished: make(chan error, 1),
		state: State{Connected: true, LeaseEpoch: "0"}}
	client.reader, _ = ipc.NewFrameReader(conn, protocol.MaxOutboundMessageBytes)
	client.writer, _ = ipc.NewFrameWriter(conn, protocol.MaxInboundMessageBytes)
	renderer, _ := desktop.NewRenderer(contextOutput{ctx: ctx, output: config.Output, failed: client.stop})
	client.frames, _ = desktop.NewFrameClient(renderer)
	client.stopParent = context.AfterFunc(parent, func() { client.stop(parent.Err()) })
	hello, err := encode(protocol.Hello, "", "", protocol.HelloPayload{ClientKind: protocol.Desktop,
		ClientBuild: config.ClientBuild, CapabilityProfile: protocol.PhoneTerminalV1, SupportedVersions: []int64{1}})
	if err == nil {
		err = client.write(ctx, hello)
	}
	if err == nil {
		err = conn.SetReadDeadline(time.Now().Add(requestTimeout))
		var data []byte
		if err == nil {
			data, err = client.reader.ReadFrame()
		}
		if clearErr := conn.SetReadDeadline(time.Time{}); err == nil {
			err = clearErr
		}
		if err == nil {
			var response protocol.Envelope
			response, err = protocol.DecodeHost(data)
			if err == nil {
				if response.RequestID != hello.RequestID {
					err = ErrProtocol
				} else if response.Type == protocol.Error {
					payload, _ := protocol.DecodePayload[protocol.ErrorPayload](response)
					err = remoteError(payload.Code, payload.Message)
				} else if response.Type != protocol.Welcome {
					err = ErrProtocol
				} else {
					payload, _ := protocol.DecodePayload[protocol.WelcomePayload](response)
					if payload.BrokerGeneration != welcome.BrokerGeneration {
						err = ErrGenerationChanged
					}
				}
			}
		}
	}
	if err != nil {
		client.stop(err)
		client.stopParent()
		return nil, err
	}
	client.wg.Add(3)
	go client.readLoop()
	go client.paintLoop()
	go client.heartbeatLoop()
	attach, _ := encode(protocol.SessionAttach, config.SessionID, config.Generation, protocol.EmptyPayload{})
	client.remember(attach, "", 0)
	if err := client.write(ctx, attach); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func (client *Client) Done() <-chan struct{} { return client.done }

// Changed is coalesced notification; read State after each notification. No
// callbacks or terminal frames can accumulate in application UI queues.
func (client *Client) Changed() <-chan struct{} { return client.changed }

func (client *Client) State() State {
	client.mu.Lock()
	defer client.mu.Unlock()
	state := client.state
	if state.ExitCode != nil {
		value := *state.ExitCode
		state.ExitCode = &value
	}
	if state.LastError != nil {
		value := *state.LastError
		state.LastError = &value
	}
	return state
}

func (client *Client) Err() error { client.mu.Lock(); defer client.mu.Unlock(); return client.err }

func (client *Client) WaitReady(ctx context.Context) error {
	if ctx == nil {
		return errors.New("liveclient: nil wait context")
	}
	for {
		client.mu.Lock()
		ready, connected, pulse := client.state.Ready, client.state.Connected, client.statePulse
		client.mu.Unlock()
		if !connected {
			return client.closedError()
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-pulse:
		}
	}
}

// Close joins all client I/O, including painting, so terminal restoration by
// the caller cannot be followed by late renderer writes. It never terminates
// the broker session.
func (client *Client) Close() error {
	client.stop(nil)
	client.stopParent()
	client.wg.Wait()
	// A read already completing when cancellation arrived can have raced the
	// painter's final drain. Both producers are now joined.
	for {
		select {
		case frame := <-client.frameQueue:
			client.mu.Lock()
			client.queuedBytes -= frame.size
			client.mu.Unlock()
		default:
			return client.Err()
		}
	}
}

func (client *Client) stop(err error) {
	client.once.Do(func() {
		client.mu.Lock()
		if pending := client.pending; pending != nil && pending.sequence != "" && pending.attempted && !pending.completed {
			err = errors.Join(ErrDeliveryUncertain, err)
		}
		client.err = err
		client.state.Connected, client.state.Ready, client.state.Controller = false, false, false
		client.lease = ""
		client.changedLocked()
		client.mu.Unlock()
		client.cancel()
		_ = client.conn.Close()
		close(client.done)
	})
}

func (client *Client) changedLocked() {
	close(client.statePulse)
	client.statePulse = make(chan struct{})
	select {
	case client.changed <- struct{}{}:
	default:
	}
}

func (client *Client) closedError() error {
	if err := client.Err(); err != nil {
		return err
	}
	return ErrClosed
}

type contextOutput struct {
	ctx    context.Context
	output Output
	failed func(error)
}

func (output contextOutput) Write(data []byte) (int, error) {
	if err := output.ctx.Err(); err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(output.ctx, requestTimeout)
	defer cancel()
	n, err := output.output.WriteContext(ctx, data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		output.failed(err)
	}
	return n, err
}

func encode(kind protocol.MessageType, sessionID, generation string, payload any) (protocol.Envelope, error) {
	id, err := newUUID()
	if err != nil {
		return protocol.Envelope{}, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return protocol.Envelope{}, err
	}
	envelope := protocol.Envelope{Version: protocol.Version, Type: kind, RequestID: id, Payload: data}
	if sessionID != "" {
		envelope.SessionID = &sessionID
	}
	if generation != "" {
		envelope.Generation = &generation
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return protocol.Envelope{}, err
	}
	return protocol.DecodeClient(encoded)
}

func newUUID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	data[6], data[8] = data[6]&15|64, data[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[:4], data[4:6], data[6:8], data[8:10], data[10:]), nil
}

// write serializes complete frames. Cancellation after writing begins closes
// the stream because a partially written frame cannot safely be reused.
func (client *Client) write(ctx context.Context, message protocol.Envelope) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	select {
	case client.writeGate <- struct{}{}:
		defer func() { <-client.writeGate }()
	case <-ctx.Done():
		return ctx.Err()
	case <-client.ctx.Done():
		return client.closedError()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := client.ctx.Err(); err != nil {
		return client.closedError()
	}
	deadline := time.Now().Add(requestTimeout)
	if earlier, ok := ctx.Deadline(); ok && earlier.Before(deadline) {
		deadline = earlier
	}
	if err := client.conn.SetWriteDeadline(deadline); err != nil {
		client.stop(err)
		return err
	}
	client.mu.Lock()
	if !client.state.Connected {
		client.mu.Unlock()
		return client.closedError()
	}
	if pending := client.pending; pending != nil && pending.id == message.RequestID {
		pending.attempted = true
	}
	client.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { client.stop(ctx.Err()) })
	err = client.writer.WriteFrame(data)
	stop()
	if err == nil {
		err = client.conn.SetWriteDeadline(time.Time{})
	}
	if err != nil {
		client.stop(err)
	}
	return err
}
