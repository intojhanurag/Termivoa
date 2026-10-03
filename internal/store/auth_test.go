package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

var testLimits = PairingLimits{OutstandingGrants: 2, PendingPairings: 2, FailedPolls: 3, Devices: 2, CredentialIdle: 7 * 24 * time.Hour}

func testHash(label string) []byte {
	sum := sha256.Sum256([]byte(label))
	return sum[:]
}

func testUUID(number int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", number)
}

func testPending(number int, now time.Time) PendingPairing {
	return PendingPairing{
		ID: testUUID(1000 + number), PollHash: testHash(fmt.Sprint("poll", number)), VerificationCode: "123-456",
		DeviceLabel: "Phone ✓", CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}
}

func testIssue(number int, now time.Time) (Device, BrowserSession) {
	device := Device{ID: testUUID(2000 + number), Label: "ignored", CreatedAt: now, LastUsedAt: now}
	return device, BrowserSession{
		ID: testUUID(3000 + number), DeviceID: device.ID, CredentialHash: testHash(fmt.Sprint("credential", number)),
		CreatedAt: now, LastUsedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
}

func TestPairingGrantIsSingleUseUnderConcurrentRedemption(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "pair.sqlite"))
	defer store.Close()
	ctx := context.Background()
	grant := PairingGrant{ID: testUUID(1), Hash: testHash("grant"), CreatedAt: testTime, ExpiresAt: testTime.Add(2 * time.Minute)}
	if err := store.CreatePairingGrant(ctx, grant, testLimits.OutstandingGrants); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 8)
	for index := range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- store.RedeemPairingGrant(ctx, grant.Hash, testPending(index, testTime.Add(time.Second)), 16, nil)
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case !errors.Is(err, ErrPairingInvalid):
			t.Fatalf("unexpected redemption error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful redemptions = %d, want exactly 1", successes)
	}
	pending, err := store.ListPendingPairings(ctx, testTime.Add(time.Second), 16)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending after race = %d, %v", len(pending), err)
	}
}

// DeleteUnredeemedGrant frees the grant's slot; a redeemed grant stays with
// its pending pairing.
func TestDeleteUnredeemedGrant(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "delete.sqlite"))
	defer store.Close()
	ctx := context.Background()
	for index := range 2 {
		if err := store.CreatePairingGrant(ctx, PairingGrant{ID: testUUID(index + 1), Hash: testHash(fmt.Sprint("g", index)),
			CreatedAt: testTime, ExpiresAt: testTime.Add(2 * time.Minute)}, 8); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RedeemPairingGrant(ctx, testHash("g1"), testPending(1, testTime), 8, nil); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"g0", "g1", "unknown"} {
		if err := store.DeleteUnredeemedGrant(ctx, testHash(label)); err != nil {
			t.Fatalf("delete %s: %v", label, err)
		}
	}
	if err := store.DeleteUnredeemedGrant(ctx, []byte("short")); err == nil {
		t.Fatal("a malformed hash was accepted")
	}
	if err := store.RedeemPairingGrant(ctx, testHash("g0"), testPending(2, testTime), 8, nil); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("deleted grant redeemed: %v", err)
	}
	var grants int
	if err := store.database.QueryRowContext(ctx, `SELECT count(*) FROM pairing_grants`).Scan(&grants); err != nil || grants != 1 {
		t.Fatalf("grants left = %d, %v (want only the redeemed one)", grants, err)
	}
	if pending, err := store.ListPendingPairings(ctx, testTime, 8); err != nil || len(pending) != 1 {
		t.Fatalf("pending = %v, %v", pending, err)
	}
}

