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
	// ErrComputerNode refuses to confirm an entry that was added for another
	// Tailscale node: the name now belongs to a different machine.
	ErrComputerNode = errors.New("store: the computer is not the node it was added as")
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

// BoundDevice is one device bound to an app origin.
type BoundDevice struct {
	DeviceID string
	App      AppBinding
}

// ListBoundDevices returns unrevoked devices bound to an app origin, ordered
// by origin. live limits them to devices holding a usable credential at now
// (the same rule as the device cap); otherwise every unrevoked one is listed.
func (store *Store) ListBoundDevices(ctx context.Context, now time.Time, credentialIdle time.Duration, live bool) ([]BoundDevice, error) {
	query := `SELECT id, ` + appSelect + ` FROM devices WHERE app_origin IS NOT NULL AND revoked_at_ms IS NULL`
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
		var app appColumns
		if err := rows.Scan(append([]any{&device.DeviceID}, app.targets()...)...); err != nil {
			return nil, fmt.Errorf("store: scan bound device: %w", err)
		}
		if device.App, err = app.binding(); err != nil {
			return nil, err
		}
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

// BoundApps returns each distinct app binding of any device, revoked and
// expired ones included, ordered by origin: the origins whose app may still
// hold a cookie of this computer and must be able to read its 401.
func (store *Store) BoundApps(ctx context.Context) ([]AppBinding, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT DISTINCT `+appSelect+` FROM devices
		WHERE app_origin IS NOT NULL ORDER BY 1, 2`)
	if err != nil {
		return nil, fmt.Errorf("store: list bound apps: %w", err)
	}
	defer rows.Close()
	var apps []AppBinding
	for rows.Next() {
		var columns appColumns
		if err := rows.Scan(columns.targets()...); err != nil {
			return nil, fmt.Errorf("store: scan bound app: %w", err)
		}
		app, err := columns.binding()
		if err != nil {
			return nil, err
		}
		apps = append(apps, app)
	}
	return apps, rows.Err()
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
// NodeID is the Tailscale node home found at that origin when it was added;
// empty for entries from before node pinning.
type Computer struct {
	DeviceID  string
	Origin    string
	NodeID    string
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
//   - Confirmed needs an existing entry (pending or confirmed) pinned to the
//     same nodeID: it confirms it and sets its label (a rename when already
//     confirmed). Another node is ErrComputerNode.
//
// A new entry needs room: at most limit entries per device, both states.
func (store *Store) PutComputer(
	ctx context.Context, deviceID, origin, nodeID, label string, state ComputerState, now, expires time.Time, limit int,
) (Computer, error) {
	if !validUUID(deviceID) || origin == "" || len(origin) > 261 || nodeID == "" || len(nodeID) > 64 || label == "" || len(label) > 64 ||
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
	case state == ComputerConfirmed && current.NodeID != nodeID:
		return Computer{}, ErrComputerNode
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
	saved := Computer{DeviceID: deviceID, Origin: origin, NodeID: nodeID, Label: label, State: state, CreatedAt: now.UTC().Truncate(time.Millisecond)}
	if exists {
		saved.CreatedAt = current.CreatedAt
	}
	var expiresAt any
	if state == ComputerPending {
		expiresAt = millis(expires)
		saved.ExpiresAt = timePointer(fromMillis(millis(expires)))
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO computers(device_id, origin, node_id, label, state, expires_at_ms, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(device_id, origin) DO UPDATE SET
		node_id = excluded.node_id, label = excluded.label, state = excluded.state, expires_at_ms = excluded.expires_at_ms`,
		deviceID, origin, nodeID, label, string(state), expiresAt, millis(saved.CreatedAt)); err != nil {
		return Computer{}, fmt.Errorf("store: save computer: %w", err)
	}
	return saved, commit(transaction, "computer update")
}

// ListComputers returns the device's confirmed and unexpired pending entries
// in creation order. It only reads: it runs on every app load, and PutComputer
// deletes expired pending entries.
func (store *Store) ListComputers(ctx context.Context, deviceID string, now time.Time) ([]Computer, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT `+computerColumns+` FROM computers
		WHERE device_id = ? AND (state = 'confirmed' OR expires_at_ms > ?) ORDER BY created_at_ms, origin`,
		deviceID, millis(now))
	if err != nil {
		return nil, fmt.Errorf("store: list computers: %w", err)
	}
	return scanComputers(rows)
}

// ListConfirmedComputers returns the confirmed entries of every unrevoked
// device, at most limit, for local administration.
func (store *Store) ListConfirmedComputers(ctx context.Context, limit int) ([]Computer, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT `+computerColumns+` FROM computers
		WHERE state = 'confirmed' AND device_id IN (SELECT id FROM devices WHERE revoked_at_ms IS NULL)
		ORDER BY created_at_ms, device_id, origin LIMIT ?`, limit)
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

const computerColumns = `device_id, origin, node_id, label, state, expires_at_ms, created_at_ms`

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
	var node sql.NullString
	var expires sql.NullInt64
	var created int64
	if err := scanner.Scan(&computer.DeviceID, &computer.Origin, &node, &computer.Label, &state, &expires, &created); err != nil {
		return Computer{}, err
	}
	computer.NodeID, computer.State, computer.ExpiresAt, computer.CreatedAt = node.String, ComputerState(state), timeFromNull(expires), fromMillis(created)
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
