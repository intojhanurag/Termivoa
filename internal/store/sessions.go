package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultRetainedTerminalSessions = 4096
	defaultSessionListLimit         = defaultRetainedTerminalSessions + 64
	// sessionColumnsV5 are the columns before migration 6. The migration 3
	// backfill runs on that schema and selects a constant for title_custom.
	sessionColumnsV5 = `id, generation, broker_generation, title, project_id, shell_profile_id,
state, columns_count, rows_count, created_at_ms, updated_at_ms, started_at_ms, ended_at_ms, exit_code`
	sessionColumns = sessionColumnsV5 + `, title_custom`
)

func (store *Store) PutProject(ctx context.Context, project Project) error {
	if err := validateProject(project); err != nil {
		return err
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin project write: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	created, updated := millis(project.CreatedAt), millis(project.UpdatedAt)
	var storedCreated, storedUpdated int64
	err = transaction.QueryRowContext(ctx,
		`SELECT created_at_ms, updated_at_ms FROM projects WHERE id = ?`, project.ID,
	).Scan(&storedCreated, &storedUpdated)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = transaction.ExecContext(ctx, `INSERT INTO projects(
			id, display_name, canonical_path, created_at_ms, updated_at_ms, disabled_at_ms
		) VALUES (?, ?, ?, ?, ?, ?)`, project.ID, project.DisplayName, project.CanonicalPath,
			created, updated, nullableMillis(project.DisabledAt))
	case err != nil:
		return fmt.Errorf("store: read project timeline: %w", err)
	case created != storedCreated:
		return fmt.Errorf("%w: project %s creation time", ErrMetadataConflict, project.ID)
	case updated < storedUpdated:
		return fmt.Errorf("%w: project %s", ErrTimestampRegression, project.ID)
	default:
		_, err = transaction.ExecContext(ctx, `UPDATE projects SET
			display_name = ?, canonical_path = ?, updated_at_ms = ?, disabled_at_ms = ?
			WHERE id = ?`, project.DisplayName, project.CanonicalPath, updated,
			nullableMillis(project.DisabledAt), project.ID)
	}
	if err != nil {
		return fmt.Errorf("store: put project: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: commit project write: %w", err)
	}
	return nil
}

func (store *Store) GetProject(ctx context.Context, id string) (Project, error) {
	if !validUUID(id) {
		return Project{}, errors.New("store: project ID must be a canonical UUID")
	}
	var project Project
	var created, updated int64
	var disabled sql.NullInt64
	err := store.database.QueryRowContext(ctx, `SELECT id, display_name, canonical_path,
        created_at_ms, updated_at_ms, disabled_at_ms FROM projects WHERE id = ?`, id).Scan(
		&project.ID, &project.DisplayName, &project.CanonicalPath, &created, &updated, &disabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrProjectMissing
	}
	if err != nil {
		return Project{}, fmt.Errorf("store: get project: %w", err)
	}
	project.CreatedAt = fromMillis(created)
	project.UpdatedAt = fromMillis(updated)
	project.DisabledAt = timeFromNull(disabled)
	return project, nil
}

