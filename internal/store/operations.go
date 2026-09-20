package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultRetainedTerminalReceipts = 8192
	defaultOperationReceiptLimit    = 16384
)

// SessionCreateFingerprint identifies the immutable client request for a
// saved-project/shell-profile create operation. Resolved titles, paths,
// executables, arguments, and environment are mutable broker-owned metadata
// and must not change the identity of a durable retry.
func SessionCreateFingerprint(projectID, shellProfileID string) ([sha256.Size]byte, error) {
	payload, err := json.Marshal(struct {
		ProjectID      string `json:"projectId"`
		ShellProfileID string `json:"shellProfileId"`
	}{ProjectID: projectID, ShellProfileID: shellProfileID})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("store: fingerprint session create: %w", err)
	}
	return sha256.Sum256(payload), nil
}

// LookupMutation returns a previously committed result. A reused operation ID
// with a different kind or request fingerprint is always a conflict, including
// after the original session has changed state.
func (store *Store) LookupMutation(
	ctx context.Context,
	operationID string,
	kind OperationKind,
	fingerprint [sha256.Size]byte,
) (MutationResult, bool, error) {
	if err := validateMutation(operationID, kind); err != nil {
		return MutationResult{}, false, err
	}
	result, storedFingerprint, found, err := readMutation(ctx, store.database, operationID)
	if err != nil {
		return MutationResult{}, false, err
	}
	if !found {
		return MutationResult{}, false, nil
	}
	if result.Kind != kind || storedFingerprint != fingerprint {
		return MutationResult{}, false, ErrOperationConflict
	}
	return result, true, nil
}

