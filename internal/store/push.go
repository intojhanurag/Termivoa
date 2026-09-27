package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrPushCapacity reports that the broker-wide subscription cap is reached.
var ErrPushCapacity = errors.New("store: push subscription capacity reached")

// PushSubscription is one browser push subscription owned by a paired device.
// Endpoint is a capability URL: it is never logged or returned to a browser.
type PushSubscription struct {
	DeviceID string
	Endpoint string
	P256dh   []byte
	Auth     []byte
}

// LoadOrCreateVAPIDKey returns the saved VAPID private key, saving candidate
// first when none exists. The key lives only in this 0600 database.
func (store *Store) LoadOrCreateVAPIDKey(ctx context.Context, candidate []byte, now time.Time) ([]byte, error) {
	if len(candidate) != 32 {
		return nil, errors.New("store: invalid VAPID key")
	}
	if _, err := store.database.ExecContext(ctx, `INSERT INTO push_vapid_key(id, private_key, created_at_ms)
		VALUES (1, ?, ?) ON CONFLICT(id) DO NOTHING`, candidate, millis(now)); err != nil {
		return nil, fmt.Errorf("store: save VAPID key: %w", err)
	}
	var key []byte
	if err := store.database.QueryRowContext(ctx, `SELECT private_key FROM push_vapid_key WHERE id = 1`).Scan(&key); err != nil {
		return nil, fmt.Errorf("store: read VAPID key: %w", err)
	}
	return key, nil
}

// SavePushSubscription stores or refreshes a subscription for an unrevoked
// device. A device keeps at most perDevice subscriptions (the oldest are
// dropped, since browsers rotate endpoints); the broker keeps at most total.
func (store *Store) SavePushSubscription(
	ctx context.Context, subscription PushSubscription, perDevice, total int, now time.Time,
) error {
	if !validUUID(subscription.DeviceID) || len(subscription.Endpoint) < 12 || len(subscription.Endpoint) > 2048 ||
		len(subscription.P256dh) != 65 || len(subscription.Auth) != 16 || perDevice < 1 || total < 1 {
		return errors.New("store: invalid push subscription")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin push subscription: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck
	var live bool
	if err := transaction.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE id = ? AND revoked_at_ms IS NULL)`,
		subscription.DeviceID).Scan(&live); err != nil {
		return fmt.Errorf("store: check push device: %w", err)
	}
	if !live {
		return ErrDeviceMissing
	}
	// The endpoint is the key: one browser re-paired as a new device moves
	// its subscription instead of notifying both device rows.
	if _, err := transaction.ExecContext(ctx, `INSERT INTO push_subscriptions(endpoint, device_id, p256dh, auth, created_at_ms)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(endpoint) DO UPDATE SET device_id = excluded.device_id,
		p256dh = excluded.p256dh, auth = excluded.auth, created_at_ms = excluded.created_at_ms`,
		subscription.Endpoint, subscription.DeviceID, subscription.P256dh, subscription.Auth, millis(now)); err != nil {
		return fmt.Errorf("store: save push subscription: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE device_id = ?1 AND endpoint NOT IN (
		SELECT endpoint FROM push_subscriptions WHERE device_id = ?1 ORDER BY created_at_ms DESC, endpoint LIMIT ?2)`,
		subscription.DeviceID, perDevice); err != nil {
		return fmt.Errorf("store: trim push subscriptions: %w", err)
	}
	var count int
	if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM push_subscriptions`).Scan(&count); err != nil {
		return fmt.Errorf("store: count push subscriptions: %w", err)
	}
	if count > total {
		return ErrPushCapacity
	}
	return commit(transaction, "push subscription")
}

// DeletePushSubscription removes one endpoint. With a device ID it removes
// only that device's row, so a browser cannot delete another device's
// subscription; with "" it removes the endpoint for whoever owns it (the
// push service reported it gone).
func (store *Store) DeletePushSubscription(ctx context.Context, deviceID, endpoint string) error {
	query, args := `DELETE FROM push_subscriptions WHERE endpoint = ?`, []any{endpoint}
	if deviceID != "" {
		query, args = query+` AND device_id = ?`, append(args, deviceID)
	}
	if _, err := store.database.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("store: delete push subscription: %w", err)
	}
	return nil
}

// ListPushSubscriptions returns subscriptions of devices that hold a live
// credential now (the same rule as the device cap), newest first. A
// non-empty deviceID limits the result to that device.
func (store *Store) ListPushSubscriptions(
	ctx context.Context, deviceID string, now time.Time, credentialIdle time.Duration, limit int,
) ([]PushSubscription, error) {
	rows, err := store.database.QueryContext(ctx, `SELECT p.device_id, p.endpoint, p.p256dh, p.auth
		FROM push_subscriptions p JOIN devices d ON d.id = p.device_id
		WHERE d.revoked_at_ms IS NULL AND p.device_id IN (`+liveDevices+`) AND (?3 = '' OR p.device_id = ?3)
		ORDER BY p.created_at_ms DESC, p.endpoint LIMIT ?4`,
		millis(now), millis(now)-credentialIdle.Milliseconds(), deviceID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list push subscriptions: %w", err)
	}
	defer rows.Close()
	var subscriptions []PushSubscription
	for rows.Next() {
		var subscription PushSubscription
		if err := rows.Scan(&subscription.DeviceID, &subscription.Endpoint, &subscription.P256dh, &subscription.Auth); err != nil {
			return nil, fmt.Errorf("store: scan push subscription: %w", err)
		}
		subscriptions = append(subscriptions, subscription)
	}
	return subscriptions, rows.Err()
}

func deleteDevicePush(ctx context.Context, transaction *sql.Tx, deviceID string) error {
	if _, err := transaction.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE device_id = ?`, deviceID); err != nil {
		return fmt.Errorf("store: delete device push subscriptions: %w", err)
	}
	return nil
}
