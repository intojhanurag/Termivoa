package broker

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

const testGeneration = "33333333-3333-4333-8333-333333333333"

type testClock struct {
	mu    sync.Mutex
	value time.Time
}

func (clock *testClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.value
}

func (clock *testClock) Set(value time.Time) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.value = value
}

func testController(t *testing.T, now time.Time) (*Controller, *testClock) {
	t.Helper()
	clock := &testClock{value: now}
	controller, err := newControllerForTest(testGeneration, bytes.NewReader(testSecrets(8)), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	return controller, clock
}

func TestControllerAcquireRevokesOldConnection(t *testing.T) {
	now := time.Unix(100, 0)
	controller, clock := testController(t, now)
	first, err := controller.Acquire(testGeneration, "connection-a", "device-a")
	if err != nil {
		t.Fatal(err)
	}
	clock.Set(now.Add(time.Second))
	second, err := controller.Acquire(testGeneration, "connection-b", "device-b")
	if err != nil {
		t.Fatal(err)
	}
	if first.Lease == second.Lease || second.Epoch != first.Epoch+1 {
		t.Fatalf("replacement grant = %#v after %#v", second, first)
	}
	clock.Set(now.Add(2 * time.Second))
	if _, err := controller.acceptInput(testGeneration, "connection-a", "device-a", first.Lease, 1, true); !errors.Is(err, ErrNotController) {
		t.Fatalf("old controller input error = %v", err)
	}
	if _, err := controller.acceptInput(testGeneration, "connection-b", "device-b", second.Lease, 1, true); err != nil {
		t.Fatalf("new controller input: %v", err)
	}
}

func TestControllerInputSequenceDeduplicatesAndRejectsGaps(t *testing.T) {
	now := time.Unix(200, 0)
	controller, _ := testController(t, now)
	grant, err := controller.Acquire(testGeneration, "connection", "device")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := controller.acceptInput(testGeneration, "connection", "device", grant.Lease, 1, true)
	if err != nil || accepted.Duplicate {
		t.Fatalf("first acceptance = %#v, %v", accepted, err)
	}
	duplicate, err := controller.acceptInput(testGeneration, "connection", "device", grant.Lease, 1, true)
	if err != nil || !duplicate.Duplicate {
		t.Fatalf("duplicate acceptance = %#v, %v", duplicate, err)
	}
	if _, err := controller.acceptInput(testGeneration, "connection", "device", grant.Lease, 3, true); !errors.Is(err, ErrInputGap) {
		t.Fatalf("gap error = %v", err)
	}
	if accepted, err := controller.acceptInput(testGeneration, "connection", "device", grant.Lease, 2, true); err != nil || accepted.Duplicate {
		t.Fatalf("sequence two acceptance = %#v, %v", accepted, err)
	}
}

func TestControllerHeartbeatExpiryAndRelease(t *testing.T) {
	now := time.Unix(300, 0)
	controller, clock := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")
	clock.Set(now.Add(5 * time.Second))
	state, err := controller.Heartbeat(testGeneration, "connection", "device", grant.Lease)
	if err != nil || !state.ExpiresAt.Equal(now.Add(20*time.Second)) {
		t.Fatalf("heartbeat state = %#v, %v", state, err)
	}
	clock.Set(now.Add(19 * time.Second))
	if controller.Expire() {
		t.Fatal("controller expired before its deadline")
	}
	clock.Set(now.Add(20 * time.Second))
	if !controller.Expire() {
		t.Fatal("controller did not expire at its deadline")
	}
	if _, err := controller.acceptInput(testGeneration, "connection", "device", grant.Lease, 1, true); !errors.Is(err, ErrNotController) {
		t.Fatalf("expired input error = %v", err)
	}

	clock.Set(now.Add(21 * time.Second))
	grant, _ = controller.Acquire(testGeneration, "connection", "device")
	clock.Set(now.Add(22 * time.Second))
	if err := controller.Release(testGeneration, "connection", "device", grant.Lease); err != nil {
		t.Fatal(err)
	}
	if state := controller.State(); state.ConnectionID != "" || state.Epoch != 2 {
		t.Fatalf("released state = %#v", state)
	}
}

func TestControllerRejectsStaleGenerationAndLease(t *testing.T) {
	now := time.Unix(400, 0)
	controller, _ := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")
	if _, err := controller.Heartbeat("changed", "connection", "device", grant.Lease); !errors.Is(err, ErrGenerationChanged) {
		t.Fatalf("generation error = %v", err)
	}
	if _, err := controller.Heartbeat(testGeneration, "connection", "device", "wrong"); !errors.Is(err, ErrStaleLease) {
		t.Fatalf("lease error = %v", err)
	}
	if controller.Disconnect("observer") {
		t.Fatal("observer disconnected controller")
	}
	if !controller.Disconnect("connection") {
		t.Fatal("controller disconnect did not release")
	}
}

func TestControllerChecksExpiryAfterEnteringCriticalSection(t *testing.T) {
	now := time.Unix(450, 0)
	controller, clock := testController(t, now)
	grant, _ := controller.Acquire(testGeneration, "connection", "device")

	controller.mu.Lock()
	result := make(chan error, 1)
	go func() {
		_, err := controller.acceptInput(testGeneration, "connection", "device", grant.Lease, 1, true)
		result <- err
	}()
	clock.Set(now.Add(ControllerTTL))
	controller.mu.Unlock()

	if err := <-result; !errors.Is(err, ErrNotController) {
		t.Fatalf("input crossing expiry error = %v", err)
	}
}

func TestControllerSerializesConcurrentTakeover(t *testing.T) {
	now := time.Unix(500, 0)
	controller, err := newControllerForTest(testGeneration, bytes.NewReader(testSecrets(32)), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	grants := make(chan ControlGrant, 16)
	for index := 0; index < 16; index++ {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			grant, acquireErr := controller.Acquire(testGeneration, string(rune('a'+id)), "device")
			if acquireErr != nil {
				t.Errorf("acquire %d: %v", id, acquireErr)
				return
			}
			grants <- grant
		}(index)
	}
	wait.Wait()
	close(grants)
	epochs := make(map[uint64]bool)
	for grant := range grants {
		epochs[grant.Epoch] = true
	}
	if len(epochs) != 16 || controller.State().Epoch != 16 {
		t.Fatalf("takeover epochs = %d, final %#v", len(epochs), controller.State())
	}
}

func testSecrets(count int) []byte {
	secrets := make([]byte, 0, count*controllerSecretBytes)
	for index := 0; index < count; index++ {
		secrets = append(secrets, bytes.Repeat([]byte{byte(index + 1)}, controllerSecretBytes)...)
	}
	return secrets
}