func (store *Store) PutShellProfile(ctx context.Context, profile ShellProfile) error {
	if profile.Arguments == nil {
		profile.Arguments = []string{}
	}
	if err := validateShellProfile(profile); err != nil {
		return err
	}
	arguments, err := json.Marshal(profile.Arguments)
	if err != nil {
		return fmt.Errorf("store: encode shell arguments: %w", err)
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin shell profile write: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	created, updated := millis(profile.CreatedAt), millis(profile.UpdatedAt)
	var storedCreated, storedUpdated int64
	err = transaction.QueryRowContext(ctx,
		`SELECT created_at_ms, updated_at_ms FROM shell_profiles WHERE id = ?`, profile.ID,
	).Scan(&storedCreated, &storedUpdated)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = transaction.ExecContext(ctx, `INSERT INTO shell_profiles(
			id, display_name, platform, executable_path, arguments_json,
			created_at_ms, updated_at_ms, disabled_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, profile.ID, profile.DisplayName, profile.Platform,
			profile.ExecutablePath, string(arguments), created, updated, nullableMillis(profile.DisabledAt))
	case err != nil:
		return fmt.Errorf("store: read shell profile timeline: %w", err)
	case created != storedCreated:
		return fmt.Errorf("%w: shell profile %s creation time", ErrMetadataConflict, profile.ID)
	case updated < storedUpdated:
		return fmt.Errorf("%w: shell profile %s", ErrTimestampRegression, profile.ID)
	default:
		_, err = transaction.ExecContext(ctx, `UPDATE shell_profiles SET
			display_name = ?, platform = ?, executable_path = ?, arguments_json = ?,
			updated_at_ms = ?, disabled_at_ms = ? WHERE id = ?`, profile.DisplayName,
			profile.Platform, profile.ExecutablePath, string(arguments), updated,
			nullableMillis(profile.DisabledAt), profile.ID)
	}
	if err != nil {
		return fmt.Errorf("store: put shell profile: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: commit shell profile write: %w", err)
	}
	return nil
}

func (store *Store) GetShellProfile(ctx context.Context, id string) (ShellProfile, error) {
	if !validUUID(id) {
		return ShellProfile{}, errors.New("store: shell profile ID must be a canonical UUID")
	}
	var profile ShellProfile
	var arguments string
	var created, updated int64
	var disabled sql.NullInt64
	err := store.database.QueryRowContext(ctx, `SELECT id, display_name, platform, executable_path,
        arguments_json, created_at_ms, updated_at_ms, disabled_at_ms FROM shell_profiles WHERE id = ?`, id).Scan(
		&profile.ID, &profile.DisplayName, &profile.Platform, &profile.ExecutablePath,
		&arguments, &created, &updated, &disabled,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ShellProfile{}, ErrShellProfileMissing
	}
	if err != nil {
		return ShellProfile{}, fmt.Errorf("store: get shell profile: %w", err)
	}
	if err := json.Unmarshal([]byte(arguments), &profile.Arguments); err != nil || profile.Arguments == nil {
		return ShellProfile{}, errors.New("store: stored shell profile arguments are invalid")
	}
	profile.CreatedAt = fromMillis(created)
	profile.UpdatedAt = fromMillis(updated)
	profile.DisabledAt = timeFromNull(disabled)
	return profile, nil
}

// BeginBrokerGeneration establishes this process generation and atomically
// marks sessions owned by earlier broker processes as lost. Calling it again
// with the same generation is harmless; changing generation requires reopen.
func (store *Store) BeginBrokerGeneration(ctx context.Context, generation string, now time.Time) ([]SessionMetadata, error) {
	if !validUUID(generation) {
		return nil, errors.New("store: broker generation must be a canonical UUID")
	}
	if now.IsZero() {
		return nil, errors.New("store: broker generation time must not be zero")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.broker != "" {
		if store.broker == generation {
			return nil, nil
		}
		return nil, ErrBrokerAlreadyStarted
	}

	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin broker recovery: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	rows, err := transaction.QueryContext(ctx, `SELECT `+sessionColumns+` FROM session_metadata
        WHERE broker_generation <> ? AND state IN ('starting', 'running', 'exiting')
        ORDER BY created_at_ms, id`, generation)
	if err != nil {
		return nil, fmt.Errorf("store: find sessions from prior broker: %w", err)
	}
	lost, err := scanSessions(rows)
	if err != nil {
		return nil, err
	}
	requestedWhen := millis(now)
	for index := range lost {
		session := &lost[index]
		when := max(requestedWhen, millis(session.CreatedAt), millis(session.UpdatedAt))
		if session.StartedAt != nil {
			when = max(when, millis(*session.StartedAt))
		}
		result, err := transaction.ExecContext(ctx, `UPDATE session_metadata
            SET state = 'lost', updated_at_ms = ?, ended_at_ms = ?, exit_code = NULL
            WHERE id = ? AND broker_generation <> ? AND state IN ('starting', 'running', 'exiting')`,
			when, when, session.ID, generation)
		if err != nil {
			return nil, fmt.Errorf("store: mark prior session %s lost: %w", session.ID, err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("store: confirm prior session %s recovery: %w", session.ID, err)
		}
		if updated != 1 {
			return nil, fmt.Errorf("store: prior session %s changed during recovery", session.ID)
		}
		ended := fromMillis(when)
		session.State = SessionLost
		session.UpdatedAt = ended
		session.EndedAt = timePointer(ended)
		session.ExitCode = nil
	}
	if err := store.pruneTerminalSessions(ctx, transaction); err != nil {
		return nil, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("store: commit broker recovery: %w", err)
	}
	store.broker = generation
	return lost, nil
}

func (store *Store) CreateSession(ctx context.Context, session SessionMetadata) error {
	broker, err := store.activeBroker()
	if err != nil {
		return err
	}
	if session.BrokerGeneration != broker {
		return ErrGenerationChanged
	}
	if err := validateSession(session); err != nil {
		return err
	}
	if session.State != SessionStarting || session.StartedAt != nil || session.EndedAt != nil || session.ExitCode != nil {
		return errors.New("store: a new session must be starting without lifecycle completion fields")
	}
	_, err = store.database.ExecContext(ctx, `INSERT INTO session_metadata(
        id, generation, broker_generation, title, project_id, shell_profile_id,
        state, columns_count, rows_count, created_at_ms, updated_at_ms,
        started_at_ms, ended_at_ms, exit_code
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.Generation, session.BrokerGeneration, session.Title,
		session.ProjectID, session.ShellProfileID, session.State, session.Columns, session.Rows,
		millis(session.CreatedAt), millis(session.UpdatedAt), nullableMillis(session.StartedAt),
		nullableMillis(session.EndedAt), nullableInt32(session.ExitCode),
	)
	if err != nil {
		return fmt.Errorf("store: create session metadata: %w", err)
	}
	return nil
}

func (store *Store) GetSession(ctx context.Context, id string) (SessionMetadata, error) {
	if !validUUID(id) {
		return SessionMetadata{}, errors.New("store: session ID must be a canonical UUID")
	}
	row := store.database.QueryRowContext(ctx, `SELECT `+sessionColumns+` FROM session_metadata WHERE id = ?`, id)
	session, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionMetadata{}, ErrSessionMissing
	}
	if err != nil {
		return SessionMetadata{}, fmt.Errorf("store: get session metadata: %w", err)
	}
	return session, nil
}

func (store *Store) ListSessions(ctx context.Context) ([]SessionMetadata, error) {
	if store.sessionListLimit < 1 {
		return nil, errors.New("store: session list limit must be positive")
	}
	rows, err := store.database.QueryContext(ctx, `SELECT `+sessionColumns+`
		FROM session_metadata ORDER BY created_at_ms DESC, id LIMIT ?`, store.sessionListLimit)
	if err != nil {
		return nil, fmt.Errorf("store: list session metadata: %w", err)
	}
	return scanSessions(rows)
}

func (store *Store) RenameSession(ctx context.Context, id, generation, title string, now time.Time) error {
	broker, err := store.activeBroker()
	if err != nil {
		return err
	}
	if !validUUID(id) {
		return errors.New("store: session ID must be a canonical UUID")
	}
	if !validUUID(generation) {
		return errors.New("store: session generation must be a canonical UUID")
	}
	if len(title) == 0 || len(title) > 256 || strings.IndexByte(title, 0) >= 0 {
		return errors.New("store: session title must be 1..256 bytes without NUL")
	}
	if now.IsZero() {
		return errors.New("store: session rename time must not be zero")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin session rename: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	var actualGeneration, actualBroker string
	var updated int64
	err = transaction.QueryRowContext(ctx,
		`SELECT generation, broker_generation, updated_at_ms FROM session_metadata WHERE id = ?`, id,
	).Scan(&actualGeneration, &actualBroker, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionMissing
	}
	if err != nil {
		return fmt.Errorf("store: read session rename state: %w", err)
	}
	if actualGeneration != generation || actualBroker != broker {
		return ErrGenerationChanged
	}
	when := millis(now)
	if when < updated {
		return fmt.Errorf("%w: rename for session %s", ErrTimestampRegression, id)
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE session_metadata SET title = ?, title_custom = 1, updated_at_ms = ?
	        WHERE id = ? AND generation = ? AND broker_generation = ?`, title, when, id, generation, broker); err != nil {
		return fmt.Errorf("store: rename session: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: commit session rename: %w", err)
	}
	return nil
}

func (store *Store) TransitionSession(ctx context.Context, id, generation string, to SessionState, exitCode *int32, now time.Time) error {
	broker, err := store.activeBroker()
	if err != nil {
		return err
	}
	if !validUUID(id) {
		return errors.New("store: session ID must be a canonical UUID")
	}
	if !validUUID(generation) {
		return errors.New("store: session generation must be a canonical UUID")
	}
	if !validState(to) {
		return errors.New("store: target session state is invalid")
	}
	if now.IsZero() {
		return errors.New("store: session transition time must not be zero")
	}
	if exitCode != nil && to != SessionExited {
		return errors.New("store: exit code is only valid for an exited session")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin session transition: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	var actualGeneration, actualBroker string
	var from SessionState
	var created, updated int64
	var started sql.NullInt64
	err = transaction.QueryRowContext(ctx,
		`SELECT generation, broker_generation, state, created_at_ms, updated_at_ms, started_at_ms
		 FROM session_metadata WHERE id = ?`, id,
	).Scan(&actualGeneration, &actualBroker, &from, &created, &updated, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionMissing
	}
	if err != nil {
		return fmt.Errorf("store: read session transition state: %w", err)
	}
	if actualGeneration != generation || actualBroker != broker {
		return ErrGenerationChanged
	}
	if !allowedTransition(from, to) {
		return fmt.Errorf("%w: %s to %s", ErrInvalidTransition, from, to)
	}
	when := millis(now)
	terminal := to == SessionExited || to == SessionLost
	if !terminal && (when < created || started.Valid && when < started.Int64 || when < updated) {
		return fmt.Errorf("%w: transition for session %s", ErrTimestampRegression, id)
	}
	updatedWhen := when
	if terminal {
		when = max(when, created)
		if started.Valid {
			when = max(when, started.Int64)
		}
		updatedWhen = max(when, updated)
	}
	var startedValue any
	if started.Valid {
		startedValue = started.Int64
	} else if to == SessionRunning {
		startedValue = when
	}
	var endedValue any
	if to == SessionExited || to == SessionLost {
		endedValue = when
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE session_metadata
        SET state = ?, updated_at_ms = ?, started_at_ms = ?, ended_at_ms = ?, exit_code = ?
        WHERE id = ? AND generation = ? AND broker_generation = ?`,
		to, updatedWhen, startedValue, endedValue, nullableInt32(exitCode), id, generation, broker,
	); err != nil {
		return fmt.Errorf("store: update session transition: %w", err)
	}
	if terminal && store.retainedTerminalSessions >= 0 {
		if err := store.pruneTerminalSessions(ctx, transaction); err != nil {
			return err
		}
		if err := store.pruneTerminalMutationReceipts(ctx, transaction); err != nil {
			return err
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: commit session transition: %w", err)
	}
	return nil
}

func (store *Store) pruneTerminalSessions(ctx context.Context, transaction *sql.Tx) error {
	if store.retainedTerminalSessions < 0 {
		return errors.New("store: retained terminal session limit must not be negative")
	}
	_, err := transaction.ExecContext(ctx, `DELETE FROM session_metadata
		WHERE state IN ('exited', 'lost') AND id NOT IN (
			SELECT id FROM session_metadata WHERE state IN ('exited', 'lost')
			ORDER BY updated_at_ms DESC, id DESC LIMIT ?
		)`, store.retainedTerminalSessions)
	if err != nil {
		return fmt.Errorf("store: prune terminal session metadata: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(scanner rowScanner) (SessionMetadata, error) {
	var session SessionMetadata
	var started, ended sql.NullInt64
	var exitCode sql.NullInt64
	var created, updated int64
	if err := scanner.Scan(
		&session.ID, &session.Generation, &session.BrokerGeneration, &session.Title,
		&session.ProjectID, &session.ShellProfileID, &session.State, &session.Columns, &session.Rows,
		&created, &updated, &started, &ended, &exitCode, &session.TitleCustom,
	); err != nil {
		return SessionMetadata{}, err
	}
	session.CreatedAt = fromMillis(created)
	session.UpdatedAt = fromMillis(updated)
	session.StartedAt = timeFromNull(started)
	session.EndedAt = timeFromNull(ended)
	if exitCode.Valid {
		value := int32(exitCode.Int64)
		session.ExitCode = &value
	}
	return session, nil
}

func scanSessions(rows *sql.Rows) ([]SessionMetadata, error) {
	defer rows.Close()
	var sessions []SessionMetadata
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan session metadata: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate session metadata: %w", err)
	}
	return sessions, nil
}

func validateProject(project Project) error {
	if !validUUID(project.ID) {
		return errors.New("store: project ID must be a canonical UUID")
	}
	if len(project.DisplayName) == 0 || len(project.DisplayName) > 128 || strings.IndexByte(project.DisplayName, 0) >= 0 {
		return errors.New("store: project display name must be 1..128 bytes without NUL")
	}
	if len(project.CanonicalPath) == 0 || len(project.CanonicalPath) > 32768 || strings.IndexByte(project.CanonicalPath, 0) >= 0 ||
		!filepath.IsAbs(project.CanonicalPath) || filepath.Clean(project.CanonicalPath) != project.CanonicalPath {
		return errors.New("store: project path must be a clean absolute path of at most 32768 bytes")
	}
	if err := validateTimes("project", project.CreatedAt, project.UpdatedAt, project.DisabledAt); err != nil {
		return err
	}
	return nil
}

func validateShellProfile(profile ShellProfile) error {
	if !validUUID(profile.ID) {
		return errors.New("store: shell profile ID must be a canonical UUID")
	}
	if len(profile.DisplayName) == 0 || len(profile.DisplayName) > 128 || strings.IndexByte(profile.DisplayName, 0) >= 0 {
		return errors.New("store: shell profile display name must be 1..128 bytes without NUL")
	}
	if profile.Platform != PlatformDarwin && profile.Platform != PlatformWindows && profile.Platform != PlatformWSL &&
		profile.Platform != PlatformLinux {
		return errors.New("store: shell profile platform must be darwin, windows, wsl, or linux")
	}
	if len(profile.ExecutablePath) == 0 || len(profile.ExecutablePath) > 32768 || strings.IndexByte(profile.ExecutablePath, 0) >= 0 ||
		!filepath.IsAbs(profile.ExecutablePath) || filepath.Clean(profile.ExecutablePath) != profile.ExecutablePath {
		return errors.New("store: shell executable must be a clean absolute path of at most 32768 bytes")
	}
	if err := validateTimes("shell profile", profile.CreatedAt, profile.UpdatedAt, profile.DisabledAt); err != nil {
		return err
	}
	for _, argument := range profile.Arguments {
		if strings.IndexByte(argument, 0) >= 0 {
			return errors.New("store: shell profile argument contains NUL")
		}
	}
	arguments, err := json.Marshal(profile.Arguments)
	if err != nil || len(arguments) < 2 || len(arguments) > 65536 {
		return errors.New("store: invalid shell profile arguments")
	}
	return nil
}

func validateSession(session SessionMetadata) error {
	if !validUUID(session.ID) {
		return errors.New("store: session ID must be a canonical UUID")
	}
	if !validUUID(session.Generation) {
		return errors.New("store: session generation must be a canonical UUID")
	}
	if !validUUID(session.BrokerGeneration) {
		return errors.New("store: session broker generation must be a canonical UUID")
	}
	if !validUUID(session.ProjectID) {
		return errors.New("store: session project ID must be a canonical UUID")
	}
	if !validUUID(session.ShellProfileID) {
		return errors.New("store: session shell profile ID must be a canonical UUID")
	}
	if len(session.Title) == 0 || len(session.Title) > 256 || strings.IndexByte(session.Title, 0) >= 0 {
		return errors.New("store: session title must be 1..256 bytes without NUL")
	}
	if !validState(session.State) {
		return errors.New("store: session state is invalid")
	}
	if session.Columns < 20 || session.Columns > 400 {
		return errors.New("store: session columns must be between 20 and 400")
	}
	if session.Rows < 5 || session.Rows > 200 {
		return errors.New("store: session rows must be between 5 and 200")
	}
	if session.CreatedAt.IsZero() || session.UpdatedAt.IsZero() || session.UpdatedAt.Before(session.CreatedAt) {
		return errors.New("store: session timestamps are invalid")
	}
	terminal := session.State == SessionExited || session.State == SessionLost
	if terminal != (session.EndedAt != nil) || (session.ExitCode != nil && session.State != SessionExited) {
		return errors.New("store: invalid terminal session metadata")
	}
	if session.StartedAt != nil && session.StartedAt.Before(session.CreatedAt) ||
		session.EndedAt != nil && session.EndedAt.Before(session.CreatedAt) {
		return errors.New("store: session lifecycle time precedes creation")
	}
	return nil
}

func validateTimes(kind string, created, updated time.Time, disabled *time.Time) error {
	if created.IsZero() || updated.IsZero() || updated.Before(created) {
		return fmt.Errorf("store: %s timestamps are invalid", kind)
	}
	if disabled != nil && disabled.Before(created) {
		return fmt.Errorf("store: %s disabled time precedes creation", kind)
	}
	return nil
}

func validState(state SessionState) bool {
	return state == SessionStarting || state == SessionRunning || state == SessionExiting || state == SessionExited || state == SessionLost
}

func allowedTransition(from, to SessionState) bool {
	switch from {
	case SessionStarting:
		return to == SessionRunning || to == SessionExiting || to == SessionExited || to == SessionLost
	case SessionRunning:
		return to == SessionExiting || to == SessionExited || to == SessionLost
	case SessionExiting:
		return to == SessionExited || to == SessionLost
	default:
		return false
	}
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' ||
		value[14] < '1' || value[14] > '8' || !strings.ContainsRune("89aAbB", rune(value[19])) {
		return false
	}
	for index := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		character := value[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func millis(value time.Time) int64 { return value.UTC().UnixMilli() }

func nullableMillis(value *time.Time) any {
	if value == nil {
		return nil
	}
	return millis(*value)
}

func nullableInt32(value *int32) any {
	if value == nil {
		return nil
	}
	return *value
}

func fromMillis(value int64) time.Time { return time.UnixMilli(value).UTC() }

func timeFromNull(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := fromMillis(value.Int64)
	return &timestamp
}

func timePointer(value time.Time) *time.Time {
	copy := value
	return &copy
}
