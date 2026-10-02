package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

var (
	ErrComputerMissing  = errors.New("store: computer is not in this device's list")
	ErrComputerCapacity = errors.New("store: this device's computer list is full")
)

// AppBinding is the app origin a grant, pending pairing or device is bound to
// (docs/multi-computer.md). The zero value means "this computer's own origin
// only". The node fields pin the app's Tailscale node: display and pinning
// data, never authentication.
type AppBinding struct {
	Origin    string
	NodeID    string
	NodeOwner string // Empty for tagged nodes.
	NodeTags  []string
}

// Bound reports whether the binding names an app origin.
func (app AppBinding) Bound() bool { return app.Origin != "" }

// appSelect lists the app binding columns in the order appColumns scans them.
const appSelect = `app_origin, app_node_id, app_node_owner, app_node_tags`

type appColumns struct{ origin, nodeID, owner, tags sql.NullString }

func (columns *appColumns) targets() []any {
	return []any{&columns.origin, &columns.nodeID, &columns.owner, &columns.tags}
}

func (columns *appColumns) binding() (AppBinding, error) {
	if !columns.origin.Valid {
		return AppBinding{}, nil
	}
	app := AppBinding{Origin: columns.origin.String, NodeID: columns.nodeID.String, NodeOwner: columns.owner.String, NodeTags: []string{}}
	if columns.tags.Valid {
		if err := json.Unmarshal([]byte(columns.tags.String), &app.NodeTags); err != nil {
			return AppBinding{}, fmt.Errorf("store: read app node tags: %w", err)
		}
	}
	return app, nil
}

// columns returns the four column values (all NULL when unbound).
func (app AppBinding) columns() ([4]any, error) {
	if !app.Bound() {
		if app.NodeID != "" || app.NodeOwner != "" || len(app.NodeTags) > 0 {
			return [4]any{}, errors.New("store: app node without app origin")
		}
		return [4]any{}, nil
	}
	if app.NodeID == "" || len(app.NodeID) > 64 || len(app.Origin) > 261 || len(app.NodeOwner) > 256 {
		return [4]any{}, errors.New("store: invalid app binding")
	}
	tags := app.NodeTags
	if tags == nil {
		tags = []string{}
	}
	encoded, err := json.Marshal(tags)
	if err != nil {
		return [4]any{}, err
	}
	var owner any
	if app.NodeOwner != "" {
		owner = app.NodeOwner
	}
	return [4]any{app.Origin, app.NodeID, owner, string(encoded)}, nil
}

// BoundDevice is one live device bound to an app origin.
type BoundDevice struct {
	DeviceID string
	Origin   string
	NodeID   string
}