func TestPairingGrantExpiryCapacityAndRollback(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "limits.sqlite"))
	defer store.Close()
	ctx := context.Background()
	for index := range 2 {
		if err := store.CreatePairingGrant(ctx, PairingGrant{ID: testUUID(index + 1), Hash: testHash(fmt.Sprint("g", index)),
			CreatedAt: testTime, ExpiresAt: testTime.Add(2 * time.Minute)}, testLimits.OutstandingGrants); err != nil {
			t.Fatal(err)
		}
	}
	third := PairingGrant{ID: testUUID(3), Hash: testHash("g2"), CreatedAt: testTime, ExpiresAt: testTime.Add(2 * time.Minute)}
	if err := store.CreatePairingGrant(ctx, third, testLimits.OutstandingGrants); !errors.Is(err, ErrPairingCapacity) {
		t.Fatalf("outstanding grant capacity = %v", err)
	}
	// Pending capacity rolls back the consumption: the grant stays usable.
	if err := store.RedeemPairingGrant(ctx, testHash("g0"), testPending(1, testTime), 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.RedeemPairingGrant(ctx, testHash("g1"), testPending(2, testTime), 1, nil); !errors.Is(err, ErrPairingCapacity) {
		t.Fatalf("pending capacity = %v", err)
	}
	if err := store.RedeemPairingGrant(ctx, testHash("g1"), testPending(2, testTime), 2, nil); err != nil {
		t.Fatalf("grant was consumed by a rolled-back redemption: %v", err)
	}
	// The two-minute boundary is exclusive and expired grants are pruned.
	if err := store.CreatePairingGrant(ctx, third, 8); err != nil {
		t.Fatal(err)
	}
	if err := store.RedeemPairingGrant(ctx, third.Hash, testPending(3, third.ExpiresAt), 8, nil); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("expired grant redemption = %v", err)
	}
	if err := store.RedeemPairingGrant(ctx, testHash("unknown"), testPending(4, testTime), 8, nil); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("unknown grant redemption = %v", err)
	}
}

func TestPendingPollRequiresSecretApprovalAndIssuesOnce(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "poll.sqlite"))
	defer store.Close()
	ctx := context.Background()
	grant := PairingGrant{ID: testUUID(1), Hash: testHash("grant"), CreatedAt: testTime, ExpiresAt: testTime.Add(2 * time.Minute)}
	if err := store.CreatePairingGrant(ctx, grant, 2); err != nil {
		t.Fatal(err)
	}
	pending := testPending(1, testTime)
	if err := store.RedeemPairingGrant(ctx, grant.Hash, pending, 2, nil); err != nil {
		t.Fatal(err)
	}
	device, session := testIssue(1, testTime.Add(time.Minute))
	now := testTime.Add(time.Minute)
	if _, approved, err := store.CompletePendingPairing(ctx, pending.ID, pending.PollHash, now, device, session, testLimits, nil); err != nil || approved {
		t.Fatalf("unapproved poll = %v, %v", approved, err)
	}
	if _, _, err := store.CompletePendingPairing(ctx, pending.ID, testHash("wrong"), now, device, session, testLimits, nil); !errors.Is(err, ErrPendingSecret) {
		t.Fatalf("wrong poll secret = %v", err)
	}
	if err := store.ApprovePendingPairing(ctx, testUUID(9999), now); !errors.Is(err, ErrPendingMissing) {
		t.Fatalf("approval of a different pending ID = %v", err)
	}
	if err := store.ApprovePendingPairing(ctx, pending.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := store.ApprovePendingPairing(ctx, pending.ID, now); !errors.Is(err, ErrPendingApproved) {
		t.Fatalf("approval replay = %v", err)
	}
	if _, _, err := store.CompletePendingPairing(ctx, pending.ID, testHash("wrong"), now, device, session, testLimits, nil); !errors.Is(err, ErrPendingSecret) {
		t.Fatalf("approved wrong poll secret = %v", err)
	}
	issued, approved, err := store.CompletePendingPairing(ctx, pending.ID, pending.PollHash, now, device, session, testLimits, nil)
	if err != nil || !approved || issued.Label != pending.DeviceLabel {
		t.Fatalf("approved poll = %#v, %v, %v", issued, approved, err)
	}
	// Lost issuance response: the pending record is consumed, never replayable.
	if _, _, err := store.CompletePendingPairing(ctx, pending.ID, pending.PollHash, now, device, session, testLimits, nil); !errors.Is(err, ErrPendingMissing) {
		t.Fatalf("replayed issuance = %v", err)
	}
	found, owner, err := store.LookupCredential(ctx, session.CredentialHash)
	if err != nil || found.ID != session.ID || owner.ID != device.ID || found.RevokedAt != nil {
		t.Fatalf("credential lookup = %#v, %#v, %v", found, owner, err)
	}
	credentials, err := store.RevokeDevice(ctx, device.ID, now.Add(time.Minute))
	if err != nil || len(credentials) != 1 || credentials[0] != session.ID {
		t.Fatalf("revoke device = %v, %v", credentials, err)
	}
	found, owner, err = store.LookupCredential(ctx, session.CredentialHash)
	if err != nil || found.RevokedAt == nil || owner.RevokedAt == nil {
		t.Fatalf("revocation not durable: %#v %#v %v", found, owner, err)
	}
}

