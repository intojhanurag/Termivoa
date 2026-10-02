package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	testBrokerA = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testBrokerB = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	testProject = "11111111-1111-4111-8111-111111111111"
	testProfile = "22222222-2222-4222-8222-222222222222"
)

var testTime = time.Date(2026, time.September, 20, 12, 0, 0, 123000000, time.UTC)

func TestOpenConfiguresAndReopensSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "phone-terminal.sqlite")
	store := openTestStore(t, path)

	assertPragma(t, store.database, "foreign_keys", "1")
	assertPragma(t, store.database, "busy_timeout", "5000")
	assertPragma(t, store.database, "journal_mode", "wal")
	if got := store.database.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("max open connections = %d, want 1", got)
	}
	assertFullSchema(t, store.database)
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened := openTestStore(t, path)
	defer reopened.Close()
	assertFullSchema(t, reopened.database)
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat database: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("database permissions = %o, want 600", got)
		}
	}
}

// assertFullSchema checks that a database holds the whole schema from
// 001_schema.sql and one ledger row with its checksum.
func assertFullSchema(t *testing.T, database *sql.DB) {
	t.Helper()
	var name string
	var checksum []byte
	var rows int
	if err := database.QueryRow(`SELECT count(*), name, checksum FROM schema_migrations`).Scan(&rows, &name, &checksum); err != nil {
		t.Fatalf("read schema ledger: %v", err)
	}
	digest := sha256.Sum256([]byte(schemaSQL))
	if rows != 1 || name != schemaName || string(checksum) != string(digest[:]) {
		t.Fatalf("schema ledger = %d rows, %q; want one row for %s with its checksum", rows, name, schemaName)
	}
	objects, err := database.Query(`SELECT type || ' ' || name FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' ORDER BY 1`)
	if err != nil {
		t.Fatalf("list schema: %v", err)
	}
	defer objects.Close()
	var got []string
	for objects.Next() {
		var object string
		if err := objects.Scan(&object); err != nil {
			t.Fatal(err)
		}
		got = append(got, object)
	}
	want := []string{
		"index browser_sessions_device_idx", "index computers_origin_idx", "index devices_app_origin_idx",
		"index mutation_receipts_created_idx", "index mutation_receipts_session_idx",
		"index push_subscriptions_device_idx", "index session_metadata_project_idx", "index session_metadata_state_idx",
		"table browser_sessions", "table computers", "table devices", "table mutation_receipts",
		"table pairing_grants", "table pending_pairings", "table projects", "table push_subscriptions",
		"table push_vapid_key", "table schema_migrations", "table session_metadata", "table settings",
		"table shell_profiles",
		"trigger mutation_receipts_snapshot_required_insert", "trigger mutation_receipts_snapshot_required_update",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("schema objects =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var strict int
	if err := database.QueryRow(`SELECT count(*) FROM pragma_table_list
		WHERE schema = 'main' AND name NOT LIKE 'sqlite_%' AND strict = 0`).Scan(&strict); err != nil || strict != 0 {
		t.Fatalf("non-STRICT tables = %d, %v; want 0", strict, err)
	}
}

func TestSchemaFailureRollsBack(t *testing.T) {
	database, err := openDatabase(context.Background(), filepath.Join(t.TempDir(), "broken.sqlite"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer database.Close()
	broken := `CREATE TABLE transient (id INTEGER PRIMARY KEY) STRICT;
		INSERT INTO table_that_does_not_exist(id) VALUES (1);`
	if err := applySchema(context.Background(), database, schemaName, broken, testTime.UnixMilli()); err == nil {
		t.Fatal("broken schema unexpectedly succeeded")
	}
	var tables int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("failed schema left %d table(s), %v", tables, err)
	}
}

// There are no migrations (AGENTS.md rule 26): a database made with another
// schema, such as one from an older build, is refused and left unchanged.
func TestOpenRefusesDatabaseWithAnotherSchema(t *testing.T) {
	for name, ledger := range map[string]string{
		"changed checksum": `UPDATE schema_migrations SET checksum = zeroblob(32)`,
		"renamed schema":   `UPDATE schema_migrations SET name = '001_sessions.sql'`,
		"older build":      `INSERT INTO schema_migrations VALUES (2, '002_mutation_receipts.sql', zeroblob(32), 1)`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "other.sqlite")
			openTestStore(t, path).Close()
			database, err := openDatabase(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(ledger); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if store, err := Open(context.Background(), path); err == nil || !strings.Contains(err.Error(), "does not match this build") {
				if store != nil {
					store.Close()
				}
				t.Fatalf("open with %s error = %v", name, err)
			}
		})
	}
}

func TestSessionForeignKeysUnicodeRoundTripAndLifecycle(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "sessions.sqlite"))
	defer store.Close()
	beginBroker(t, store, testBrokerA)

	orphan := fixtureSession("33333333-3333-4333-8333-333333333333", testBrokerA, "orphan")
	if err := store.CreateSession(context.Background(), orphan); err == nil {
		t.Fatal("session with missing project/profile unexpectedly succeeded")
	}
	seedParents(t, store)
	project, err := store.GetProject(context.Background(), testProject)
	if err != nil || project.DisplayName != "Résumé 東京" {
		t.Fatalf("project round trip = %+v, %v", project, err)
	}
	profile, err := store.GetShellProfile(context.Background(), testProfile)
	if err != nil || len(profile.Arguments) != 3 || profile.Arguments[2] != "emoji 🚀" {
		t.Fatalf("shell profile round trip = %+v, %v", profile, err)
	}

	session := fixtureSession("33333333-3333-4333-8333-333333333333", testBrokerA, "東京 e\u0301 🚀")
	if err := store.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("create session: %v", err)
	}
	got, err := store.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got.Title != session.Title || got.Columns != 80 || got.Rows != 24 || got.State != SessionStarting {
		t.Fatalf("session round trip = %+v", got)
	}

	runningAt := testTime.Add(time.Second)
	if err := store.TransitionSession(context.Background(), session.ID, session.Generation, SessionRunning, nil, runningAt); err != nil {
		t.Fatalf("transition running: %v", err)
	}
	if err := store.TransitionSession(context.Background(), session.ID, "44444444-4444-4444-8444-444444444444", SessionExiting, nil, runningAt); !errors.Is(err, ErrGenerationChanged) {
		t.Fatalf("wrong generation error = %v", err)
	}
	if err := store.RenameSession(context.Background(), session.ID, session.Generation, "stale rename", testTime); !errors.Is(err, ErrTimestampRegression) {
		t.Fatalf("backward rename error = %v", err)
	}
	if err := store.RenameSession(context.Background(), session.ID, session.Generation, "renamed ✓", runningAt); err != nil {
		t.Fatalf("rename session: %v", err)
	}
	if err := store.TransitionSession(context.Background(), session.ID, session.Generation, SessionExiting, nil, testTime.Add(500*time.Millisecond)); !errors.Is(err, ErrTimestampRegression) {
		t.Fatalf("backward transition error = %v", err)
	}
	unchanged, err := store.GetSession(context.Background(), session.ID)
	if err != nil || unchanged.State != SessionRunning || !unchanged.UpdatedAt.Equal(runningAt) {
		t.Fatalf("session after rejected timestamps = %+v, %v", unchanged, err)
	}
	if err := store.TransitionSession(context.Background(), session.ID, session.Generation, SessionExiting, nil, testTime.Add(2*time.Second)); err != nil {
		t.Fatalf("transition exiting: %v", err)
	}
	exitCode := int32(7)
	if err := store.TransitionSession(context.Background(), session.ID, session.Generation, SessionExited, &exitCode, testTime.Add(3*time.Second)); err != nil {
		t.Fatalf("transition exited: %v", err)
	}
	if err := store.TransitionSession(context.Background(), session.ID, session.Generation, SessionRunning, nil, testTime.Add(4*time.Second)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal transition error = %v", err)
	}
	got, err = store.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("get exited session: %v", err)
	}
	if got.Title != "renamed ✓" || got.State != SessionExited || got.StartedAt == nil || got.EndedAt == nil || got.ExitCode == nil || *got.ExitCode != 7 {
		t.Fatalf("exited session = %+v", got)
	}
}

