package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrPairingInvalid    = errors.New("store: pairing grant is unknown, used, or expired")
	ErrPairingCapacity   = errors.New("store: pairing capacity reached")
	ErrPendingMissing    = errors.New("store: pending pairing is unknown or expired")
	ErrPendingSecret     = errors.New("store: poll secret does not match")
	ErrPendingApproved   = errors.New("store: pending pairing was already approved")
	ErrDeviceMissing     = errors.New("store: device does not exist")
	ErrDeviceCapacity    = errors.New("store: paired device capacity reached")
	ErrCredentialMissing = errors.New("store: browser credential does not exist")
)

// PairingGrant is persisted only as the SHA-256 hash of its secret.
type PairingGrant struct {
	ID        string
	Hash      []byte
	CreatedAt time.Time
	ExpiresAt time.Time
}

// PendingPairing is created by one atomic grant redemption. VerificationCode
// is a human comparison value; PollHash is the only proof accepted for polling.
type PendingPairing struct {
	ID               string
	GrantID          string
	PollHash         []byte
	VerificationCode string
	DeviceLabel      string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	ApprovedAt       *time.Time
}

// Device is a paired browser. Label is untrusted display text, never identity.
type Device struct {
	ID         string
	Label      string
	CreatedAt  time.Time
	LastUsedAt time.Time
	RevokedAt  *time.Time
}

// BrowserSession is one browser credential, persisted as its SHA-256 hash.
type BrowserSession struct {
	ID             string
	DeviceID       string
	CredentialHash []byte
	CreatedAt      time.Time
	LastUsedAt     time.Time
	ExpiresAt      time.Time
	RevokedAt      *time.Time
}

// PairingLimits bound rows that untrusted pairing attempts can allocate.
type PairingLimits struct {
	OutstandingGrants int
	PendingPairings   int
	FailedPolls       int
	Devices           int
}