func TestPendingPollBurnsAfterFailuresAndExpires(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "burn.sqlite"))
	defer store.Close()
	ctx := context.Background()
	for index, grantName := range []string{"a", "b"} {
		if err := store.CreatePairingGrant(ctx, PairingGrant{ID: testUUID(index + 1), Hash: testHash(grantName),
			CreatedAt: testTime, ExpiresAt: testTime.Add(2 * time.Minute)}, 4); err != nil {
			t.Fatal(err)
		}
		if err := store.RedeemPairingGrant(ctx, testHash(grantName), testPending(index, testTime), 4, nil); err != nil {
			t.Fatal(err)
		}
	}
	burned, expired := testPending(0, testTime), testPending(1, testTime)
	device, session := testIssue(1, testTime)
	for attempt := range testLimits.FailedPolls {
		if _, _, err := store.CompletePendingPairing(ctx, burned.ID, testHash("wrong"), testTime, device, session, testLimits, nil); !errors.Is(err, ErrPendingSecret) {
			t.Fatalf("failed poll %d = %v", attempt, err)
		}
	}
	if _, _, err := store.CompletePendingPairing(ctx, burned.ID, burned.PollHash, testTime, device, session, testLimits, nil); !errors.Is(err, ErrPendingMissing) {
		t.Fatalf("burned pending still usable: %v", err)
	}
	if err := store.ApprovePendingPairing(ctx, expired.ID, expired.ExpiresAt); !errors.Is(err, ErrPendingMissing) {
		t.Fatalf("expired approval = %v", err)
	}
	if _, _, err := store.CompletePendingPairing(ctx, expired.ID, expired.PollHash, expired.ExpiresAt, device, session, testLimits, nil); !errors.Is(err, ErrPendingMissing) {
		t.Fatalf("expired poll = %v", err)
	}
}

func TestDeviceCapacityPrunesOnlyRevokedDevices(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "devices.sqlite"))
	defer store.Close()
	ctx := context.Background()
	pair := func(number int) error {
		grant := PairingGrant{ID: testUUID(number), Hash: testHash(fmt.Sprint("grant", number)), CreatedAt: testTime, ExpiresAt: testTime.Add(time.Minute)}
		pending := testPending(number, testTime)
		if err := store.CreatePairingGrant(ctx, grant, 8); err != nil {
			return err
		}
		if err := store.RedeemPairingGrant(ctx, grant.Hash, pending, 8, nil); err != nil {
			return err
		}
		if err := store.ApprovePendingPairing(ctx, pending.ID, testTime); err != nil {
			return err
		}
		device, session := testIssue(number, testTime)
		_, _, err := store.CompletePendingPairing(ctx, pending.ID, pending.PollHash, testTime, device, session, testLimits, nil)
		return err
	}
	for number := 1; number <= 2; number++ {
		if err := pair(number); err != nil {
			t.Fatal(err)
		}
	}
	if err := pair(3); !errors.Is(err, ErrDeviceCapacity) {
		t.Fatalf("active device capacity = %v", err)
	}
	first, _ := testIssue(1, testTime)
	if _, err := store.RevokeDevice(ctx, first.ID, testTime); err != nil {
		t.Fatal(err)
	}
	if err := pair(4); err != nil {
		t.Fatalf("revoked device was not pruned for capacity: %v", err)
	}
	devices, err := store.ListDevices(ctx, testTime, testLimits.CredentialIdle, 10)
	if err != nil || len(devices) != 2 {
		t.Fatalf("devices = %d, %v", len(devices), err)
	}
	for _, device := range devices {
		if device.ID == first.ID {
			t.Fatal("revoked device survived capacity pruning")
		}
	}
	if _, err := store.RevokeDevice(ctx, testUUID(9999), testTime); !errors.Is(err, ErrDeviceMissing) {
		t.Fatalf("missing device revoke = %v", err)
	}
}