// ListBoundDevices returns unrevoked devices bound to an app origin, ordered
// by origin. live limits them to devices holding a usable credential at now
// (the same rule as the device cap); otherwise every unrevoked one is listed.
func (store *Store) ListBoundDevices(ctx context.Context, now time.Time, credentialIdle time.Duration, live bool) ([]BoundDevice, error) {
	query := `SELECT id, app_origin, app_node_id FROM devices WHERE app_origin IS NOT NULL AND revoked_at_ms IS NULL`
	args := []any{}
	if live {
		query += ` AND id IN (` + liveDevices + `)`
		args = append(args, millis(now), millis(now)-credentialIdle.Milliseconds())
	}
	rows, err := store.database.QueryContext(ctx, query+` ORDER BY app_origin, id`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list bound devices: %w", err)
	}
	defer rows.Close()
	var devices []BoundDevice
	for rows.Next() {
		var device BoundDevice
		if err := rows.Scan(&device.DeviceID, &device.Origin, &device.NodeID); err != nil {
			return nil, fmt.Errorf("store: scan bound device: %w", err)
		}
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

// PairingOrigins returns the distinct app origins of unexpired, unredeemed
// grants and unexpired pending pairings, sorted, at most limit.
func (store *Store) PairingOrigins(ctx context.Context, now time.Time, limit int) ([]string, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT app_origin FROM pairing_grants
		WHERE app_origin IS NOT NULL AND redeemed_at_ms IS NULL AND created_at_ms <= ?1 AND expires_at_ms > ?1
		UNION SELECT app_origin FROM pending_pairings
		WHERE app_origin IS NOT NULL AND created_at_ms <= ?1 AND expires_at_ms > ?1
		ORDER BY 1 LIMIT ?2`, millis(now), limit)
	if err != nil {
		return nil, fmt.Errorf("store: list pairing origins: %w", err)
	}
	defer rows.Close()
	var origins []string
	for rows.Next() {
		var origin string
		if err := rows.Scan(&origin); err != nil {
			return nil, fmt.Errorf("store: scan pairing origin: %w", err)
		}
		origins = append(origins, origin)
	}
	return origins, rows.Err()
}

// DeletePairingsVia deletes every grant and pending pairing bound to origin,
// so a revoked app origin cannot finish a pairing already under way.
func (store *Store) DeletePairingsVia(ctx context.Context, origin string) error {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin pairing removal: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	for _, table := range []string{"pending_pairings", "pairing_grants"} {
		if _, err := transaction.ExecContext(ctx, `DELETE FROM `+table+` WHERE app_origin = ?`, origin); err != nil {
			return fmt.Errorf("store: delete %s via origin: %w", table, err)
		}
	}
	return commit(transaction, "pairing removal")
}

// ComputerState is pending (short-lived, CSP only) or confirmed.
type ComputerState string

const (
	ComputerPending   ComputerState = "pending"
	ComputerConfirmed ComputerState = "confirmed"
)

// Computer is one entry of a device's computer list on the home computer.
type Computer struct {
	DeviceID  string
	Origin    string
	Label     string
	State     ComputerState
	ExpiresAt *time.Time // Set only while pending.
	CreatedAt time.Time
}

// PutComputer records a pending entry or confirms one, for one device, and
// returns the saved entry. Expired pending entries are deleted first.
//
//   - Pending inserts or refreshes a pending entry until expires. If the
//     origin is already confirmed, the confirmed entry is kept unchanged.
//   - Confirmed needs an existing entry (pending or confirmed): it confirms it
//     and sets its label (a rename when already confirmed).
//
// A new entry needs room: at most limit entries per device, both states.
func (store *Store) PutComputer(
	ctx context.Context, deviceID, origin, label string, state ComputerState, now, expires time.Time, limit int,
) (Computer, error) {
	if !validUUID(deviceID) || origin == "" || len(origin) > 261 || label == "" || len(label) > 64 ||
		!utf8.ValidString(label) || limit < 1 || (state == ComputerPending && !expires.After(now)) ||
		(state != ComputerPending && state != ComputerConfirmed) {
		return Computer{}, errors.New("store: invalid computer")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return Computer{}, fmt.Errorf("store: begin computer update: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	when := millis(now)
	if err := pruneComputers(ctx, transaction, deviceID, when); err != nil {
		return Computer{}, err
	}
	current, err := scanComputer(transaction.QueryRowContext(ctx, `SELECT `+computerColumns+`
		FROM computers WHERE device_id = ? AND origin = ?`, deviceID, origin))
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Computer{}, fmt.Errorf("store: read computer: %w", err)
	}
	switch {
	case state == ComputerPending && exists && current.State == ComputerConfirmed:
		return current, commit(transaction, "computer update")
	case state == ComputerConfirmed && !exists:
		return Computer{}, ErrComputerMissing
	case !exists:
		var count int
		if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM computers WHERE device_id = ?`,
			deviceID).Scan(&count); err != nil {
			return Computer{}, fmt.Errorf("store: count computers: %w", err)
		}
		if count >= limit {
			return Computer{}, ErrComputerCapacity
		}
	}
	saved := Computer{DeviceID: deviceID, Origin: origin, Label: label, State: state, CreatedAt: now.UTC().Truncate(time.Millisecond)}
	if exists {
		saved.CreatedAt = current.CreatedAt
	}
	var expiresAt any
	if state == ComputerPending {
		expiresAt = millis(expires)
		saved.ExpiresAt = timePointer(fromMillis(millis(expires)))
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO computers(device_id, origin, label, state, expires_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(device_id, origin) DO UPDATE SET
		label = excluded.label, state = excluded.state, expires_at_ms = excluded.expires_at_ms`,
		deviceID, origin, label, string(state), expiresAt, millis(saved.CreatedAt)); err != nil {
		return Computer{}, fmt.Errorf("store: save computer: %w", err)
	}
	return saved, commit(transaction, "computer update")
}

// ListComputers deletes the device's expired pending entries, then returns
// the rest in creation order.
func (store *Store) ListComputers(ctx context.Context, deviceID string, now time.Time) ([]Computer, error) {
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin computer list: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	if err := pruneComputers(ctx, transaction, deviceID, millis(now)); err != nil {
		return nil, err
	}
	rows, err := transaction.QueryContext(ctx, `SELECT `+computerColumns+` FROM computers
		WHERE device_id = ? ORDER BY created_at_ms, origin`, deviceID)
	if err != nil {
		return nil, fmt.Errorf("store: list computers: %w", err)
	}
	computers, err := scanComputers(rows)
	if err != nil {
		return nil, err
	}
	return computers, commit(transaction, "computer list")
}

// ListConfirmedComputers returns the confirmed entries of every device, at
// most limit, for local administration.
func (store *Store) ListConfirmedComputers(ctx context.Context, limit int) ([]Computer, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT `+computerColumns+` FROM computers
		WHERE state = 'confirmed' ORDER BY created_at_ms, device_id, origin LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list all computers: %w", err)
	}
	return scanComputers(rows)
}

// RemoveComputer removes origin from one device's list, or from every
// device's list when deviceID is empty. It returns the number of lists that
// held it.
func (store *Store) RemoveComputer(ctx context.Context, deviceID, origin string) (int, error) {
	query, args := `DELETE FROM computers WHERE origin = ?`, []any{origin}
	if deviceID != "" {
		query, args = query+` AND device_id = ?`, append(args, deviceID)
	}
	result, err := store.database.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("store: remove computer: %w", err)
	}
	removed, err := result.RowsAffected()
	return int(removed), err
}

const computerColumns = `device_id, origin, label, state, expires_at_ms, created_at_ms`

func pruneComputers(ctx context.Context, transaction *sql.Tx, deviceID string, now int64) error {
	if _, err := transaction.ExecContext(ctx, `DELETE FROM computers
		WHERE device_id = ? AND state = 'pending' AND expires_at_ms <= ?`, deviceID, now); err != nil {
		return fmt.Errorf("store: prune pending computers: %w", err)
	}
	return nil
}

func scanComputer(scanner rowScanner) (Computer, error) {
	var computer Computer
	var state string
	var expires sql.NullInt64
	var created int64
	if err := scanner.Scan(&computer.DeviceID, &computer.Origin, &computer.Label, &state, &expires, &created); err != nil {
		return Computer{}, err
	}
	computer.State, computer.ExpiresAt, computer.CreatedAt = ComputerState(state), timeFromNull(expires), fromMillis(created)
	return computer, nil
}

func scanComputers(rows *sql.Rows) ([]Computer, error) {
	defer rows.Close()
	var computers []Computer
	for rows.Next() {
		computer, err := scanComputer(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan computer: %w", err)
		}
		computers = append(computers, computer)
	}
	return computers, rows.Err()
}
