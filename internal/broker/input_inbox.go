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

type InputKind string

const (
	InputKindKey   InputKind = "key"
	InputKindText  InputKind = "text"
	InputKindPaste InputKind = "paste"
	InputKindMouse InputKind = "mouse"
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

func (inbox *InputInbox) Submit(
	generation, connectionID, deviceID, lease string,
	sequence uint64,
	action InputAction,
) (InputAcceptance, error) {
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
		inbox.mu.Lock()
		if inbox.closed {
			inbox.mu.Unlock()
			return AcceptedInput{}, ErrInputQueueClosed
		}
		if len(inbox.queue) > 0 {
			input := inbox.queue[0]
			inbox.queuedBytes -= input.byteSize
			inbox.queue[0] = AcceptedInput{}
			inbox.queue = inbox.queue[1:]
			inbox.mu.Unlock()
			return input, nil
		}
		notify := inbox.notify
		inbox.mu.Unlock()
		select {
		case <-ctx.Done():
			return AcceptedInput{}, ctx.Err()
		case <-notify:
		}
	}
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
	switch action.Kind {
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