// Expired-but-unrevoked devices do not count toward the cap; they stay listed
// until their row is needed. Revoked devices are pruned before expired ones,
// and a live device is never pruned.
func TestDeviceCapacityIgnoresExpiredDevices(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "expired.sqlite"))
	defer store.Close()
	ctx := context.Background()
	day := 24 * time.Hour
	pair := func(number int, now time.Time, lifetime time.Duration) error {
		grant := PairingGrant{ID: testUUID(number), Hash: testHash(fmt.Sprint("grant", number)), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
		pending := testPending(number, now)
		if err := store.CreatePairingGrant(ctx, grant, 8); err != nil {
			return err
		}
		if err := store.RedeemPairingGrant(ctx, grant.Hash, pending, 8, nil); err != nil {
			return err
		}
		if err := store.ApprovePendingPairing(ctx, pending.ID, now); err != nil {
			return err
		}
		device, session := testIssue(number, now)
		session.ExpiresAt = now.Add(lifetime)
		_, _, err := store.CompletePendingPairing(ctx, pending.ID, pending.PollHash, now, device, session, testLimits, nil)
		return err
	}
	listed := func(now time.Time, want ...int) {
		t.Helper()
		devices, err := store.ListDevices(ctx, now, testLimits.CredentialIdle, 10)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, device := range devices {
			got[device.ID] = true
		}
		if len(devices) != len(want) {
			t.Fatalf("devices = %d, want %v", len(devices), want)
		}
		for _, number := range want {
			if !got[testUUID(2000+number)] {
				t.Fatalf("device %d is not listed", number)
			}
		}
	}
	if err := pair(1, testTime, day); err != nil { // Absolute expiry after one day.
		t.Fatal(err)
	}
	if err := pair(2, testTime, 30*day); err != nil {
		t.Fatal(err)
	}
	if err := pair(3, testTime, 30*day); !errors.Is(err, ErrDeviceCapacity) {
		t.Fatalf("live device capacity = %v", err)
	}
	// Device 1 passed its absolute expiry without revocation.
	if err := pair(3, testTime.Add(2*day), 30*day); err != nil {
		t.Fatalf("absolutely expired device still counted: %v", err)
	}
	listed(testTime.Add(2*day), 2, 3)
	three, _ := testIssue(3, testTime)
	if _, err := store.RevokeDevice(ctx, three.ID, testTime.Add(2*day)); err != nil {
		t.Fatal(err)
	}
	// At day 8 device 2 is idle-expired (last use day 0). The revoked device
	// is pruned first; the expired one stays listed.
	later := testTime.Add(8 * day)
	if err := pair(4, later, 30*day); err != nil {
		t.Fatalf("pairing with revoked and idle-expired devices = %v", err)
	}
	listed(later, 2, 4)
	if err := pair(5, later, 30*day); err != nil {
		t.Fatalf("idle-expired device still counted: %v", err)
	}
	listed(later, 4, 5)
	if err := pair(6, later, 30*day); !errors.Is(err, ErrDeviceCapacity) {
		t.Fatalf("live devices were pruned: %v", err)
	}
	listed(later, 4, 5)
}

