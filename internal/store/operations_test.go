package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

const (
	testCreateOperation    = "90000000-0000-4000-8000-000000000001"
	testRenameOperation    = "90000000-0000-4000-8000-000000000002"
	testTerminateOperation = "90000000-0000-4000-8000-000000000003"
)

func TestCreateSessionReceiptReplaysAcrossReopenAndRejectsConflicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "create-receipt.sqlite")
	first := openTestStore(t, path)
	beginBroker(t, first, testBrokerA)
	seedParents(t, first)
	session := runningReceiptSession("70000000-0000-4000-8000-000000000001", "durable")
	fingerprint := sha256.Sum256([]byte("create request"))

	result, replayed, err := first.CreateSessionWithReceipt(
		context.Background(), testCreateOperation, fingerprint, session,
	)
	if err != nil || replayed {
		t.Fatalf("create result = %+v, replayed=%t, err=%v", result, replayed, err)
	}
	if result.SessionID != session.ID || result.Generation != session.Generation || result.State != SessionRunning {
		t.Fatalf("create result = %+v", result)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second := openTestStore(t, path)
	defer second.Close()
	beginBroker(t, second, testBrokerA)
	replayedResult, replayed, err := second.CreateSessionWithReceipt(
		context.Background(), testCreateOperation, fingerprint, session,
	)
	if err != nil || !replayed || replayedResult != result {
		t.Fatalf("durable replay = %+v, replayed=%t, err=%v; want %+v", replayedResult, replayed, err, result)
	}
	conflicting := sha256.Sum256([]byte("different create request"))
	if _, _, err := second.CreateSessionWithReceipt(
		context.Background(), testCreateOperation, conflicting, session,
	); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("create conflict error = %v", err)
	}
	stored, found, err := second.LookupMutation(
		context.Background(), testCreateOperation, OperationSessionCreate, fingerprint,
	)
	if err != nil || !found || stored != result {
		t.Fatalf("stored receipt = %+v, found=%t, err=%v", stored, found, err)
	}
	if _, _, err := second.LookupMutation(
		context.Background(), testCreateOperation, OperationSessionRename, fingerprint,
	); !errors.Is(err, ErrOperationConflict) {
		t.Fatalf("cross-kind conflict error = %v", err)
	}
}

func TestCreateSessionReceiptRollsBackWithRejectedMetadata(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "create-rollback.sqlite"))
	defer store.Close()
	beginBroker(t, store, testBrokerA)
	session := runningReceiptSession("70000000-0000-4000-8000-000000000006", "missing parents")
	fingerprint := sha256.Sum256([]byte("rejected create"))
	if _, _, err := store.CreateSessionWithReceipt(
		context.Background(), testCreateOperation, fingerprint, session,
	); err == nil {
		t.Fatal("create with missing parents unexpectedly succeeded")
	}
	if _, err := store.GetSession(context.Background(), session.ID); !errors.Is(err, ErrSessionMissing) {
		t.Fatalf("rolled-back session error = %v", err)
	}
	if _, found, err := store.LookupMutation(
		context.Background(), testCreateOperation, OperationSessionCreate, fingerprint,
	); err != nil || found {
		t.Fatalf("rolled-back receipt found=%t, err=%v", found, err)
	}
}

func TestMutationReceiptsRequireCanonicalOperationUUIDs(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "operation-id.sqlite"))
	defer store.Close()
	fingerprint := sha256.Sum256([]byte("request"))
	for _, operationID := range []string{
		"named-operation",
		"90000000-0000-4000-8000-00000000000A",
		"90000000000040008000000000000000",
		"90000000-0000-0000-8000-000000000001",
	} {
		if _, _, err := store.LookupMutation(
			context.Background(), operationID, OperationSessionCreate, fingerprint,
		); err == nil {
			t.Fatalf("invalid operation ID %q was accepted", operationID)
		}
	}
}

