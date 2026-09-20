package broker

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	controllerSecretBytes = 32
	ControllerTTL         = 15 * time.Second
)

var (
	ErrGenerationChanged = errors.New("broker: session generation changed")
	ErrNotController     = errors.New("broker: connection is not the controller")
	ErrStaleLease        = errors.New("broker: controller lease is stale")
	ErrInputGap          = errors.New("broker: input sequence contains a gap")
	ErrInputQueueFull    = errors.New("broker: input queue is full")
)

// ControlGrant is returned only to the connection that explicitly acquired
// control. Lease is a secret and must not be broadcast to observers.
type ControlGrant struct {
	ConnectionID string
	DeviceID     string
	Lease        string
	Epoch        uint64
	ExpiresAt    time.Time
}

// ControlState is safe to broadcast because it omits the lease secret.
type ControlState struct {
	ConnectionID string
	DeviceID     string
	Epoch        uint64
	ExpiresAt    time.Time
}

// InputAcceptance records the broker acceptance boundary. Duplicate is true
// when this lease already accepted the sequence and the action must not be
// enqueued again.
type InputAcceptance struct {
	Sequence   uint64
	LeaseEpoch uint64
	Duplicate  bool
}

// Controller owns one session generation's single-writer lease state.
type Controller struct {
	mu         sync.Mutex
	generation string
	random     io.Reader
	now        func() time.Time
	epoch      uint64
	connection string
	device     string
	lease      string
	expiresAt  time.Time
	highWater  uint64
}

func NewController(generation string) (*Controller, error) {
	if generation == "" {
		return nil, errors.New("broker: controller generation must not be empty")
	}
	return &Controller{generation: generation, random: rand.Reader, now: time.Now}, nil
}

func newControllerForTest(generation string, random io.Reader, now func() time.Time) (*Controller, error) {
	controller, err := NewController(generation)
	if err != nil {
		return nil, err
	}
	if random == nil {
		return nil, errors.New("broker: controller random source must not be nil")
	}
	if now == nil {
		return nil, errors.New("broker: controller clock must not be nil")
	}
	controller.random = random
	controller.now = now
	return controller, nil
}

// Acquire serially revokes any existing connection and creates a new lease.
func (controller *Controller) Acquire(generation, connectionID, deviceID string) (ControlGrant, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now()
	if generation != controller.generation {
		return ControlGrant{}, ErrGenerationChanged
	}
	if connectionID == "" {
		return ControlGrant{}, errors.New("broker: controller connection must not be empty")
	}
	if deviceID == "" {
		return ControlGrant{}, errors.New("broker: controller device must not be empty")
	}
	secret := make([]byte, controllerSecretBytes)
	if _, err := io.ReadFull(controller.random, secret); err != nil {
		return ControlGrant{}, fmt.Errorf("broker: create controller lease: %w", err)
	}
	controller.epoch++
	controller.connection = connectionID
	controller.device = deviceID
	controller.lease = base64.RawURLEncoding.EncodeToString(secret)
	controller.expiresAt = now.Add(ControllerTTL)
	controller.highWater = 0
	return ControlGrant{
		ConnectionID: connectionID,
		DeviceID:     deviceID,
		Lease:        controller.lease,
		Epoch:        controller.epoch,
		ExpiresAt:    controller.expiresAt,
	}, nil
}

// Heartbeat renews only the current connection and lease.
func (controller *Controller) Heartbeat(generation, connectionID, deviceID, lease string) (ControlState, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now()
	if err := controller.authorize(generation, connectionID, deviceID, lease, now); err != nil {
		return ControlState{}, err
	}
	controller.expiresAt = now.Add(ControllerTTL)
	return controller.state(), nil
}

// AcceptInput checks authorization immediately before the broker queues an
// action and advances the high-water mark before returning accepted.
func (controller *Controller) AcceptInput(
	generation, connectionID, deviceID, lease string,
	sequence uint64,
) (InputAcceptance, error) {
	return controller.acceptInput(generation, connectionID, deviceID, lease, sequence, true)
}

// acceptInput applies a bounded-queue decision atomically with sequence
// acceptance. A full queue must not advance the high-water mark, otherwise a
// safe client retry would be misclassified as a duplicate and never execute.
func (controller *Controller) acceptInput(
	generation, connectionID, deviceID, lease string,
	sequence uint64,
	queueAvailable bool,
) (InputAcceptance, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now()
	if err := controller.authorize(generation, connectionID, deviceID, lease, now); err != nil {
		return InputAcceptance{}, err
	}
	if sequence == 0 {
		return InputAcceptance{}, ErrInputGap
	}
	if sequence <= controller.highWater {
		return InputAcceptance{Sequence: sequence, LeaseEpoch: controller.epoch, Duplicate: true}, nil
	}
	if sequence != controller.highWater+1 {
		return InputAcceptance{}, ErrInputGap
	}
	if !queueAvailable {
		return InputAcceptance{}, ErrInputQueueFull
	}
	controller.highWater = sequence
	return InputAcceptance{Sequence: sequence, LeaseEpoch: controller.epoch}, nil
}

// Release clears control only when the caller still holds the current lease.
func (controller *Controller) Release(generation, connectionID, deviceID, lease string) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now()
	if err := controller.authorize(generation, connectionID, deviceID, lease, now); err != nil {
		return err
	}
	controller.clear()
	return nil
}

// Disconnect immediately releases a matching controller connection. It is a
// no-op for observers and for a connection whose lease was already revoked.
func (controller *Controller) Disconnect(connectionID string) bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if connectionID == "" || connectionID != controller.connection {
		return false
	}
	controller.clear()
	return true
}

// RevokeDevice immediately releases control when it belongs to deviceID.
func (controller *Controller) RevokeDevice(deviceID string) bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if deviceID == "" || deviceID != controller.device {
		return false
	}
	controller.clear()
	return true
}

// Expire clears a lease whose heartbeat deadline has elapsed.
func (controller *Controller) Expire() bool {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now()
	if controller.connection == "" || now.Before(controller.expiresAt) {
		return false
	}
	controller.clear()
	return true
}

func (controller *Controller) State() ControlState {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	now := controller.now()
	if controller.connection != "" && !now.Before(controller.expiresAt) {
		controller.clear()
	}
	return controller.state()
}

func (controller *Controller) authorize(generation, connectionID, deviceID, lease string, now time.Time) error {
	if generation != controller.generation {
		return ErrGenerationChanged
	}
	if controller.connection == "" || !now.Before(controller.expiresAt) {
		controller.clear()
		return ErrNotController
	}
	if connectionID != controller.connection {
		return ErrNotController
	}
	if deviceID != controller.device {
		return ErrNotController
	}
	if len(lease) != len(controller.lease) || subtle.ConstantTimeCompare([]byte(lease), []byte(controller.lease)) != 1 {
		return ErrStaleLease
	}
	return nil
}

func (controller *Controller) state() ControlState {
	return ControlState{ConnectionID: controller.connection, DeviceID: controller.device, Epoch: controller.epoch, ExpiresAt: controller.expiresAt}
}

func (controller *Controller) clear() {
	controller.connection = ""
	controller.device = ""
	controller.lease = ""
	controller.expiresAt = time.Time{}
	controller.highWater = 0
}
