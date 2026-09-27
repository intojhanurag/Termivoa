package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Migration 7 rebuilds shell_profiles so Linux profiles are accepted. Rows
// and the session foreign key that points at them must survive, and foreign
// keys must be enforced again afterwards.
func TestLinuxShellProfileMigrationKeepsExistingRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "linux.sqlite")
	database, err := openDatabase(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := bundledMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, database, migrations[:6], testTime.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	const (
		projectID = "11111111-1111-4111-8111-111111111111"
		profileID = "22222222-2222-4222-8222-222222222222"
		sessionID = "33333333-3333-4333-8333-333333333333"
	)
	for _, statement := range []string{
		`INSERT INTO projects VALUES ('` + projectID + `', 'Home', '/home/ada', 1, 1, NULL)`,
		`INSERT INTO shell_profiles VALUES ('` + profileID + `', 'zsh (login)', 'darwin', '/bin/zsh', '["-l"]', 1, 1, NULL)`,
		`INSERT INTO session_metadata (id, generation, broker_generation, title, project_id, shell_profile_id, state,
			columns_count, rows_count, created_at_ms, updated_at_ms, ended_at_ms)
		 VALUES ('` + sessionID + `', '44444444-4444-4444-8444-444444444444', '55555555-5555-4555-8555-555555555555',
			'old', '` + projectID + `', '` + profileID + `', 'lost', 80, 24, 1, 1, 2)`,
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded := openTestStore(t, path)
	defer upgraded.Close()
	assertPragma(t, upgraded.database, "foreign_keys", "1")
	old, err := upgraded.GetShellProfile(ctx, profileID)
	if err != nil || old.Platform != PlatformDarwin || old.ExecutablePath != "/bin/zsh" {
		t.Fatalf("kept profile = %+v, %v", old, err)
	}
	linux := ShellProfile{
		ID: "66666666-6666-4666-8666-666666666666", DisplayName: "bash (login)", Platform: PlatformLinux,
		ExecutablePath: "/bin/bash", Arguments: []string{"-l"}, CreatedAt: testTime, UpdatedAt: testTime,
	}
	if err := upgraded.PutShellProfile(ctx, linux); err != nil {
		t.Fatalf("put Linux profile: %v", err)
	}
	if _, err := upgraded.database.ExecContext(ctx, `DELETE FROM shell_profiles WHERE id = ?`, profileID); err == nil {
		t.Fatal("deleting a profile a session still uses succeeded; foreign keys are off")
	}
	if _, err := upgraded.database.ExecContext(ctx,
		`INSERT INTO shell_profiles VALUES ('77777777-7777-4777-8777-777777777777', 'x', 'beos', '/bin/sh', '[]', 1, 1, NULL)`); err == nil {
		t.Fatal("unknown platform accepted")
	}
}