func TestSessionTitleLegacyBoundaryRoundTrip(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "legacy-title.sqlite"))
	defer store.Close()
	beginBroker(t, store, testBrokerA)
	seedParents(t, store)

	title := strings.Repeat("x", 255) + "\n"
	session := fixtureSession("33333333-3333-4333-8333-333333333333", testBrokerA, title)
	if err := store.CreateSession(context.Background(), session); err != nil {
		t.Fatalf("create session with legacy title: %v", err)
	}
	got, err := store.GetSession(context.Background(), session.ID)
	if err != nil || got.Title != title {
		t.Fatalf("legacy title round trip bytes = %d, %v; want 256", len(got.Title), err)
	}
}

func TestProjectAndShellProfileRejectStaleOrRecreatedMetadata(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "metadata-order.sqlite"))
	defer store.Close()
	seedParents(t, store)

	newerProject := Project{
		ID: testProject, DisplayName: "new project", CanonicalPath: "/tmp/new project",
		CreatedAt: testTime, UpdatedAt: testTime.Add(10 * time.Second),
	}
	if err := store.PutProject(context.Background(), newerProject); err != nil {
		t.Fatalf("put newer project: %v", err)
	}
	staleProject := newerProject
	staleProject.DisplayName = "stale project"
	staleProject.UpdatedAt = testTime.Add(5 * time.Second)
	if err := store.PutProject(context.Background(), staleProject); !errors.Is(err, ErrTimestampRegression) {
		t.Fatalf("stale project error = %v", err)
	}
	recreatedProject := newerProject
	recreatedProject.CreatedAt = testTime.Add(time.Second)
	recreatedProject.UpdatedAt = testTime.Add(11 * time.Second)
	if err := store.PutProject(context.Background(), recreatedProject); !errors.Is(err, ErrMetadataConflict) {
		t.Fatalf("recreated project error = %v", err)
	}
	project, err := store.GetProject(context.Background(), testProject)
	if err != nil || project.DisplayName != newerProject.DisplayName || !project.UpdatedAt.Equal(newerProject.UpdatedAt) {
		t.Fatalf("project after rejected writes = %+v, %v", project, err)
	}

	newerProfile := ShellProfile{
		ID: testProfile, DisplayName: "new shell", Platform: PlatformDarwin,
		ExecutablePath: "/bin/zsh", Arguments: []string{"-l", "new"},
		CreatedAt: testTime, UpdatedAt: testTime.Add(10 * time.Second),
	}
	if err := store.PutShellProfile(context.Background(), newerProfile); err != nil {
		t.Fatalf("put newer shell profile: %v", err)
	}
	staleProfile := newerProfile
	staleProfile.DisplayName = "stale shell"
	staleProfile.Arguments = []string{"stale"}
	staleProfile.UpdatedAt = testTime.Add(5 * time.Second)
	if err := store.PutShellProfile(context.Background(), staleProfile); !errors.Is(err, ErrTimestampRegression) {
		t.Fatalf("stale shell profile error = %v", err)
	}
	recreatedProfile := newerProfile
	recreatedProfile.CreatedAt = testTime.Add(time.Second)
	recreatedProfile.UpdatedAt = testTime.Add(11 * time.Second)
	if err := store.PutShellProfile(context.Background(), recreatedProfile); !errors.Is(err, ErrMetadataConflict) {
		t.Fatalf("recreated shell profile error = %v", err)
	}
	profile, err := store.GetShellProfile(context.Background(), testProfile)
	if err != nil || profile.DisplayName != newerProfile.DisplayName || len(profile.Arguments) != 2 || profile.Arguments[1] != "new" {
		t.Fatalf("shell profile after rejected writes = %+v, %v", profile, err)
	}
}