// TestDeviceCapacityAndStatusRejectClockRolledBackDevice covers a clock
// rollback: a device created "in the future" relative to a rolled-back now
// must not be treated as live, matching BrowserLifetime.Check, which fails
// closed when CreatedAt or LastUsedAt is after now. Before the fix,
// liveDevices ignored the created_at_ms/last_used_at_ms upper bounds, so the
// device wrongly reported active and wrongly occupied the device cap.
func TestDeviceCapacityAndStatusRejectClockRolledBackDevice(t *testing.T) {
	testStore := openTestStore(t, filepath.Join(t.TempDir(), "rollback.sqlite"))
	defer testStore.Close()
	ctx := context.Background()
	limits := PairingLimits{FailedPolls: 3, Devices: 1, CredentialIdle: 7 * 24 * time.Hour}
	pair := func(number int, now time.Time) error {
		grant := PairingGrant{ID: testUUID(number), Hash: testHash(fmt.Sprint("grant", number)), CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
		pending := testPending(number, now)
		if err := testStore.CreatePairingGrant(ctx, grant, 8); err != nil {
			return err
		}
		if err := testStore.RedeemPairingGrant(ctx, grant.Hash, pending, 8, nil); err != nil {
			return err
		}
		if err := testStore.ApprovePendingPairing(ctx, pending.ID, now); err != nil {
			return err
		}
		device, session := testIssue(number, now)
		_, _, err := testStore.CompletePendingPairing(ctx, pending.ID, pending.PollHash, now, device, session, limits, nil)
		return err
	}
	if err := pair(1, testTime); err != nil {
		t.Fatal(err)
	}
	rolledBack := testTime.Add(-time.Hour)

	devices, err := testStore.ListDevices(ctx, rolledBack, limits.CredentialIdle, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].Status != DeviceExpired {
		t.Fatalf("clock-rolled-back device status = %+v, want expired", devices)
	}

	if err := pair(2, rolledBack); err != nil {
		t.Fatalf("clock-rolled-back device wrongly counted toward the cap: %v", err)
	}
}

// TestDeviceCapacityExcludesPendingRevoked covers a revocation that could not
// be persisted to SQLite: the device row still looks live there, but the
// caller (auth.Service) already knows it is revoked in memory.
// PairingLimits.PendingRevoked must let the cap count exclude it so a real
// pairing is not blocked by a phantom active device.
func TestDeviceCapacityExcludesPendingRevoked(t *testing.T) {
	testStore := openTestStore(t, filepath.Join(t.TempDir(), "pending-revoked.sqlite"))
	defer testStore.Close()
	ctx := context.Background()
	base := PairingLimits{FailedPolls: 3, Devices: 1, CredentialIdle: 7 * 24 * time.Hour}
	pair := func(number int, pendingRevoked []string) error {
		grant := PairingGrant{ID: testUUID(number), Hash: testHash(fmt.Sprint("grant", number)), CreatedAt: testTime, ExpiresAt: testTime.Add(time.Minute)}
		pending := testPending(number, testTime)
		if err := testStore.CreatePairingGrant(ctx, grant, 8); err != nil {
			return err
		}
		if err := testStore.RedeemPairingGrant(ctx, grant.Hash, pending, 8, nil); err != nil {
			return err
		}
		if err := testStore.ApprovePendingPairing(ctx, pending.ID, testTime); err != nil {
			return err
		}
		device, session := testIssue(number, testTime)
		limits := base
		limits.PendingRevoked = pendingRevoked
		_, _, err := testStore.CompletePendingPairing(ctx, pending.ID, pending.PollHash, testTime, device, session, limits, nil)
		return err
	}
	if err := pair(1, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := testIssue(1, testTime)
	if err := pair(2, nil); !errors.Is(err, ErrDeviceCapacity) {
		t.Fatalf("cap not enforced without exclusion = %v", err)
	}
	if err := pair(3, []string{first.ID}); err != nil {
		t.Fatalf("pending-revoked device still counted toward the cap: %v", err)
	}
}

// TestListDevicesStatus checks every status ListDevices reports: active,
// expired by absolute expiry despite recent use, expired by 7-day inactivity
// despite a distant absolute expiry, and revoked (which overrides a live
// credential).
func TestListDevicesStatus(t *testing.T) {
	testStore := openTestStore(t, filepath.Join(t.TempDir(), "status.sqlite"))
	defer testStore.Close()
	ctx := context.Background()
	day := 24 * time.Hour
	checkTime := testTime.Add(30 * day)
	credentialIdle := 7 * day
	limits := PairingLimits{FailedPolls: 3, Devices: 8, CredentialIdle: credentialIdle}

	issue := func(number int, lastUsed, expires time.Time) Device {
		grant := PairingGrant{ID: testUUID(number), Hash: testHash(fmt.Sprint("grant", number)), CreatedAt: testTime, ExpiresAt: testTime.Add(time.Minute)}
		pending := testPending(number, testTime)
		if err := testStore.CreatePairingGrant(ctx, grant, 8); err != nil {
			t.Fatal(err)
		}
		if err := testStore.RedeemPairingGrant(ctx, grant.Hash, pending, 8, nil); err != nil {
			t.Fatal(err)
		}
		if err := testStore.ApprovePendingPairing(ctx, pending.ID, testTime); err != nil {
			t.Fatal(err)
		}
		device, session := testIssue(number, testTime)
		session.LastUsedAt, session.ExpiresAt = lastUsed, expires
		issued, approved, err := testStore.CompletePendingPairing(ctx, pending.ID, pending.PollHash, testTime, device, session, limits, nil)
		if err != nil || !approved {
			t.Fatalf("issue device %d = %v, %v", number, approved, err)
		}
		return issued
	}

	active := issue(1, checkTime.Add(-1*day), checkTime.Add(20*day))
	absoluteExpired := issue(2, checkTime.Add(-1*day), checkTime.Add(-time.Hour))
	inactiveExpired := issue(3, checkTime.Add(-8*day), checkTime.Add(20*day))
	revoked := issue(4, checkTime.Add(-1*day), checkTime.Add(20*day))
	if _, err := testStore.RevokeDevice(ctx, revoked.ID, testTime); err != nil {
		t.Fatal(err)
	}

	devices, err := testStore.ListDevices(ctx, checkTime, credentialIdle, 10)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]DeviceStatus{}
	for _, device := range devices {
		status[device.ID] = device.Status
	}
	for id, want := range map[string]DeviceStatus{
		active.ID: DeviceActive, absoluteExpired.ID: DeviceExpired,
		inactiveExpired.ID: DeviceExpired, revoked.ID: DeviceRevoked,
	} {
		if got := status[id]; got != want {
			t.Fatalf("device %s status = %q, want %q", id, got, want)
		}
	}
}

func TestSecretColumnsAcceptOnlyHashes(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "hashes.sqlite"))
	defer store.Close()
	// Only fixed-size hashes are accepted for secret-derived columns.
	for _, statement := range []string{
		`INSERT INTO pairing_grants(id, grant_hash, created_at_ms, expires_at_ms) VALUES ('` + testUUID(1) + `', x'00', 1, 2)`,
		`INSERT INTO pairing_grants(id, grant_hash, created_at_ms, expires_at_ms) VALUES ('` + testUUID(1) + `', 'plaintext-secret-value-000000000', 1, 2)`,
	} {
		if _, err := store.database.Exec(statement); err == nil {
			t.Fatalf("non-hash secret column accepted: %s", statement)
		}
	}
}