// CreateSessionWithReceipt atomically records an already-started runtime's
// metadata and its durable operation result. Callers must close the runtime if
// this transaction fails; no session row or receipt is committed partially.
func (store *Store) CreateSessionWithReceipt(
	ctx context.Context,
	operationID string,
	fingerprint [sha256.Size]byte,
	session SessionMetadata,
) (MutationResult, bool, error) {
	broker, err := store.activeBroker()
	if err != nil {
		return MutationResult{}, false, err
	}
	if err := validateMutation(operationID, OperationSessionCreate); err != nil {
		return MutationResult{}, false, err
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult{}, false, fmt.Errorf("store: begin receipt-backed session create: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	if result, replayed, err := matchingMutation(ctx, transaction, operationID, OperationSessionCreate, fingerprint); replayed || err != nil {
		return result, replayed, err
	}
	if session.BrokerGeneration != broker {
		return MutationResult{}, false, ErrGenerationChanged
	}
	if err := validateSession(session); err != nil {
		return MutationResult{}, false, err
	}
	if session.State != SessionRunning || session.StartedAt == nil || session.EndedAt != nil || session.ExitCode != nil {
		return MutationResult{}, false, errors.New("store: a receipt-backed new session must be running without lifecycle completion fields")
	}
	if err := store.reserveMutationCapacity(ctx, transaction); err != nil {
		return MutationResult{}, false, err
	}
	if err := insertSession(ctx, transaction, session); err != nil {
		return MutationResult{}, false, fmt.Errorf("store: create receipt-backed session metadata: %w", err)
	}
	storedSession, err := readMutationSession(ctx, transaction, session.ID)
	if err != nil {
		return MutationResult{}, false, err
	}
	result := MutationResult{
		OperationID: operationID,
		Kind:        OperationSessionCreate,
		SessionID:   session.ID,
		Generation:  session.Generation,
		State:       SessionRunning,
		CommittedAt: fromMillis(millis(session.UpdatedAt)),
		Session:     mutationSessionSnapshot(storedSession),
	}
	if err := insertMutation(ctx, transaction, fingerprint, result); err != nil {
		return MutationResult{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return MutationResult{}, false, fmt.Errorf("store: commit receipt-backed session create: %w", err)
	}
	return result, false, nil
}

// RenameSessionWithReceipt changes the durable title and records the exact
// acknowledgement in one transaction.
func (store *Store) RenameSessionWithReceipt(
	ctx context.Context,
	operationID string,
	fingerprint [sha256.Size]byte,
	id, generation, title string,
	now time.Time,
) (MutationResult, bool, error) {
	broker, err := store.activeBroker()
	if err != nil {
		return MutationResult{}, false, err
	}
	if err := validateMutation(operationID, OperationSessionRename); err != nil {
		return MutationResult{}, false, err
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult{}, false, fmt.Errorf("store: begin receipt-backed session rename: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	if result, replayed, err := matchingMutation(ctx, transaction, operationID, OperationSessionRename, fingerprint); replayed || err != nil {
		return result, replayed, err
	}
	if err := validateSessionMutation(id, generation, now); err != nil {
		return MutationResult{}, false, err
	}
	if len(title) == 0 || len(title) > 256 || strings.IndexByte(title, 0) >= 0 {
		return MutationResult{}, false, errors.New("store: session title must be 1..256 bytes without NUL")
	}
	if err := store.reserveMutationCapacity(ctx, transaction); err != nil {
		return MutationResult{}, false, err
	}
	actualGeneration, actualBroker, state, updated, err := readSessionMutationState(ctx, transaction, id)
	if err != nil {
		return MutationResult{}, false, err
	}
	if actualGeneration != generation || actualBroker != broker {
		return MutationResult{}, false, ErrGenerationChanged
	}
	when := millis(now)
	if when < updated {
		when = updated
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE session_metadata SET title = ?, updated_at_ms = ?
		WHERE id = ? AND generation = ? AND broker_generation = ?`, title, when, id, generation, broker); err != nil {
		return MutationResult{}, false, fmt.Errorf("store: rename session with receipt: %w", err)
	}
	result := MutationResult{
		OperationID: operationID,
		Kind:        OperationSessionRename,
		SessionID:   id,
		Generation:  generation,
		Title:       title,
		State:       state,
		CommittedAt: fromMillis(when),
	}
	snapshot, err := readMutationSession(ctx, transaction, id)
	if err != nil {
		return MutationResult{}, false, err
	}
	result.Session = mutationSessionSnapshot(snapshot)
	if err := insertMutation(ctx, transaction, fingerprint, result); err != nil {
		return MutationResult{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return MutationResult{}, false, fmt.Errorf("store: commit receipt-backed session rename: %w", err)
	}
	return result, false, nil
}

// BeginSessionTerminationWithReceipt atomically moves a live session to
// exiting and records acceptance. The broker performs the process-tree side
// effect only when replayed is false.
func (store *Store) BeginSessionTerminationWithReceipt(
	ctx context.Context,
	operationID string,
	fingerprint [sha256.Size]byte,
	id, generation string,
	now time.Time,
) (MutationResult, bool, error) {
	broker, err := store.activeBroker()
	if err != nil {
		return MutationResult{}, false, err
	}
	if err := validateMutation(operationID, OperationSessionTerminate); err != nil {
		return MutationResult{}, false, err
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return MutationResult{}, false, fmt.Errorf("store: begin receipt-backed session termination: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	if result, replayed, err := matchingMutation(ctx, transaction, operationID, OperationSessionTerminate, fingerprint); replayed || err != nil {
		return result, replayed, err
	}
	if err := validateSessionMutation(id, generation, now); err != nil {
		return MutationResult{}, false, err
	}
	if err := store.reserveMutationCapacity(ctx, transaction); err != nil {
		return MutationResult{}, false, err
	}
	actualGeneration, actualBroker, from, updated, err := readSessionMutationState(ctx, transaction, id)
	if err != nil {
		return MutationResult{}, false, err
	}
	if actualGeneration != generation || actualBroker != broker {
		return MutationResult{}, false, ErrGenerationChanged
	}
	if !allowedTransition(from, SessionExiting) {
		return MutationResult{}, false, fmt.Errorf("%w: %s to %s", ErrInvalidTransition, from, SessionExiting)
	}
	when := millis(now)
	if when < updated {
		when = updated
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE session_metadata
		SET state = 'exiting', updated_at_ms = ?
		WHERE id = ? AND generation = ? AND broker_generation = ?`, when, id, generation, broker); err != nil {
		return MutationResult{}, false, fmt.Errorf("store: begin session termination with receipt: %w", err)
	}
	result := MutationResult{
		OperationID: operationID,
		Kind:        OperationSessionTerminate,
		SessionID:   id,
		Generation:  generation,
		State:       SessionExiting,
		CommittedAt: fromMillis(when),
	}
	snapshot, err := readMutationSession(ctx, transaction, id)
	if err != nil {
		return MutationResult{}, false, err
	}
	result.Session = mutationSessionSnapshot(snapshot)
	if err := insertMutation(ctx, transaction, fingerprint, result); err != nil {
		return MutationResult{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return MutationResult{}, false, fmt.Errorf("store: commit receipt-backed session termination: %w", err)
	}
	return result, false, nil
}

type mutationQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func matchingMutation(
	ctx context.Context,
	querier mutationQuerier,
	operationID string,
	kind OperationKind,
	fingerprint [sha256.Size]byte,
) (MutationResult, bool, error) {
	result, storedFingerprint, found, err := readMutation(ctx, querier, operationID)
	if err != nil || !found {
		return MutationResult{}, false, err
	}
	if result.Kind != kind || storedFingerprint != fingerprint {
		return MutationResult{}, false, ErrOperationConflict
	}
	return result, true, nil
}

func readMutation(
	ctx context.Context,
	querier mutationQuerier,
	operationID string,
) (MutationResult, [sha256.Size]byte, bool, error) {
	var result MutationResult
	var fingerprint []byte
	var title sql.NullString
	var sessionJSON sql.NullString
	var committed int64
	err := querier.QueryRowContext(ctx, `SELECT operation_id, operation_kind, request_fingerprint,
		session_id, session_generation, result_title, result_state, result_at_ms, result_session_json
		FROM mutation_receipts WHERE operation_id = ?`, operationID).Scan(
		&result.OperationID, &result.Kind, &fingerprint, &result.SessionID,
		&result.Generation, &title, &result.State, &committed, &sessionJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return MutationResult{}, [sha256.Size]byte{}, false, nil
	}
	if err != nil {
		return MutationResult{}, [sha256.Size]byte{}, false, fmt.Errorf("store: read mutation receipt: %w", err)
	}
	if len(fingerprint) != sha256.Size {
		return MutationResult{}, [sha256.Size]byte{}, false, errors.New("store: stored mutation fingerprint is invalid")
	}
	var digest [sha256.Size]byte
	copy(digest[:], fingerprint)
	if title.Valid {
		result.Title = title.String
	}
	result.CommittedAt = fromMillis(committed)
	if !sessionJSON.Valid {
		return MutationResult{}, [sha256.Size]byte{}, false,
			errors.New("store: stored mutation session snapshot is missing")
	}
	if err := json.Unmarshal([]byte(sessionJSON.String), &result.Session); err != nil {
		return MutationResult{}, [sha256.Size]byte{}, false, errors.New("store: stored mutation session snapshot is invalid")
	}
	if err := validateMutationResult(result); err != nil {
		return MutationResult{}, [sha256.Size]byte{}, false, err
	}
	return result, digest, true, nil
}

type legacyMutationReceipt struct {
	operationID string
	kind        OperationKind
	sessionID   string
	generation  string
	title       string
	state       SessionState
	committedAt time.Time
}

// backfillMutationReceiptSnapshots upgrades schema-v2 receipts atomically with
// migration 003. It also rewrites create fingerprints from the old resolved
// process configuration to the stable saved-ID request identity.
func backfillMutationReceiptSnapshots(ctx context.Context, transaction *sql.Tx) error {
	rows, err := transaction.QueryContext(ctx, `SELECT operation_id, operation_kind,
		session_id, session_generation, COALESCE(result_title, ''), result_state, result_at_ms
		FROM mutation_receipts WHERE result_session_json IS NULL ORDER BY operation_id`)
	if err != nil {
		return fmt.Errorf("read legacy mutation receipts: %w", err)
	}
	var receipts []legacyMutationReceipt
	for rows.Next() {
		var receipt legacyMutationReceipt
		var committed int64
		if err := rows.Scan(&receipt.operationID, &receipt.kind, &receipt.sessionID,
			&receipt.generation, &receipt.title, &receipt.state, &committed); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy mutation receipt: %w", err)
		}
		receipt.committedAt = fromMillis(committed)
		receipts = append(receipts, receipt)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy mutation receipts: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate legacy mutation receipts: %w", err)
	}

	for _, receipt := range receipts {
		session, err := readMutationSession(ctx, transaction, receipt.sessionID)
		if err != nil {
			return err
		}
		result := MutationResult{
			OperationID: receipt.operationID, Kind: receipt.kind,
			SessionID: receipt.sessionID, Generation: receipt.generation,
			Title: receipt.title, State: receipt.state, CommittedAt: receipt.committedAt,
			Session: mutationSessionSnapshot(session),
		}
		result.Session.State = result.State
		result.Session.UpdatedAt = result.CommittedAt
		if result.Kind == OperationSessionRename {
			result.Session.Title = result.Title
		}
		if result.State != SessionExited && result.State != SessionLost {
			result.Session.EndedAt = time.Time{}
			result.Session.HasEndedAt = false
			result.Session.ExitCode = 0
			result.Session.HasExitCode = false
		}
		if err := validateMutationResult(result); err != nil {
			return fmt.Errorf("validate legacy mutation receipt %s: %w", receipt.operationID, err)
		}
		snapshotJSON, err := json.Marshal(result.Session)
		if err != nil {
			return fmt.Errorf("encode legacy mutation receipt %s: %w", receipt.operationID, err)
		}
		if result.Kind == OperationSessionCreate {
			fingerprint, err := SessionCreateFingerprint(result.Session.ProjectID, result.Session.ShellProfileID)
			if err != nil {
				return err
			}
			if _, err := transaction.ExecContext(ctx, `UPDATE mutation_receipts
				SET request_fingerprint = ?, result_session_json = ? WHERE operation_id = ?`,
				fingerprint[:], string(snapshotJSON), result.OperationID); err != nil {
				return fmt.Errorf("backfill legacy create receipt %s: %w", result.OperationID, err)
			}
			continue
		}
		if _, err := transaction.ExecContext(ctx, `UPDATE mutation_receipts
			SET result_session_json = ? WHERE operation_id = ?`, string(snapshotJSON), result.OperationID); err != nil {
			return fmt.Errorf("backfill legacy mutation receipt %s: %w", result.OperationID, err)
		}
	}
	return nil
}

func insertMutation(ctx context.Context, transaction *sql.Tx, fingerprint [sha256.Size]byte, result MutationResult) error {
	if err := validateMutationResult(result); err != nil {
		return err
	}
	var title any
	if result.Kind == OperationSessionRename {
		title = result.Title
	}
	sessionJSON, err := json.Marshal(result.Session)
	if err != nil {
		return fmt.Errorf("store: encode mutation session snapshot: %w", err)
	}
	when := millis(result.CommittedAt)
	_, err = transaction.ExecContext(ctx, `INSERT INTO mutation_receipts(
		operation_id, operation_kind, request_fingerprint, session_id, session_generation,
		result_title, result_state, result_at_ms, created_at_ms, result_session_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, result.OperationID, result.Kind, fingerprint[:],
		result.SessionID, result.Generation, title, result.State, when, when, string(sessionJSON))
	if err != nil {
		return fmt.Errorf("store: insert mutation receipt: %w", err)
	}
	return nil
}

func readMutationSession(ctx context.Context, querier mutationQuerier, id string) (SessionMetadata, error) {
	session, err := scanSession(querier.QueryRowContext(ctx, `SELECT `+sessionColumns+`
		FROM session_metadata WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return SessionMetadata{}, ErrSessionMissing
	}
	if err != nil {
		return SessionMetadata{}, fmt.Errorf("store: read mutation session snapshot: %w", err)
	}
	return session, nil
}

func mutationSessionSnapshot(session SessionMetadata) MutationSessionSnapshot {
	snapshot := MutationSessionSnapshot{
		ID: session.ID, Generation: session.Generation, BrokerGeneration: session.BrokerGeneration,
		Title: session.Title, ProjectID: session.ProjectID, ShellProfileID: session.ShellProfileID,
		State: session.State, Columns: session.Columns, Rows: session.Rows,
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
	}
	if session.StartedAt != nil {
		snapshot.StartedAt = *session.StartedAt
		snapshot.HasStartedAt = true
	}
	if session.EndedAt != nil {
		snapshot.EndedAt = *session.EndedAt
		snapshot.HasEndedAt = true
	}
	if session.ExitCode != nil {
		snapshot.ExitCode = *session.ExitCode
		snapshot.HasExitCode = true
	}
	return snapshot
}

func mutationSnapshotMetadata(snapshot MutationSessionSnapshot) SessionMetadata {
	metadata := SessionMetadata{
		ID: snapshot.ID, Generation: snapshot.Generation, BrokerGeneration: snapshot.BrokerGeneration,
		Title: snapshot.Title, ProjectID: snapshot.ProjectID, ShellProfileID: snapshot.ShellProfileID,
		State: snapshot.State, Columns: snapshot.Columns, Rows: snapshot.Rows,
		CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt,
	}
	if snapshot.HasStartedAt {
		value := snapshot.StartedAt
		metadata.StartedAt = &value
	}
	if snapshot.HasEndedAt {
		value := snapshot.EndedAt
		metadata.EndedAt = &value
	}
	if snapshot.HasExitCode {
		value := snapshot.ExitCode
		metadata.ExitCode = &value
	}
	return metadata
}

func validateMutationResult(result MutationResult) error {
	metadata := mutationSnapshotMetadata(result.Session)
	if err := validateSession(metadata); err != nil {
		return fmt.Errorf("store: mutation session snapshot: %w", err)
	}
	if result.SessionID != metadata.ID || result.Generation != metadata.Generation ||
		result.State != metadata.State || !result.CommittedAt.Equal(metadata.UpdatedAt) {
		return errors.New("store: mutation receipt does not match its session snapshot")
	}
	if result.Kind == OperationSessionRename && result.Title != metadata.Title {
		return errors.New("store: rename receipt title does not match its session snapshot")
	}
	return nil
}

func insertSession(ctx context.Context, transaction *sql.Tx, session SessionMetadata) error {
	_, err := transaction.ExecContext(ctx, `INSERT INTO session_metadata(
		id, generation, broker_generation, title, project_id, shell_profile_id,
		state, columns_count, rows_count, created_at_ms, updated_at_ms,
		started_at_ms, ended_at_ms, exit_code
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.Generation, session.BrokerGeneration, session.Title,
		session.ProjectID, session.ShellProfileID, session.State, session.Columns, session.Rows,
		millis(session.CreatedAt), millis(session.UpdatedAt), nullableMillis(session.StartedAt),
		nullableMillis(session.EndedAt), nullableInt32(session.ExitCode))
	return err
}

func readSessionMutationState(ctx context.Context, transaction *sql.Tx, id string) (string, string, SessionState, int64, error) {
	var generation, broker string
	var state SessionState
	var updated int64
	err := transaction.QueryRowContext(ctx, `SELECT generation, broker_generation, state, updated_at_ms
		FROM session_metadata WHERE id = ?`, id).Scan(&generation, &broker, &state, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", 0, ErrSessionMissing
	}
	if err != nil {
		return "", "", "", 0, fmt.Errorf("store: read session mutation state: %w", err)
	}
	return generation, broker, state, updated, nil
}

func (store *Store) reserveMutationCapacity(ctx context.Context, transaction *sql.Tx) error {
	if store.operationReceiptLimit < 1 {
		return errors.New("store: operation receipt limit must be positive")
	}
	if err := store.pruneTerminalMutationReceipts(ctx, transaction); err != nil {
		return err
	}
	var count int
	if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM mutation_receipts`).Scan(&count); err != nil {
		return fmt.Errorf("store: count mutation receipts: %w", err)
	}
	if count >= store.operationReceiptLimit {
		needed := count - store.operationReceiptLimit + 1
		result, err := transaction.ExecContext(ctx, `DELETE FROM mutation_receipts WHERE operation_id IN (
			SELECT receipt.operation_id FROM mutation_receipts AS receipt
			JOIN session_metadata AS session ON session.id = receipt.session_id
			WHERE session.state IN ('exited', 'lost')
			ORDER BY receipt.created_at_ms, receipt.operation_id
			LIMIT ?
		)`, needed)
		if err != nil {
			return fmt.Errorf("store: free terminal mutation receipt capacity: %w", err)
		}
		removed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: confirm freed mutation receipt capacity: %w", err)
		}
		count -= int(removed)
	}
	if count >= store.operationReceiptLimit {
		return ErrOperationReceiptLimit
	}
	return nil
}

func (store *Store) pruneTerminalMutationReceipts(ctx context.Context, transaction *sql.Tx) error {
	if store.retainedTerminalReceipts < 0 || store.retainedTerminalReceipts > store.operationReceiptLimit {
		return errors.New("store: retained terminal receipt limit is invalid")
	}
	_, err := transaction.ExecContext(ctx, `DELETE FROM mutation_receipts WHERE operation_id IN (
		SELECT receipt.operation_id FROM mutation_receipts AS receipt
		JOIN session_metadata AS session ON session.id = receipt.session_id
		WHERE session.state IN ('exited', 'lost')
		ORDER BY receipt.created_at_ms DESC, receipt.operation_id DESC
		LIMIT -1 OFFSET ?
	)`, store.retainedTerminalReceipts)
	if err != nil {
		return fmt.Errorf("store: prune terminal mutation receipts: %w", err)
	}
	return nil
}

func validateMutation(operationID string, kind OperationKind) error {
	if !validUUID(operationID) {
		return errors.New("store: operation ID must be a canonical UUID")
	}
	if kind != OperationSessionCreate && kind != OperationSessionRename && kind != OperationSessionTerminate {
		return errors.New("store: mutation kind is invalid")
	}
	return nil
}

func validateSessionMutation(id, generation string, now time.Time) error {
	if !validUUID(id) {
		return errors.New("store: session ID must be a canonical UUID")
	}
	if !validUUID(generation) {
		return errors.New("store: session generation must be a canonical UUID")
	}
	if now.IsZero() {
		return errors.New("store: session mutation time must not be zero")
	}
	return nil
}
