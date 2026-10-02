package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"
)

var testApp = AppBinding{
	Origin: "https://mac.tailx.ts.net", NodeID: "nMac1CNTRL", NodeOwner: "ada@example.com", NodeTags: []string{},
}

// A node without an origin is refused by the schema itself.
func TestSchemaRefusesAppNodeWithoutOrigin(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "unbound.sqlite"))
	defer store.Close()
	now := itoa(testTime.UnixMilli())
	if _, err := store.database.Exec(`INSERT INTO devices(id, label, created_at_ms, last_used_at_ms, app_node_id)
		VALUES ('` + testUUID(3) + `', 'Phone', ` + now + `, ` + now + `, 'nX')`); err == nil {
		t.Fatal("app node without app origin accepted")
	}
}

// A bound grant carries its app binding to the pending pairing and the
// device. A refused check consumes nothing.
func TestAppBindingFlowsFromGrantToDevice(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "bound.sqlite"))
	defer store.Close()
	ctx := context.Background()
	grant := PairingGrant{ID: testUUID(1), Hash: testHash("grant"), CreatedAt: testTime, ExpiresAt: testTime.Add(2 * time.Minute), App: testApp}
	if err := store.CreatePairingGrant(ctx, grant, 8); err != nil {
		t.Fatal(err)
	}
	if origins, err := store.PairingOrigins(ctx, testTime, 16); err != nil || !reflect.DeepEqual(origins, []string{testApp.Origin}) {
		t.Fatalf("grant pairing origins = %v, %v", origins, err)
	}
	refuse := errors.New("refused")
	var seen AppBinding
	pending := testPending(1, testTime)
	if err := store.RedeemPairingGrant(ctx, grant.Hash, pending, 4, func(app AppBinding) error {
		seen = app
		return refuse
	}); !errors.Is(err, refuse) || !reflect.DeepEqual(seen, testApp) {
		t.Fatalf("refused redeem = %v, saw %+v", err, seen)
	}
	if err := store.RedeemPairingGrant(ctx, grant.Hash, pending, 4, nil); err != nil {
		t.Fatalf("grant was consumed by a refused redeem: %v", err)
	}
	listed, err := store.ListPendingPairings(ctx, testTime, 16)
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0].App, testApp) {
		t.Fatalf("pending = %+v, %v", listed, err)
	}
	if err := store.ApprovePendingPairing(ctx, pending.ID, testTime); err != nil {
		t.Fatal(err)
	}
	device, session := testIssue(1, testTime)
	if _, _, err := store.CompletePendingPairing(ctx, pending.ID, pending.PollHash, testTime, device, session, testLimits,
		func(AppBinding) error { return refuse }); !errors.Is(err, refuse) {
		t.Fatalf("refused poll = %v", err)
	}
	issued, approved, err := store.CompletePendingPairing(ctx, pending.ID, pending.PollHash, testTime, device, session, testLimits, nil)
	if err != nil || !approved || !reflect.DeepEqual(issued.App, testApp) {
		t.Fatalf("poll = %+v, %v, %v", issued, approved, err)
	}
	_, looked, err := store.LookupCredential(ctx, session.CredentialHash)
	if err != nil || !reflect.DeepEqual(looked.App, testApp) {
		t.Fatalf("credential device = %+v, %v", looked, err)
	}
	devices, err := store.ListDevices(ctx, testTime, time.Hour, 10)
	if err != nil || len(devices) != 1 || !reflect.DeepEqual(devices[0].App, testApp) {
		t.Fatalf("devices = %+v, %v", devices, err)
	}
	bound, err := store.ListBoundDevices(ctx, testTime, time.Hour, true)
	if err != nil || len(bound) != 1 || !reflect.DeepEqual(bound[0], BoundDevice{DeviceID: device.ID, App: testApp}) {
		t.Fatalf("bound = %+v, %v", bound, err)
	}
	if bound, _ := store.ListBoundDevices(ctx, testTime.Add(2*time.Hour), time.Hour, true); len(bound) != 0 {
		t.Fatalf("idle device still live: %+v", bound)
	}
}

