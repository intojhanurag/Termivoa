package broker

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

const (
	DefaultInputQueueCapacity = 128
	MaxInputQueueCapacity     = 1024
	MaxInputQueueBytes        = 2 * 1024 * 1024
)

var ErrInputQueueClosed = errors.New("broker: input queue is closed")

var readyInput = func() <-chan struct{} {
	ready := make(chan struct{})
	close(ready)
	return ready
}()

type InputKind string

const (
	InputKindKey   InputKind = "key"
	InputKindText  InputKind = "text"
	InputKindPaste InputKind = "paste"
	InputKindMouse InputKind = "mouse"
	// InputKindAnswer answers the detected agent question. The actor derives
	// the keys from the screen when it writes, never from the client.
	InputKindAnswer InputKind = "answer"
)

// InputAction is the immutable broker representation of one validated v1
// input message. Fields that do not belong to Kind must retain their zero
// values.
type InputAction struct {
	Kind      InputKind
	Key       protocol.KeyName
	Character *string
	Text      string
	Modifiers protocol.Modifiers
	MouseKind protocol.Kind
	Button    protocol.Button
	Column    int64
	Row       int64
	// QuestionID and OptionIndex belong to InputKindAnswer only.
	QuestionID  string
	OptionIndex int
}

// AcceptedInput records receipt by the broker. It is not a claim that the
// child process executed the action.
type AcceptedInput struct {
	ConnectionID string
	DeviceID     string
	LeaseEpoch   uint64
	Sequence     uint64
	Action       InputAction
	byteSize     int
	// access is rechecked by the actor immediately before encoding. Nil means
	// a local desktop connection inside the current-user boundary.
	access AccessCheck
}

// InputInbox is the bounded ingress to a session actor. It serializes
// capacity checks with controller sequence acceptance so rejected input can be
// safely retried without creating a sequence hole.
type InputInbox struct {
	mu          sync.Mutex
	controller  *Controller
	queue       []AcceptedInput
	capacity    int
	queuedBytes int
	notify      chan struct{}
	closed      bool
}

func NewInputInbox(controller *Controller, capacity int) (*InputInbox, error) {
	if controller == nil {
		return nil, errors.New("broker: input inbox controller must not be nil")
	}
	if capacity <= 0 || capacity > MaxInputQueueCapacity {
		return nil, fmt.Errorf("broker: input inbox capacity must be between 1 and %d", MaxInputQueueCapacity)
	}
	return &InputInbox{controller: controller, capacity: capacity, notify: make(chan struct{})}, nil
}

func (inbox *InputInbox) submit(identity MutationIdentity, sequence uint64, action InputAction) (InputAcceptance, error) {
	generation, connectionID, deviceID, lease := identity.Generation, identity.ConnectionID, identity.DeviceID, identity.Lease
	validated, err := validatedInputAction(action)
	if err != nil {
		return InputAcceptance{}, err
	}

	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if inbox.closed {
		return InputAcceptance{}, ErrInputQueueClosed
	}
	actionBytes := inputActionBytes(validated)
	acceptance, err := inbox.controller.acceptInput(
		generation,
		connectionID,
		deviceID,
		lease,
		sequence,
		len(inbox.queue) < inbox.capacity && inbox.queuedBytes+actionBytes <= MaxInputQueueBytes,
	)
	if err != nil || acceptance.Duplicate {
		return acceptance, err
	}
	inbox.queue = append(inbox.queue, AcceptedInput{
		ConnectionID: connectionID,
		DeviceID:     deviceID,
		LeaseEpoch:   acceptance.LeaseEpoch,
		Sequence:     sequence,
		Action:       validated,
		byteSize:     actionBytes,
		access:       identity.Access,
	})
	inbox.queuedBytes += actionBytes
	inbox.signal()
	return acceptance, nil
}

func (inbox *InputInbox) Next(ctx context.Context) (AcceptedInput, error) {
	for {
		if err := ctx.Err(); err != nil {
			return AcceptedInput{}, err
		}
		if input, ok, err := inbox.TryNext(); err != nil || ok {
			return input, err
		}
		select {
		case <-ctx.Done():
			return AcceptedInput{}, ctx.Err()
		case <-inbox.Ready():
		}
	}
}

// Ready reports when TryNext can make progress. The returned channel is
// already closed while input is queued or after the inbox is closed, avoiding
// a missed wakeup between checking the queue and beginning a select.
func (inbox *InputInbox) Ready() <-chan struct{} {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if inbox.closed || len(inbox.queue) > 0 {
		return readyInput
	}
	return inbox.notify
}

// TryNext removes one accepted action without blocking. It exists so the
// session actor can select input alongside PTY output and control requests;
// it must remain the only consumer for that session's inbox.
func (inbox *InputInbox) TryNext() (AcceptedInput, bool, error) {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if inbox.closed {
		return AcceptedInput{}, false, ErrInputQueueClosed
	}
	if len(inbox.queue) == 0 {
		return AcceptedInput{}, false, nil
	}
	input := inbox.queue[0]
	inbox.queuedBytes -= input.byteSize
	inbox.queue[0] = AcceptedInput{}
	inbox.queue = inbox.queue[1:]
	return input, true, nil
}