func TestTerminalSessionRetentionAndListingAreBounded(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "session-retention.sqlite"))
	defer store.Close()
	store.retainedTerminalSessions = 2
	store.sessionListLimit = 3
	beginBroker(t, store, testBrokerA)
	seedParents(t, store)

	active := fixtureSession("35555555-5555-4555-8555-555555555550", testBrokerA, "active")
	if err := store.CreateSession(context.Background(), active); err != nil {
		t.Fatalf("create active session: %v", err)
	}
	terminalIDs := []string{
		"35555555-5555-4555-8555-555555555551",
		"35555555-5555-4555-8555-555555555552",
		"35555555-5555-4555-8555-555555555553",
	}
	for index, id := range terminalIDs {
		session := fixtureSession(id, testBrokerA, fmt.Sprintf("terminal-%d", index))
		if err := store.CreateSession(context.Background(), session); err != nil {
			t.Fatalf("create terminal session %d: %v", index, err)
		}
		mustTransition(t, store, session, SessionRunning, nil, index*2+1)
		code := int32(index)
		mustTransition(t, store, session, SessionExited, &code, index*2+2)
	}
	if _, err := store.GetSession(context.Background(), terminalIDs[0]); !errors.Is(err, ErrSessionMissing) {
		t.Fatalf("oldest retained session error = %v", err)
	}
	listed, err := store.ListSessions(context.Background())
	if err != nil {
		t.Fatalf("list bounded sessions: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("listed sessions = %d, want active plus two terminal", len(listed))
	}
	foundActive := false
	for _, session := range listed {
		foundActive = foundActive || session.ID == active.ID
	}
	if !foundActive {
		t.Fatal("bounded listing omitted the live session")
	}
}

