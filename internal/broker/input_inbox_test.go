package broker

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/protocol"
)

func TestInputInboxPreservesOrderAndCopiesInput(t *testing.T) {
	now := time.Unix(600, 0)
	controller, _ := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")
	inbox, err := NewInputInbox(controller, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()

	character := "c"
	first := InputAction{Kind: InputKindKey, Key: protocol.Character, Character: &character, Modifiers: protocol.Modifiers{Ctrl: true}}
	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 1, first); err != nil {
		t.Fatal(err)
	}
	character = "x"
	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 2, InputAction{Kind: InputKindText, Text: "hello"}); err != nil {
		t.Fatal(err)
	}

	gotFirst, err := inbox.Next(context.Background())
	if err != nil || gotFirst.Sequence != 1 || gotFirst.Action.Character == nil || *gotFirst.Action.Character != "c" {
		t.Fatalf("first input = %#v, %v", gotFirst, err)
	}
	gotSecond, err := inbox.Next(context.Background())
	if err != nil || gotSecond.Sequence != 2 || gotSecond.Action.Text != "hello" {
		t.Fatalf("second input = %#v, %v", gotSecond, err)
	}
}

func TestInputInboxFullDoesNotAdvanceSequence(t *testing.T) {
	now := time.Unix(700, 0)
	controller, _ := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")
	inbox, _ := NewInputInbox(controller, 1)
	defer inbox.Close()
	action := InputAction{Kind: InputKindText, Text: "one"}

	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 1, action); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 2, action); !errors.Is(err, ErrInputQueueFull) {
		t.Fatalf("full queue error = %v", err)
	}
	duplicate, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 1, action)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate while full = %#v, %v", duplicate, err)
	}
	if _, err := inbox.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if accepted, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 2, action); err != nil || accepted.Duplicate {
		t.Fatalf("retry after drain = %#v, %v", accepted, err)
	}
}

func TestInputInboxRejectsInvalidInputBeforeSequenceAcceptance(t *testing.T) {
	now := time.Unix(800, 0)
	controller, _ := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")
	inbox, _ := NewInputInbox(controller, 1)
	defer inbox.Close()

	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 1, InputAction{Kind: InputKindText, Text: "\x03"}); err == nil {
		t.Fatal("invalid control text was accepted")
	}
	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 1, InputAction{Kind: InputKindPaste, Text: "safe\n"}); err != nil {
		t.Fatalf("valid retry after rejected input: %v", err)
	}
}

func TestInputInboxCloseAndCancellation(t *testing.T) {
	controller, _ := testController(t, time.Unix(900, 0))
	inbox, _ := NewInputInbox(controller, 1)
	inbox.Close()
	inbox.Close()
	if _, err := inbox.Submit(testGeneration, "connection", "device", "lease", 1, InputAction{Kind: InputKindText, Text: "x"}); !errors.Is(err, ErrInputQueueClosed) {
		t.Fatalf("submit after close error = %v", err)
	}
	if _, err := inbox.Next(context.Background()); !errors.Is(err, ErrInputQueueClosed) {
		t.Fatalf("next after close error = %v", err)
	}

	active, _ := NewInputInbox(controller, 1)
	defer active.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := active.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled next error = %v", err)
	}
}

func TestInputInboxDropsQueuedInputForRevokedDevice(t *testing.T) {
	now := time.Unix(1000, 0)
	controller, _ := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "revoked-device")
	inbox, _ := NewInputInbox(controller, 2)
	defer inbox.Close()
	if _, err := inbox.Submit(testGeneration, "connection", "revoked-device", grant.Lease, 1, InputAction{Kind: InputKindText, Text: "do not run"}); err != nil {
		t.Fatal(err)
	}
	if dropped := inbox.RevokeDevice("revoked-device"); dropped != 1 {
		t.Fatalf("dropped inputs = %d", dropped)
	}
	if _, err := inbox.Submit(testGeneration, "connection", "revoked-device", grant.Lease, 2, InputAction{Kind: InputKindText, Text: "still blocked"}); !errors.Is(err, ErrNotController) {
		t.Fatalf("revoked device submit error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inbox.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("empty inbox after revocation error = %v", err)
	}
}

func TestInputInboxRevocationPreservesOtherDeviceInput(t *testing.T) {
	now := time.Unix(1050, 0)
	controller, _ := testController(t, now)
	first, _ := controller.Acquire(testGeneration, "connection-a", "device-a")
	inbox, _ := NewInputInbox(controller, 4)
	defer inbox.Close()
	if _, err := inbox.Submit(testGeneration, "connection-a", "device-a", first.Lease, 1, InputAction{Kind: InputKindText, Text: "drop"}); err != nil {
		t.Fatal(err)
	}
	second, _ := controller.Acquire(testGeneration, "connection-b", "device-b")
	if _, err := inbox.Submit(testGeneration, "connection-b", "device-b", second.Lease, 1, InputAction{Kind: InputKindText, Text: "keep"}); err != nil {
		t.Fatal(err)
	}
	if dropped := inbox.RevokeDevice("device-a"); dropped != 1 {
		t.Fatalf("dropped inputs = %d", dropped)
	}
	input, err := inbox.Next(context.Background())
	if err != nil || input.DeviceID != "device-b" || input.Action.Text != "keep" {
		t.Fatalf("preserved input = %#v, %v", input, err)
	}
	if _, err := controller.Heartbeat(testGeneration, "connection-b", "device-b", second.Lease); err != nil {
		t.Fatalf("other device lease was revoked: %v", err)
	}
}

