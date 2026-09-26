package liveclient

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/intojhanurag/phone-terminal/internal/desktop"
)

func TestAdmittedInputRechecksEpochAfterWaitingForWriter(t *testing.T) {
	client, host, _ := readyClient(t)
	acquire(t, client, host, "1")
	epoch := client.State().InputEpoch
	if epoch == 0 {
		t.Fatal("ready controller has no admission epoch")
	}
	client.writeGate <- struct{}{}
	result := make(chan error, 1)
	go func() { result <- client.SendAdmitted(context.Background(), desktop.TextAction{Text: "stale"}, epoch) }()
	deadline := time.Now().Add(time.Second)
	for {
		client.mu.Lock()
		pending := client.pending != nil
		if pending {
			client.state.Ready = false
			client.changedLocked()
			client.state.Ready = true
			client.changedLocked()
		}
		client.mu.Unlock()
		if pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("input did not reach pending writer")
		}
		time.Sleep(time.Millisecond)
	}
	<-client.writeGate
	if err := <-result; !errors.Is(err, ErrNotReady) || errors.Is(err, ErrDeliveryUncertain) {
		t.Fatalf("epoch rejection: %v", err)
	}
	if !client.State().Connected {
		t.Fatal("unattempted stale input closed viewer")
	}
	select {
	case request := <-host.requests:
		t.Fatalf("stale action sent: %s", request.Type)
	default:
	}
	client.mu.Lock()
	sequence := client.sequence
	client.mu.Unlock()
	if sequence != 0 {
		t.Fatalf("unsent action consumed sequence %d", sequence)
	}
}