func TestSessionValidationReportsTheRejectedBoundary(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "validation.sqlite"))
	defer store.Close()
	beginBroker(t, store, testBrokerA)
	seedParents(t, store)

	tests := []struct {
		name string
		edit func(*SessionMetadata)
		want string
	}{
		{name: "columns", edit: func(session *SessionMetadata) { session.Columns = 401 }, want: "columns"},
		{name: "rows", edit: func(session *SessionMetadata) { session.Rows = 4 }, want: "rows"},
		{name: "generation", edit: func(session *SessionMetadata) { session.Generation = "not-a-uuid" }, want: "session generation"},
		{name: "title NUL", edit: func(session *SessionMetadata) { session.Title = "bad\x00title" }, want: "session title"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := fixtureSession("39999999-9999-4999-8999-999999999999", testBrokerA, "valid")
			session.ID = session.ID[:35] + string(rune('0'+index))
			test.edit(&session)
			err := store.CreateSession(context.Background(), session)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want field %q", err, test.want)
			}
		})
	}
}

func TestBrokerRestartMarksOnlyPriorNonterminalSessionsLost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.sqlite")
	first := openTestStore(t, path)
	beginBroker(t, first, testBrokerA)
	seedParents(t, first)

	states := []SessionState{SessionStarting, SessionRunning, SessionExiting, SessionExited, SessionLost}
	ids := []string{
		"30000000-0000-4000-8000-000000000001",
		"30000000-0000-4000-8000-000000000002",
		"30000000-0000-4000-8000-000000000003",
		"30000000-0000-4000-8000-000000000004",
		"30000000-0000-4000-8000-000000000005",
	}
	for index, state := range states {
		session := fixtureSession(ids[index], testBrokerA, string(state))
		if err := first.CreateSession(context.Background(), session); err != nil {
			t.Fatalf("create %s session: %v", state, err)
		}
		switch state {
		case SessionRunning:
			mustTransition(t, first, session, SessionRunning, nil, 1)
		case SessionExiting:
			mustTransition(t, first, session, SessionRunning, nil, 1)
			mustTransition(t, first, session, SessionExiting, nil, 2)
		case SessionExited:
			mustTransition(t, first, session, SessionRunning, nil, 1)
			code := int32(0)
			mustTransition(t, first, session, SessionExited, &code, 2)
		case SessionLost:
			mustTransition(t, first, session, SessionLost, nil, 1)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second := openTestStore(t, path)
	defer second.Close()
	lost, err := second.BeginBrokerGeneration(context.Background(), testBrokerB, testTime.Add(-time.Second))
	if err != nil {
		t.Fatalf("begin replacement broker after clock rollback: %v", err)
	}
	if len(lost) != 3 {
		t.Fatalf("recovered lost count = %d, want 3", len(lost))
	}
	lostIDs := []string{lost[0].ID, lost[1].ID, lost[2].ID}
	sort.Strings(lostIDs)
	if strings.Join(lostIDs, ",") != strings.Join(ids[:3], ",") {
		t.Fatalf("lost IDs = %v, want %v", lostIDs, ids[:3])
	}
	for index, id := range ids {
		session, err := second.GetSession(context.Background(), id)
		if err != nil {
			t.Fatalf("get recovered session %s: %v", id, err)
		}
		want := states[index]
		if index < 3 {
			want = SessionLost
		}
		if session.State != want {
			t.Fatalf("session %s state = %s, want %s", id, session.State, want)
		}
		if index < 3 && session.EndedAt == nil {
			t.Fatalf("session %s recovery time is missing", id)
		}
	}
	starting, err := second.GetSession(context.Background(), ids[0])
	if err != nil {
		t.Fatalf("get recovered starting session: %v", err)
	}
	running, err := second.GetSession(context.Background(), ids[1])
	if err != nil {
		t.Fatalf("get recovered running session: %v", err)
	}
	exiting, err := second.GetSession(context.Background(), ids[2])
	if err != nil {
		t.Fatalf("get recovered exiting session: %v", err)
	}
	if !starting.EndedAt.Equal(testTime) {
		t.Fatalf("starting recovery time = %v, want %v", starting.EndedAt, testTime)
	}
	if !running.EndedAt.Equal(testTime.Add(time.Second)) {
		t.Fatalf("running recovery time = %v, want %v", running.EndedAt, testTime.Add(time.Second))
	}
	if !exiting.EndedAt.Equal(testTime.Add(2 * time.Second)) {
		t.Fatalf("exiting recovery time = %v, want %v", exiting.EndedAt, testTime.Add(2*time.Second))
	}

	current := fixtureSession("30000000-0000-4000-8000-000000000006", testBrokerB, "current")
	if err := second.CreateSession(context.Background(), current); err != nil {
		t.Fatalf("create current session: %v", err)
	}
	again, err := second.BeginBrokerGeneration(context.Background(), testBrokerB, testTime.Add(11*time.Second))
	if err != nil || len(again) != 0 {
		t.Fatalf("repeat generation = %d sessions, %v", len(again), err)
	}
	currentAfter, err := second.GetSession(context.Background(), current.ID)
	if err != nil || currentAfter.State != SessionStarting {
		t.Fatalf("current generation session = %+v, %v", currentAfter, err)
	}
}

func TestSchemaDoesNotPersistTerminalOrCredentialMaterial(t *testing.T) {
	store := openTestStore(t, filepath.Join(t.TempDir(), "schema.sqlite"))
	defer store.Close()
	rows, err := store.database.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatalf("list schema: %v", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close tables: %v", err)
	}
	disallowed := []string{"terminal", "output", "history", "lease", "token", "secret", "environment"}
	for _, table := range tables {
		columns, err := store.database.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		for columns.Next() {
			var ordinal, notNull, primaryKey int
			var name, dataType string
			var defaultValue any
			if err := columns.Scan(&ordinal, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
				t.Fatalf("scan %s column: %v", table, err)
			}
			lower := strings.ToLower(name)
			for _, forbidden := range disallowed {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("schema persists disallowed material in %s.%s", table, name)
				}
			}
		}
		if err := columns.Close(); err != nil {
			t.Fatalf("close %s columns: %v", table, err)
		}
	}
}

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store
}