func TestDeletePairingsVia(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "via.sqlite"))
	defer store.Close()
	ctx := context.Background()
	other := AppBinding{Origin: "https://pi.tailx.ts.net", NodeID: "nPi", NodeTags: []string{"tag:pi"}}
	for index, app := range []AppBinding{testApp, testApp, other, {}} {
		grant := PairingGrant{ID: testUUID(index + 1), Hash: testHash(itoa(int64(index))), CreatedAt: testTime,
			ExpiresAt: testTime.Add(2 * time.Minute), App: app}
		if err := store.CreatePairingGrant(ctx, grant, 8); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.RedeemPairingGrant(ctx, testHash("0"), testPending(1, testTime), 4, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePairingsVia(ctx, testApp.Origin); err != nil {
		t.Fatal(err)
	}
	if origins, _ := store.PairingOrigins(ctx, testTime, 16); !reflect.DeepEqual(origins, []string{other.Origin}) {
		t.Fatalf("origins after delete = %v", origins)
	}
	if pending, _ := store.ListPendingPairings(ctx, testTime, 16); len(pending) != 0 {
		t.Fatalf("pending via the origin survived: %+v", pending)
	}
	if err := store.RedeemPairingGrant(ctx, testHash("3"), testPending(2, testTime), 4, nil); err != nil {
		t.Fatalf("unbound grant was deleted: %v", err)
	}
}

func TestComputerListPendingConfirmLimitAndExpiry(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "computers.sqlite"))
	defer store.Close()
	ctx := context.Background()
	device, session := testIssue(1, testTime)
	insertDevice(t, store, device, session)
	other, otherSession := testIssue(2, testTime)
	insertDevice(t, store, other, otherSession)
	expires := testTime.Add(10 * time.Minute)
	put := func(deviceID, origin, label string, state ComputerState, now time.Time) (Computer, error) {
		return store.PutComputer(ctx, deviceID, origin, "nNode", label, state, now, now.Add(10*time.Minute), 8, nil)
	}

	if _, err := put(device.ID, "https://vps.tailx.ts.net", "vps", ComputerConfirmed, testTime); !errors.Is(err, ErrComputerMissing) {
		t.Fatalf("confirm before pending = %v", err)
	}
	saved, err := put(device.ID, "https://vps.tailx.ts.net", "vps", ComputerPending, testTime)
	if err != nil || saved.State != ComputerPending || saved.ExpiresAt == nil || !saved.ExpiresAt.Equal(expires) {
		t.Fatalf("pending = %+v, %v", saved, err)
	}
	if saved, err = put(device.ID, "https://vps.tailx.ts.net", "server", ComputerConfirmed, testTime.Add(time.Minute)); err != nil ||
		saved.State != ComputerConfirmed || saved.ExpiresAt != nil || saved.Label != "server" || !saved.CreatedAt.Equal(testTime) {
		t.Fatalf("confirmed = %+v, %v", saved, err)
	}
	// Pending again (pair again) keeps the confirmed entry; confirmed again renames.
	if saved, err = put(device.ID, "https://vps.tailx.ts.net", "new", ComputerPending, testTime); err != nil ||
		saved.State != ComputerConfirmed || saved.Label != "server" {
		t.Fatalf("pending over confirmed = %+v, %v", saved, err)
	}
	if saved, _ = put(device.ID, "https://vps.tailx.ts.net", "box", ComputerConfirmed, testTime); saved.Label != "box" || saved.NodeID != "nNode" {
		t.Fatalf("rename = %+v", saved)
	}
	// Confirming needs the node the entry was added for (SM2).
	if _, err := store.PutComputer(ctx, device.ID, "https://vps.tailx.ts.net", "nOther", "vps", ComputerConfirmed,
		testTime, expires, 8, nil); !errors.Is(err, ErrComputerNode) {
		t.Fatalf("confirm as another node = %v", err)
	}
	// The list is per device and holds at most 8 entries, both states.
	for index := range 7 {
		if _, err := put(device.ID, "https://pc"+itoa(int64(index))+".tailx.ts.net", "pc", ComputerPending, testTime); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := put(device.ID, "https://ninth.tailx.ts.net", "ninth", ComputerPending, testTime); !errors.Is(err, ErrComputerCapacity) {
		t.Fatalf("ninth entry = %v", err)
	}
	if _, err := put(other.ID, "https://ninth.tailx.ts.net", "ninth", ComputerPending, testTime); err != nil {
		t.Fatalf("other device's list is separate: %v", err)
	}
	// Expired pending entries are not listed. Listing only reads (L3); the
	// next PutComputer deletes them and frees their slots.
	listed, err := store.ListComputers(ctx, device.ID, expires)
	if err != nil || len(listed) != 1 || listed[0].Origin != "https://vps.tailx.ts.net" || listed[0].NodeID != "nNode" {
		t.Fatalf("after expiry = %+v, %v", listed, err)
	}
	countRows := func() (count int) {
		if err := store.database.QueryRow(`SELECT count(*) FROM computers WHERE device_id = ?`, device.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if count := countRows(); count != 8 {
		t.Fatalf("listing wrote: %d rows", count)
	}
	if _, err := put(device.ID, "https://ninth.tailx.ts.net", "ninth", ComputerPending, expires); err != nil {
		t.Fatalf("slot not freed: %v", err)
	}
	if count := countRows(); count != 2 {
		t.Fatalf("expired rows kept: %d", count)
	}
	if _, err := put(other.ID, "https://vps.tailx.ts.net", "vps", ComputerPending, expires); err != nil {
		t.Fatal(err)
	}
	if _, err := put(other.ID, "https://vps.tailx.ts.net", "vps", ComputerConfirmed, expires); err != nil {
		t.Fatal(err)
	}
	all, err := store.ListConfirmedComputers(ctx, 512)
	if err != nil || len(all) != 2 {
		t.Fatalf("confirmed of all devices = %+v, %v", all, err)
	}
	// A revoked device's list is not shown to local administration (L2).
	if _, err := store.database.Exec(`UPDATE devices SET revoked_at_ms = ? WHERE id = ?`, millis(expires), other.ID); err != nil {
		t.Fatal(err)
	}
	if all, err := store.ListConfirmedComputers(ctx, 512); err != nil || len(all) != 1 || all[0].DeviceID != device.ID {
		t.Fatalf("confirmed after revoking a device = %+v, %v", all, err)
	}
	if removed, err := store.RemoveComputer(ctx, device.ID, "https://ninth.tailx.ts.net"); err != nil || removed != 1 {
		t.Fatalf("remove one = %d, %v", removed, err)
	}
	if removed, err := store.RemoveComputer(ctx, "", "https://vps.tailx.ts.net"); err != nil || removed != 2 {
		t.Fatalf("remove everywhere = %d, %v", removed, err)
	}
	// Removing a device removes its list.
	if _, err := store.database.Exec(`DELETE FROM devices WHERE id = ?`, other.ID); err != nil {
		t.Fatal(err)
	}
	if listed, _ := store.ListComputers(ctx, other.ID, expires); len(listed) != 0 {
		t.Fatalf("list of deleted device = %+v", listed)
	}
}

// A peer that reinstalled Tailscale has a new node ID. Adding it again must
// replace the entry pinned to the old node (or to none, from before pinning)
// with a pending one for the new node, so it can be confirmed; and entries
// the caller reports stale must not hold slots of a full list.
func TestComputerAddAgainAfterNodeChange(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "computers.sqlite"))
	defer store.Close()
	ctx := context.Background()
	device, session := testIssue(1, testTime)
	insertDevice(t, store, device, session)
	expires := testTime.Add(10 * time.Minute)
	const vps = "https://vps.tailx.ts.net"
	put := func(origin, node string, state ComputerState, stale func(Computer) bool) (Computer, error) {
		return store.PutComputer(ctx, device.ID, origin, node, "vps", state, testTime, expires, 3, stale)
	}
	for _, step := range []struct {
		node  string
		state ComputerState
	}{{"nOld", ComputerPending}, {"nOld", ComputerConfirmed}} {
		if _, err := put(vps, step.node, step.state, nil); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := put(vps, "nNew", ComputerPending, nil)
	if err != nil || saved.State != ComputerPending || saved.NodeID != "nNew" || saved.ExpiresAt == nil {
		t.Fatalf("pending add for a new node = %+v, %v", saved, err)
	}
	if saved, err = put(vps, "nNew", ComputerConfirmed, nil); err != nil || saved.State != ComputerConfirmed {
		t.Fatalf("confirm the new node = %+v, %v", saved, err)
	}
	// An entry from before node pinning (NULL node) is replaced the same way.
	if _, err := store.database.Exec(`UPDATE computers SET node_id = NULL WHERE origin = ?`, vps); err != nil {
		t.Fatal(err)
	}
	if saved, err = put(vps, "nNew", ComputerPending, nil); err != nil || saved.State != ComputerPending || saved.NodeID != "nNew" {
		t.Fatalf("pending add over an unpinned entry = %+v, %v", saved, err)
	}
	// Full list: a stale entry makes room, a live one does not.
	for _, origin := range []string{"https://a.tailx.ts.net", "https://b.tailx.ts.net"} {
		if _, err := put(origin, "n", ComputerPending, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := put("https://c.tailx.ts.net", "n", ComputerPending, nil); !errors.Is(err, ErrComputerCapacity) {
		t.Fatalf("fourth entry = %v", err)
	}
	staleA := func(computer Computer) bool { return computer.Origin == "https://a.tailx.ts.net" }
	if _, err := put("https://c.tailx.ts.net", "n", ComputerPending, staleA); err != nil {
		t.Fatalf("stale entry kept its slot: %v", err)
	}
	listed, err := store.ListComputers(ctx, device.ID, testTime)
	if err != nil || len(listed) != 3 || slices.ContainsFunc(listed, staleA) {
		t.Fatalf("list after replacing a stale entry = %+v, %v", listed, err)
	}
}

func insertDevice(t *testing.T, store *Store, device Device, session BrowserSession) {
	t.Helper()
	if _, err := store.database.Exec(`INSERT INTO devices(id, label, created_at_ms, last_used_at_ms) VALUES (?, 'Phone', ?, ?)`,
		device.ID, millis(device.CreatedAt), millis(device.LastUsedAt)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.database.Exec(`INSERT INTO browser_sessions(id, device_id, credential_hash, created_at_ms, last_used_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)`, session.ID, device.ID, session.CredentialHash, millis(session.CreatedAt),
		millis(session.LastUsedAt), millis(session.ExpiresAt)); err != nil {
		t.Fatal(err)
	}
}

func itoa(value int64) string { return fmt.Sprint(value) }

func hexOf(value []byte) string { return fmt.Sprintf("%x", value) }