func TestRenameAndTerminateReceiptsReturnOriginalResults(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "mutation-results.sqlite"))
	defer store.Close()
	beginBroker(t, store, testBrokerA)
	seedParents(t, store)
	session := fixtureSession("70000000-0000-4000-8000-000000000002", testBrokerA, "before")
	if err := store.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	mustTransition(t, store, session, SessionRunning, nil, 1)

	renameFingerprint := sha256.Sum256([]byte("rename to first"))
	renameAt := testTime.Add(2 * time.Second)
	firstRename, replayed, err := store.RenameSessionWithReceipt(
		context.Background(), testRenameOperation, renameFingerprint,
		session.ID, session.Generation, "first title", renameAt,
	)
	if err != nil || replayed {
		t.Fatalf("first rename = %+v, replayed=%t, err=%v", firstRename, replayed, err)
	}
	secondFingerprint := sha256.Sum256([]byte("rename to second"))
	if _, replayed, err := store.RenameSessionWithReceipt(
		context.Background(), "90000000-0000-4000-8000-000000000004", secondFingerprint,
		session.ID, session.Generation, "second title", testTime.Add(3*time.Second),
	); err != nil || replayed {
		t.Fatalf("second rename replayed=%t, err=%v", replayed, err)
	}
	replayedRename, replayed, err := store.RenameSessionWithReceipt(
		context.Background(), testRenameOperation, renameFingerprint,
		session.ID, session.Generation, "first title", testTime.Add(20*time.Second),
	)
	if err != nil || !replayed || replayedRename != firstRename {
		t.Fatalf("rename replay = %+v, replayed=%t, err=%v; want %+v", replayedRename, replayed, err, firstRename)
	}
	metadata, err := store.GetSession(context.Background(), session.ID)
	if err != nil || metadata.Title != "second title" {
		t.Fatalf("metadata after old replay = %+v, err=%v", metadata, err)
	}

	terminateFingerprint := sha256.Sum256([]byte("terminate"))
	terminateAt := testTime.Add(4 * time.Second)
	terminated, replayed, err := store.BeginSessionTerminationWithReceipt(
		context.Background(), testTerminateOperation, terminateFingerprint,
		session.ID, session.Generation, terminateAt,
	)
	if err != nil || replayed || terminated.State != SessionExiting {
		t.Fatalf("termination = %+v, replayed=%t, err=%v", terminated, replayed, err)
	}
	code := int32(0)
	if err := store.TransitionSession(
		context.Background(), session.ID, session.Generation, SessionExited, &code, testTime.Add(5*time.Second),
	); err != nil {
		t.Fatalf("finish termination: %v", err)
	}
	replayedTermination, replayed, err := store.BeginSessionTerminationWithReceipt(
		context.Background(), testTerminateOperation, terminateFingerprint,
		session.ID, session.Generation, testTime.Add(30*time.Second),
	)
	if err != nil || !replayed || replayedTermination != terminated {
		t.Fatalf("termination replay = %+v, replayed=%t, err=%v; want %+v", replayedTermination, replayed, err, terminated)
	}
}

func TestTerminalTransitionKeepsActualEndTimeAfterFutureDatedReceipt(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "terminal-time.sqlite"))
	defer store.Close()
	beginBroker(t, store, testBrokerA)
	seedParents(t, store)
	session := fixtureSession("70000000-0000-4000-8000-000000000007", testBrokerA, "clock")
	if err := store.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	mustTransition(t, store, session, SessionRunning, nil, 1)

	receiptAt := testTime.Add(10 * time.Second)
	if _, _, err := store.BeginSessionTerminationWithReceipt(
		context.Background(), testTerminateOperation, sha256.Sum256([]byte("future terminate")),
		session.ID, session.Generation, receiptAt,
	); err != nil {
		t.Fatalf("begin future-dated termination: %v", err)
	}
	actualEnd := testTime.Add(2 * time.Second)
	exitCode := int32(0)
	if err := store.TransitionSession(
		context.Background(), session.ID, session.Generation, SessionExited, &exitCode, actualEnd,
	); err != nil {
		t.Fatalf("persist actual terminal time: %v", err)
	}
	metadata, err := store.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get terminal session: %v", err)
	}
	if metadata.State != SessionExited || !metadata.UpdatedAt.Equal(receiptAt) ||
		metadata.EndedAt == nil || !metadata.EndedAt.Equal(actualEnd) {
		t.Fatalf("terminal timeline = %+v; want updated=%v ended=%v", metadata, receiptAt, actualEnd)
	}
}