func beginBroker(t *testing.T, store *Store, generation string) {
	t.Helper()
	if lost, err := store.BeginBrokerGeneration(context.Background(), generation, testTime); err != nil || len(lost) != 0 {
		t.Fatalf("begin broker: lost=%d, err=%v", len(lost), err)
	}
}

func seedParents(t *testing.T, store *Store) {
	t.Helper()
	if err := store.PutProject(context.Background(), Project{
		ID: testProject, DisplayName: "Résumé 東京", CanonicalPath: "/tmp/project with spaces", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatalf("put project: %v", err)
	}
	if err := store.PutShellProfile(context.Background(), ShellProfile{
		ID: testProfile, DisplayName: "zsh ✓", Platform: PlatformDarwin, ExecutablePath: "/bin/zsh",
		Arguments: []string{"-l", "path with spaces", "emoji 🚀"}, CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatalf("put shell profile: %v", err)
	}
}

func fixtureSession(id, broker, title string) SessionMetadata {
	generation := id[:14] + "4" + id[15:]
	return SessionMetadata{
		ID: id, Generation: generation, BrokerGeneration: broker, Title: title,
		ProjectID: testProject, ShellProfileID: testProfile, State: SessionStarting,
		Columns: 80, Rows: 24, CreatedAt: testTime, UpdatedAt: testTime,
	}
}

func mustTransition(t *testing.T, store *Store, session SessionMetadata, state SessionState, exitCode *int32, seconds int) {
	t.Helper()
	if err := store.TransitionSession(context.Background(), session.ID, session.Generation, state, exitCode, testTime.Add(time.Duration(seconds)*time.Second)); err != nil {
		t.Fatalf("transition %s to %s: %v", session.ID, state, err)
	}
}

func assertPragma(t *testing.T, database *sql.DB, name, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow(`PRAGMA ` + name).Scan(&got); err != nil {
		t.Fatalf("read pragma %s: %v", name, err)
	}
	if strings.ToLower(got) != want {
		t.Fatalf("pragma %s = %q, want %q", name, got, want)
	}
}

func TestSQLiteDSNUsesEmptyAuthority(t *testing.T) {
	const pragmas = "?_busy_timeout=5000&_defensive=1&_dqs=0&_foreign_keys=1&_journal_mode=WAL&_synchronous=NORMAL&_txlock=immediate"
	for input, want := range map[string]string{
		"/Users/a/x.db":             "file:///Users/a/x.db" + pragmas,
		"C:/Users/A B/x.db":         "file:///C:/Users/A%20B/x.db" + pragmas,
		"//server/share/dir/x.db":   "file:////server/share/dir/x.db" + pragmas,
		"/tmp/100% #1?/x.db":        "file:///tmp/100%25%20%231%3F/x.db" + pragmas,
		"D:/Phone Terminal/data.db": "file:///D:/Phone%20Terminal/data.db" + pragmas,
	} {
		if got := sqliteDSN(input); got != want {
			t.Errorf("sqliteDSN(%q) = %q, want %q", input, got, want)
		}
	}
}