// CreatePairingGrant prunes expired grants and inserts a new one only while
// fewer than limit unredeemed grants remain outstanding.
func (store *Store) CreatePairingGrant(ctx context.Context, grant PairingGrant, limit int) error {
	if !validUUID(grant.ID) || len(grant.Hash) != sha256.Size || grant.CreatedAt.IsZero() ||
		!grant.ExpiresAt.After(grant.CreatedAt) || limit < 1 {
		return errors.New("store: invalid pairing grant")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin pairing grant: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	now := millis(grant.CreatedAt)
	if _, err := transaction.ExecContext(ctx, `DELETE FROM pairing_grants WHERE expires_at_ms <= ?`, now); err != nil {
		return fmt.Errorf("store: prune pairing grants: %w", err)
	}
	var outstanding int
	if err := transaction.QueryRowContext(ctx,
		`SELECT count(*) FROM pairing_grants WHERE redeemed_at_ms IS NULL`).Scan(&outstanding); err != nil {
		return fmt.Errorf("store: count pairing grants: %w", err)
	}
	if outstanding >= limit {
		return ErrPairingCapacity
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO pairing_grants(id, grant_hash, created_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?)`, grant.ID, grant.Hash, now, millis(grant.ExpiresAt)); err != nil {
		return fmt.Errorf("store: insert pairing grant: %w", err)
	}
	return commit(transaction, "pairing grant")
}

// RedeemPairingGrant consumes an unexpired unused grant and creates its
// pending pairing in one transaction. A capacity or insert failure rolls back
// the consumption, so concurrent redeemers can never both succeed and a failed
// attempt leaves no partially consumed grant.
func (store *Store) RedeemPairingGrant(ctx context.Context, grantHash []byte, pending PendingPairing, limit int) error {
	if len(grantHash) != sha256.Size || limit < 1 {
		return errors.New("store: invalid pairing redemption")
	}
	if err := validatePending(pending); err != nil {
		return err
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin pairing redemption: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	now := millis(pending.CreatedAt)
	if err := prunePairing(ctx, transaction, now); err != nil {
		return err
	}
	var grantID string
	var expires int64
	var redeemed sql.NullInt64
	err = transaction.QueryRowContext(ctx,
		`SELECT id, expires_at_ms, redeemed_at_ms FROM pairing_grants WHERE grant_hash = ?`, grantHash,
	).Scan(&grantID, &expires, &redeemed)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (redeemed.Valid || expires <= now) {
		return ErrPairingInvalid
	}
	if err != nil {
		return fmt.Errorf("store: read pairing grant: %w", err)
	}
	var active int
	if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM pending_pairings`).Scan(&active); err != nil {
		return fmt.Errorf("store: count pending pairings: %w", err)
	}
	if active >= limit {
		return ErrPairingCapacity
	}
	result, err := transaction.ExecContext(ctx,
		`UPDATE pairing_grants SET redeemed_at_ms = ? WHERE id = ? AND redeemed_at_ms IS NULL`, now, grantID)
	if err != nil {
		return fmt.Errorf("store: consume pairing grant: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return ErrPairingInvalid
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO pending_pairings(
		id, grant_id, poll_hash, verification_code, device_label, created_at_ms, expires_at_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?)`, pending.ID, grantID, pending.PollHash, pending.VerificationCode,
		pending.DeviceLabel, now, millis(pending.ExpiresAt)); err != nil {
		return fmt.Errorf("store: insert pending pairing: %w", err)
	}
	return commit(transaction, "pairing redemption")
}

// ListPendingPairings returns unexpired pairings still awaiting approval.
func (store *Store) ListPendingPairings(ctx context.Context, now time.Time, limit int) ([]PendingPairing, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT id, grant_id, poll_hash, verification_code,
		device_label, created_at_ms, expires_at_ms, approved_at_ms FROM pending_pairings
		WHERE expires_at_ms > ? AND approved_at_ms IS NULL ORDER BY created_at_ms, id LIMIT ?`, millis(now), limit)
	if err != nil {
		return nil, fmt.Errorf("store: list pending pairings: %w", err)
	}
	defer rows.Close()
	var pending []PendingPairing
	for rows.Next() {
		var record PendingPairing
		var created, expires int64
		var approved sql.NullInt64
		if err := rows.Scan(&record.ID, &record.GrantID, &record.PollHash, &record.VerificationCode,
			&record.DeviceLabel, &created, &expires, &approved); err != nil {
			return nil, fmt.Errorf("store: scan pending pairing: %w", err)
		}
		record.CreatedAt, record.ExpiresAt, record.ApprovedAt = fromMillis(created), fromMillis(expires), timeFromNull(approved)
		pending = append(pending, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: iterate pending pairings: %w", err)
	}
	return pending, nil
}

// ApprovePendingPairing records a local approval exactly once.
func (store *Store) ApprovePendingPairing(ctx context.Context, id string, now time.Time) error {
	if !validUUID(id) || now.IsZero() {
		return errors.New("store: invalid pending approval")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin pending approval: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	var expires int64
	var approved sql.NullInt64
	err = transaction.QueryRowContext(ctx,
		`SELECT expires_at_ms, approved_at_ms FROM pending_pairings WHERE id = ?`, id).Scan(&expires, &approved)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrPendingMissing
	case err != nil:
		return fmt.Errorf("store: read pending pairing: %w", err)
	case expires <= millis(now):
		return ErrPendingMissing
	case approved.Valid:
		return ErrPendingApproved
	}
	if _, err := transaction.ExecContext(ctx,
		`UPDATE pending_pairings SET approved_at_ms = ? WHERE id = ?`, millis(now), id); err != nil {
		return fmt.Errorf("store: approve pending pairing: %w", err)
	}
	return commit(transaction, "pending approval")
}

// CompletePendingPairing checks one poll. A wrong secret increments a bounded
// failure counter and deletes the record at limits.FailedPolls. An approved
// poll atomically creates the device and browser credential and deletes the
// pending record, so the credential can be delivered only once.
func (store *Store) CompletePendingPairing(
	ctx context.Context, id string, pollHash []byte, now time.Time,
	device Device, session BrowserSession, limits PairingLimits,
) (Device, bool, error) {
	if !validUUID(id) || len(pollHash) != sha256.Size || now.IsZero() || limits.FailedPolls < 1 || limits.Devices < 1 {
		return Device{}, false, errors.New("store: invalid pairing poll")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return Device{}, false, fmt.Errorf("store: begin pairing poll: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	when := millis(now)
	var storedHash []byte
	var label string
	var expires int64
	var approved sql.NullInt64
	var failed int
	err = transaction.QueryRowContext(ctx, `SELECT poll_hash, device_label, expires_at_ms, approved_at_ms, failed_polls
		FROM pending_pairings WHERE id = ?`, id).Scan(&storedHash, &label, &expires, &approved, &failed)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, false, ErrPendingMissing
	}
	if err != nil {
		return Device{}, false, fmt.Errorf("store: read pending pairing: %w", err)
	}
	if expires <= when {
		if _, err := transaction.ExecContext(ctx, `DELETE FROM pending_pairings WHERE id = ?`, id); err != nil {
			return Device{}, false, fmt.Errorf("store: prune pending pairing: %w", err)
		}
		if err := commit(transaction, "expired pairing"); err != nil {
			return Device{}, false, err
		}
		return Device{}, false, ErrPendingMissing
	}
	if subtle.ConstantTimeCompare(storedHash, pollHash) != 1 {
		statement := `UPDATE pending_pairings SET failed_polls = failed_polls + 1 WHERE id = ?`
		if failed+1 >= limits.FailedPolls {
			statement = `DELETE FROM pending_pairings WHERE id = ?`
		}
		if _, err := transaction.ExecContext(ctx, statement, id); err != nil {
			return Device{}, false, fmt.Errorf("store: record failed poll: %w", err)
		}
		if err := commit(transaction, "failed poll"); err != nil {
			return Device{}, false, err
		}
		return Device{}, false, ErrPendingSecret
	}
	if !approved.Valid {
		return Device{}, false, nil
	}
	device.Label = label
	if err := validateDevice(device); err != nil {
		return Device{}, false, err
	}
	if err := validateBrowserSession(session); err != nil || session.DeviceID != device.ID {
		return Device{}, false, errors.New("store: invalid browser session")
	}
	if err := reserveDeviceCapacity(ctx, transaction, limits.Devices); err != nil {
		return Device{}, false, err
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO devices(id, label, created_at_ms, last_used_at_ms)
		VALUES (?, ?, ?, ?)`, device.ID, device.Label, millis(device.CreatedAt), millis(device.LastUsedAt)); err != nil {
		return Device{}, false, fmt.Errorf("store: insert device: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO browser_sessions(
		id, device_id, credential_hash, created_at_ms, last_used_at_ms, expires_at_ms
	) VALUES (?, ?, ?, ?, ?, ?)`, session.ID, session.DeviceID, session.CredentialHash,
		millis(session.CreatedAt), millis(session.LastUsedAt), millis(session.ExpiresAt)); err != nil {
		return Device{}, false, fmt.Errorf("store: insert browser session: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM pending_pairings WHERE id = ?`, id); err != nil {
		return Device{}, false, fmt.Errorf("store: consume pending pairing: %w", err)
	}
	if err := commit(transaction, "pairing completion"); err != nil {
		return Device{}, false, err
	}
	return device, true, nil
}

// reserveDeviceCapacity removes the oldest revoked devices (cascading their
// credentials) until one row is free. Active devices are never pruned.
func reserveDeviceCapacity(ctx context.Context, transaction *sql.Tx, limit int) error {
	var total, active int
	if err := transaction.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE revoked_at_ms IS NULL)
		FROM devices`).Scan(&total, &active); err != nil {
		return fmt.Errorf("store: count devices: %w", err)
	}
	if active >= limit {
		return ErrDeviceCapacity
	}
	if excess := total - limit + 1; excess > 0 {
		if _, err := transaction.ExecContext(ctx, `DELETE FROM devices WHERE id IN (
			SELECT id FROM devices WHERE revoked_at_ms IS NOT NULL ORDER BY revoked_at_ms, id LIMIT ?)`, excess); err != nil {
			return fmt.Errorf("store: prune revoked devices: %w", err)
		}
	}
	return nil
}

// LookupCredential resolves a credential hash. The caller must still apply
// revocation and lifetime policy; a returned row is not authorization.
func (store *Store) LookupCredential(ctx context.Context, hash []byte) (BrowserSession, Device, error) {
	if len(hash) != sha256.Size {
		return BrowserSession{}, Device{}, ErrCredentialMissing
	}
	var session BrowserSession
	var device Device
	var created, used, expires, deviceCreated, deviceUsed int64
	var revoked, deviceRevoked sql.NullInt64
	err := store.database.QueryRowContext(ctx, `SELECT s.id, s.device_id, s.credential_hash, s.created_at_ms,
		s.last_used_at_ms, s.expires_at_ms, s.revoked_at_ms, d.label, d.created_at_ms, d.last_used_at_ms, d.revoked_at_ms
		FROM browser_sessions s JOIN devices d ON d.id = s.device_id WHERE s.credential_hash = ?`, hash).Scan(
		&session.ID, &session.DeviceID, &session.CredentialHash, &created, &used, &expires, &revoked,
		&device.Label, &deviceCreated, &deviceUsed, &deviceRevoked)
	if errors.Is(err, sql.ErrNoRows) {
		return BrowserSession{}, Device{}, ErrCredentialMissing
	}
	if err != nil {
		return BrowserSession{}, Device{}, fmt.Errorf("store: lookup credential: %w", err)
	}
	session.CreatedAt, session.LastUsedAt, session.ExpiresAt = fromMillis(created), fromMillis(used), fromMillis(expires)
	session.RevokedAt = timeFromNull(revoked)
	device.ID, device.CreatedAt, device.LastUsedAt = session.DeviceID, fromMillis(deviceCreated), fromMillis(deviceUsed)
	device.RevokedAt = timeFromNull(deviceRevoked)
	return session, device, nil
}

// TouchCredential advances last use monotonically for the credential and its
// device. It never changes the absolute expiry or revives revoked rows.
func (store *Store) TouchCredential(ctx context.Context, sessionID string, now time.Time) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin credential use: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	when := millis(now)
	if _, err := transaction.ExecContext(ctx, `UPDATE browser_sessions SET last_used_at_ms = max(last_used_at_ms, ?)
		WHERE id = ? AND revoked_at_ms IS NULL`, when, sessionID); err != nil {
		return fmt.Errorf("store: record credential use: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE devices SET last_used_at_ms = max(last_used_at_ms, ?)
		WHERE id = (SELECT device_id FROM browser_sessions WHERE id = ?) AND revoked_at_ms IS NULL`, when, sessionID); err != nil {
		return fmt.Errorf("store: record device use: %w", err)
	}
	return commit(transaction, "credential use")
}

// RevokeCredential durably revokes one browser credential (logout).
func (store *Store) RevokeCredential(ctx context.Context, sessionID string, now time.Time) error {
	result, err := store.database.ExecContext(ctx, `UPDATE browser_sessions
		SET revoked_at_ms = coalesce(revoked_at_ms, ?) WHERE id = ?`, millis(now), sessionID)
	if err != nil {
		return fmt.Errorf("store: revoke credential: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return ErrCredentialMissing
	}
	return nil
}

// RevokeDevice durably revokes a device and every credential it owns, and
// returns those credential IDs for in-memory fan-out.
func (store *Store) RevokeDevice(ctx context.Context, deviceID string, now time.Time) ([]string, error) {
	if !validUUID(deviceID) {
		return nil, ErrDeviceMissing
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin device revocation: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	when := millis(now)
	result, err := transaction.ExecContext(ctx,
		`UPDATE devices SET revoked_at_ms = coalesce(revoked_at_ms, ?) WHERE id = ?`, when, deviceID)
	if err != nil {
		return nil, fmt.Errorf("store: revoke device: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return nil, ErrDeviceMissing
	}
	if _, err := transaction.ExecContext(ctx, `UPDATE browser_sessions SET revoked_at_ms = coalesce(revoked_at_ms, ?)
		WHERE device_id = ?`, when, deviceID); err != nil {
		return nil, fmt.Errorf("store: revoke device credentials: %w", err)
	}
	rows, err := transaction.QueryContext(ctx, `SELECT id FROM browser_sessions WHERE device_id = ? ORDER BY id`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("store: list device credentials: %w", err)
	}
	var credentials []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: scan device credential: %w", err)
		}
		credentials = append(credentials, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return credentials, commit(transaction, "device revocation")
}

// ListDevices returns the most recently created devices first.
func (store *Store) ListDevices(ctx context.Context, limit int) ([]Device, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT id, label, created_at_ms, last_used_at_ms, revoked_at_ms
		FROM devices ORDER BY created_at_ms DESC, id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list devices: %w", err)
	}
	defer rows.Close()
	var devices []Device
	for rows.Next() {
		var device Device
		var created, used int64
		var revoked sql.NullInt64
		if err := rows.Scan(&device.ID, &device.Label, &created, &used, &revoked); err != nil {
			return nil, fmt.Errorf("store: scan device: %w", err)
		}
		device.CreatedAt, device.LastUsedAt, device.RevokedAt = fromMillis(created), fromMillis(used), timeFromNull(revoked)
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func (store *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := store.database.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: read setting: %w", err)
	}
	return value, true, nil
}

func (store *Store) PutSetting(ctx context.Context, key, value string, now time.Time) error {
	if key == "" || len(key) > 64 || len(value) > 4096 || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return errors.New("store: invalid setting")
	}
	if _, err := store.database.ExecContext(ctx, `INSERT INTO settings(key, value, updated_at_ms) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at_ms = excluded.updated_at_ms`,
		key, value, millis(now)); err != nil {
		return fmt.Errorf("store: write setting: %w", err)
	}
	return nil
}

// ListProjects returns enabled projects in creation order.
func (store *Store) ListProjects(ctx context.Context, limit int) ([]Project, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT id, display_name, canonical_path, created_at_ms,
		updated_at_ms FROM projects WHERE disabled_at_ms IS NULL ORDER BY created_at_ms, id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var project Project
		var created, updated int64
		if err := rows.Scan(&project.ID, &project.DisplayName, &project.CanonicalPath, &created, &updated); err != nil {
			return nil, fmt.Errorf("store: scan project: %w", err)
		}
		project.CreatedAt, project.UpdatedAt = fromMillis(created), fromMillis(updated)
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

// ListShellProfiles returns enabled profiles in creation order.
func (store *Store) ListShellProfiles(ctx context.Context, limit int) ([]ShellProfile, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT id FROM shell_profiles
		WHERE disabled_at_ms IS NULL ORDER BY created_at_ms, id LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list shell profiles: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: scan shell profile: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	profiles := make([]ShellProfile, 0, len(ids))
	for _, id := range ids {
		profile, err := store.GetShellProfile(ctx, id)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, nil
}

func prunePairing(ctx context.Context, transaction *sql.Tx, now int64) error {
	if _, err := transaction.ExecContext(ctx, `DELETE FROM pending_pairings WHERE expires_at_ms <= ?`, now); err != nil {
		return fmt.Errorf("store: prune pending pairings: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM pairing_grants WHERE expires_at_ms <= ?`, now); err != nil {
		return fmt.Errorf("store: prune pairing grants: %w", err)
	}
	return nil
}

func validatePending(pending PendingPairing) error {
	if !validUUID(pending.ID) || len(pending.PollHash) != sha256.Size || len(pending.VerificationCode) != 7 ||
		pending.DeviceLabel == "" || len(pending.DeviceLabel) > 64 || !utf8.ValidString(pending.DeviceLabel) ||
		pending.CreatedAt.IsZero() || !pending.ExpiresAt.After(pending.CreatedAt) {
		return errors.New("store: invalid pending pairing")
	}
	return nil
}

func validateDevice(device Device) error {
	if !validUUID(device.ID) || device.Label == "" || len(device.Label) > 64 || device.CreatedAt.IsZero() ||
		device.LastUsedAt.Before(device.CreatedAt) {
		return errors.New("store: invalid device")
	}
	return nil
}

func validateBrowserSession(session BrowserSession) error {
	if !validUUID(session.ID) || !validUUID(session.DeviceID) || len(session.CredentialHash) != sha256.Size ||
		session.CreatedAt.IsZero() || session.LastUsedAt.Before(session.CreatedAt) || !session.ExpiresAt.After(session.CreatedAt) {
		return errors.New("store: invalid browser session")
	}
	return nil
}

func commit(transaction *sql.Tx, name string) error {
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: commit %s: %w", name, err)
	}
	return nil
}