func TestReceiptCapacityProtectsActiveSessionsAndPrunesTerminalHistory(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "receipt-capacity.sqlite"))
	defer store.Close()
	store.operationReceiptLimit = 1
	store.retainedTerminalReceipts = 1
	beginBroker(t, store, testBrokerA)
	seedParents(t, store)

	first := runningReceiptSession("70000000-0000-4000-8000-000000000003", "first")
	if _, _, err := store.CreateSessionWithReceipt(
		context.Background(), testCreateOperation, sha256.Sum256([]byte("first")), first,
	); err != nil {
		t.Fatalf("create protected receipt: %v", err)
	}
	second := fixtureSession("70000000-0000-4000-8000-000000000004", testBrokerA, "second")
	if err := store.CreateSession(context.Background(), second); err != nil {
		t.Fatalf("create second session: %v", err)
	}
	mustTransition(t, store, second, SessionRunning, nil, 1)
	if _, _, err := store.RenameSessionWithReceipt(
		context.Background(), testRenameOperation, sha256.Sum256([]byte("blocked")),
		second.ID, second.Generation, "blocked", testTime.Add(2*time.Second),
	); !errors.Is(err, ErrOperationReceiptLimit) {
		t.Fatalf("active receipt capacity error = %v", err)
	}
	unchanged, err := store.GetSession(context.Background(), second.ID)
	if err != nil || unchanged.Title != "second" {
		t.Fatalf("capacity failure changed metadata = %+v, err=%v", unchanged, err)
	}
	if err := store.TransitionSession(
		context.Background(), first.ID, first.Generation, SessionExited, int32Pointer(0), testTime.Add(3*time.Second),
	); err != nil {
		t.Fatalf("finish first session: %v", err)
	}
	if _, found, err := store.LookupMutation(
		context.Background(), testCreateOperation, OperationSessionCreate, sha256.Sum256([]byte("first")),
	); err != nil || !found {
		t.Fatalf("retained terminal receipt found=%t, err=%v", found, err)
	}
	if _, replayed, err := store.RenameSessionWithReceipt(
		context.Background(), testRenameOperation, sha256.Sum256([]byte("blocked")),
		second.ID, second.Generation, "now accepted", testTime.Add(4*time.Second),
	); err != nil || replayed {
		t.Fatalf("mutation after terminal prune replayed=%t, err=%v", replayed, err)
	}
	if _, found, err := store.LookupMutation(
		context.Background(), testCreateOperation, OperationSessionCreate, sha256.Sum256([]byte("first")),
	); err != nil || found {
		t.Fatalf("capacity-evicted terminal receipt found=%t, err=%v", found, err)
	}
}

func TestConcurrentMutationReceiptWinnerIsDeterministic(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "receipt-race.sqlite"))
	defer store.Close()
	beginBroker(t, store, testBrokerA)
	seedParents(t, store)
	session := fixtureSession("70000000-0000-4000-8000-000000000005", testBrokerA, "before")
	if err := store.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	mustTransition(t, store, session, SessionRunning, nil, 1)

	titles := []string{"left", "right"}
	type outcome struct {
		title string
		err   error
	}
	outcomes := make(chan outcome, len(titles))
	var ready sync.WaitGroup
	ready.Add(len(titles))
	start := make(chan struct{})
	for _, title := range titles {
		title := title
		go func() {
			ready.Done()
			<-start
			result, _, err := store.RenameSessionWithReceipt(
				context.Background(), testRenameOperation, sha256.Sum256([]byte(title)),
				session.ID, session.Generation, title, testTime.Add(2*time.Second),
			)
			outcomes <- outcome{title: result.Title, err: err}
		}()
	}
	ready.Wait()
	close(start)
	var winner string
	conflicts := 0
	for range titles {
		outcome := <-outcomes
		switch {
		case outcome.err == nil:
			winner = outcome.title
		case errors.Is(outcome.err, ErrOperationConflict):
			conflicts++
		default:
			t.Fatalf("race error = %v", outcome.err)
		}
	}
	if winner == "" || conflicts != 1 {
		t.Fatalf("winner=%q conflicts=%d", winner, conflicts)
	}
	metadata, err := store.GetSession(context.Background(), session.ID)
	if err != nil || metadata.Title != winner {
		t.Fatalf("race metadata = %+v, err=%v; winner=%q", metadata, err, winner)
	}
}

func runningReceiptSession(id, title string) SessionMetadata {
	session := fixtureSession(id, testBrokerA, title)
	started := testTime.Add(time.Second)
	session.State = SessionRunning
	session.UpdatedAt = started
	session.StartedAt = &started
	return session
}

func int32Pointer(value int32) *int32 { return &value }