func TestInputInboxCloseAbortsQueuedInput(t *testing.T) {
	now := time.Unix(1100, 0)
	controller, _ := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")
	inbox, _ := NewInputInbox(controller, 1)
	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 1, InputAction{Kind: InputKindText, Text: "queued"}); err != nil {
		t.Fatal(err)
	}
	inbox.Close()
	if _, err := inbox.Next(context.Background()); !errors.Is(err, ErrInputQueueClosed) {
		t.Fatalf("next after abortive close error = %v", err)
	}
}

func TestInputInboxCanceledContextDoesNotConsumeReadyInput(t *testing.T) {
	now := time.Unix(1200, 0)
	controller, _ := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")
	inbox, _ := NewInputInbox(controller, 1)
	defer inbox.Close()
	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 1, InputAction{Kind: InputKindText, Text: "queued"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inbox.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ready input with canceled context error = %v", err)
	}
	if input, err := inbox.Next(context.Background()); err != nil || input.Sequence != 1 {
		t.Fatalf("input after canceled read = %#v, %v", input, err)
	}
}

func TestInputInboxEnforcesCapacityMaximum(t *testing.T) {
	controller, _ := testController(t, time.Unix(1300, 0))
	if _, err := NewInputInbox(controller, MaxInputQueueCapacity+1); err == nil {
		t.Fatal("oversized input inbox was accepted")
	}
}

func TestInputInboxEnforcesAggregateByteBudget(t *testing.T) {
	now := time.Unix(1400, 0)
	controller, _ := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")
	inbox, _ := NewInputInbox(controller, MaxInputQueueCapacity)
	defer inbox.Close()
	paste := InputAction{Kind: InputKindPaste, Text: string(bytes.Repeat([]byte{'a'}, protocol.MaxPasteBytes))}
	sequence := uint64(1)
	for {
		_, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, sequence, paste)
		if errors.Is(err, ErrInputQueueFull) {
			break
		}
		if err != nil {
			t.Fatalf("submit %d: %v", sequence, err)
		}
		sequence++
	}
	if sequence >= uint64(MaxInputQueueCapacity) {
		t.Fatalf("byte budget did not bind before item capacity: sequence %d", sequence)
	}
	if _, err := inbox.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, sequence, paste); err != nil {
		t.Fatalf("retry after freeing byte budget: %v", err)
	}
}

func TestInputInboxReadyAndTryNextDoNotMissWakeup(t *testing.T) {
	now := time.Unix(1500, 0)
	controller, _ := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")
	inbox, _ := NewInputInbox(controller, 2)
	defer inbox.Close()

	wakeup := inbox.Ready()
	select {
	case <-wakeup:
		t.Fatal("empty inbox reported ready")
	default:
	}
	if _, err := inbox.Submit(testGeneration, "connection", "device", grant.Lease, 1, InputAction{Kind: InputKindText, Text: "ready"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wakeup:
	case <-time.After(time.Second):
		t.Fatal("input submission did not wake waiter")
	}
	input, ok, err := inbox.TryNext()
	if err != nil || !ok || input.Action.Text != "ready" {
		t.Fatalf("try next = %#v, %v, %v", input, ok, err)
	}
	if _, ok, err := inbox.TryNext(); err != nil || ok {
		t.Fatalf("empty try next = ok %v, err %v", ok, err)
	}
}

func TestInputInboxDropsOnlyMatchingConnection(t *testing.T) {
	now := time.Unix(1600, 0)
	controller, _ := testController(t, now)
	first, _ := controller.Acquire(testGeneration, "connection-a", "device-a")
	inbox, _ := NewInputInbox(controller, 4)
	defer inbox.Close()
	if _, err := inbox.Submit(testGeneration, "connection-a", "device-a", first.Lease, 1, InputAction{Kind: InputKindText, Text: "drop"}); err != nil {
		t.Fatal(err)
	}
	second, _ := controller.Acquire(testGeneration, "connection-b", "device-a")
	if _, err := inbox.Submit(testGeneration, "connection-b", "device-a", second.Lease, 1, InputAction{Kind: InputKindText, Text: "keep"}); err != nil {
		t.Fatal(err)
	}
	if dropped := inbox.DropConnection("connection-a"); dropped != 1 {
		t.Fatalf("dropped inputs = %d", dropped)
	}
	input, ok, err := inbox.TryNext()
	if err != nil || !ok || input.ConnectionID != "connection-b" || input.Action.Text != "keep" {
		t.Fatalf("preserved input = %#v, %v, %v", input, ok, err)
	}
}

func TestInputInboxDropsExpiredLeaseEpochOnTakeover(t *testing.T) {
	now := time.Unix(1700, 0)
	controller, clock := testController(t, now)
	first, _ := controller.Acquire(testGeneration, "connection-a", "device-a")
	inbox, _ := NewInputInbox(controller, 2)
	defer inbox.Close()
	if _, err := inbox.Submit(testGeneration, "connection-a", "device-a", first.Lease, 1, InputAction{Kind: InputKindText, Text: "expired"}); err != nil {
		t.Fatal(err)
	}
	clock.Set(first.ExpiresAt)
	if state := controller.State(); state.ConnectionID != "" {
		t.Fatalf("expired controller state = %#v", state)
	}
	second, err := controller.Acquire(testGeneration, "connection-b", "device-b")
	if err != nil {
		t.Fatal(err)
	}
	if dropped := inbox.DropBeforeLeaseEpoch(second.Epoch); dropped != 1 {
		t.Fatalf("dropped expired inputs = %d", dropped)
	}
	if _, ok, err := inbox.TryNext(); err != nil || ok {
		t.Fatalf("expired input survived takeover: ok=%v err=%v", ok, err)
	}
}