func TestRenameDeviceChangesOnlyThatLabel(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "rename.sqlite"))
	defer store.Close()
	ctx := context.Background()
	grant := PairingGrant{ID: testUUID(1), Hash: testHash("grant"), CreatedAt: testTime, ExpiresAt: testTime.Add(2 * time.Minute)}
	if err := store.CreatePairingGrant(ctx, grant, 2); err != nil {
		t.Fatal(err)
	}
	pending := testPending(1, testTime)
	if err := store.RedeemPairingGrant(ctx, grant.Hash, pending, 2, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.ApprovePendingPairing(ctx, pending.ID, testTime); err != nil {
		t.Fatal(err)
	}
	device, session := testIssue(1, testTime)
	if _, approved, err := store.CompletePendingPairing(ctx, pending.ID, pending.PollHash, testTime, device, session, testLimits, nil); err != nil || !approved {
		t.Fatalf("issue = %v, %v", approved, err)
	}
	if err := store.RenameDevice(ctx, device.ID, "Work phone ✓"); err != nil {
		t.Fatal(err)
	}
	if _, owner, err := store.LookupCredential(ctx, session.CredentialHash); err != nil || owner.Label != "Work phone ✓" {
		t.Fatalf("renamed device = %#v, %v", owner, err)
	}
	if err := store.RenameDevice(ctx, testUUID(9999), "Other"); !errors.Is(err, ErrDeviceMissing) {
		t.Fatalf("rename of a missing device = %v", err)
	}
	for _, label := range []string{"", string(make([]byte, 65)), "\xff"} {
		if err := store.RenameDevice(ctx, device.ID, label); err == nil {
			t.Fatalf("label %q accepted", label)
		}
	}
}