// RevokeDevice releases the device's controller lease and removes accepted
// actions that have not entered the session actor. An action already returned
// by Next is in flight and cannot be retracted.
func (inbox *InputInbox) RevokeDevice(deviceID string) int {
	if deviceID == "" {
		return 0
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	inbox.controller.RevokeDevice(deviceID)
	kept := inbox.queue[:0]
	dropped := 0
	for _, input := range inbox.queue {
		if input.DeviceID == deviceID {
			inbox.queuedBytes -= input.byteSize
			dropped++
			continue
		}
		kept = append(kept, input)
	}
	for index := len(kept); index < len(inbox.queue); index++ {
		inbox.queue[index] = AcceptedInput{}
	}
	inbox.queue = kept
	return dropped
}

// DropConnection removes accepted actions that have not entered the session
// actor. It does not mutate controller state, allowing callers to coordinate a
// takeover or release without accidentally revoking a newly granted lease for
// the same device.
func (inbox *InputInbox) DropConnection(connectionID string) int {
	if connectionID == "" {
		return 0
	}
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	kept := inbox.queue[:0]
	dropped := 0
	for _, input := range inbox.queue {
		if input.ConnectionID == connectionID {
			inbox.queuedBytes -= input.byteSize
			dropped++
			continue
		}
		kept = append(kept, input)
	}
	for index := len(kept); index < len(inbox.queue); index++ {
		inbox.queue[index] = AcceptedInput{}
	}
	inbox.queue = kept
	return dropped
}

// DropBeforeLeaseEpoch removes input accepted under superseded controller
// leases. It is used after acquisition even when Controller.State has already
// cleared an expired controller and no longer exposes its connection ID.
func (inbox *InputInbox) DropBeforeLeaseEpoch(epoch uint64) int {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	kept := inbox.queue[:0]
	dropped := 0
	for _, input := range inbox.queue {
		if input.LeaseEpoch < epoch {
			inbox.queuedBytes -= input.byteSize
			dropped++
			continue
		}
		kept = append(kept, input)
	}
	for index := len(kept); index < len(inbox.queue); index++ {
		inbox.queue[index] = AcceptedInput{}
	}
	inbox.queue = kept
	return dropped
}

func (inbox *InputInbox) Close() {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	if inbox.closed {
		return
	}
	inbox.closed = true
	for index := range inbox.queue {
		inbox.queue[index] = AcceptedInput{}
	}
	inbox.queue = nil
	inbox.queuedBytes = 0
	inbox.signal()
}

func (inbox *InputInbox) signal() {
	close(inbox.notify)
	inbox.notify = make(chan struct{})
}

func inputActionBytes(action InputAction) int {
	const metadataBytes = 128
	return metadataBytes + len(action.Text)
}

func validatedInputAction(action InputAction) (InputAction, error) {
	validated := action
	if action.Kind != InputKindAnswer && (action.QuestionID != "" || action.OptionIndex != 0) {
		return InputAction{}, errors.New("broker: input contains answer fields")
	}
	switch action.Kind {
	case InputKindAnswer:
		if action != (InputAction{Kind: InputKindAnswer, QuestionID: action.QuestionID, OptionIndex: action.OptionIndex}) {
			return InputAction{}, errors.New("broker: answer input contains unrelated fields")
		}
		if !protocol.ValidAgentQuestionID(action.QuestionID) || action.OptionIndex < 0 || action.OptionIndex >= protocol.MaxAgentOptions {
			return InputAction{}, errors.New("broker: invalid answer input")
		}
	case InputKindKey:
		if action.Text != "" || action.MouseKind != "" || action.Button != "" || action.Column != 0 || action.Row != 0 {
			return InputAction{}, errors.New("broker: key input contains unrelated fields")
		}
		if err := protocol.ValidateKeyInput(action.Key, action.Character, action.Modifiers); err != nil {
			return InputAction{}, fmt.Errorf("broker: invalid key input: %w", err)
		}
		if action.Character != nil {
			character := *action.Character
			validated.Character = &character
		}
	case InputKindText:
		if action.Key != "" || action.Character != nil || action.MouseKind != "" || action.Button != "" || action.Column != 0 || action.Row != 0 || action.Modifiers != (protocol.Modifiers{}) {
			return InputAction{}, errors.New("broker: text input contains unrelated fields")
		}
		if err := protocol.ValidateTextInput(action.Text); err != nil {
			return InputAction{}, fmt.Errorf("broker: invalid text input: %w", err)
		}
	case InputKindPaste:
		if action.Key != "" || action.Character != nil || action.MouseKind != "" || action.Button != "" || action.Column != 0 || action.Row != 0 || action.Modifiers != (protocol.Modifiers{}) {
			return InputAction{}, errors.New("broker: paste input contains unrelated fields")
		}
		if err := protocol.ValidatePasteInput(action.Text); err != nil {
			return InputAction{}, fmt.Errorf("broker: invalid paste input: %w", err)
		}
	case InputKindMouse:
		if action.Key != "" || action.Character != nil || action.Text != "" {
			return InputAction{}, errors.New("broker: mouse input contains unrelated fields")
		}
		if err := protocol.ValidateMouseInput(action.MouseKind, action.Button, action.Column, action.Row); err != nil {
			return InputAction{}, fmt.Errorf("broker: invalid mouse input: %w", err)
		}
	default:
		return InputAction{}, errors.New("broker: invalid input kind")
	}
	return validated, nil
}
