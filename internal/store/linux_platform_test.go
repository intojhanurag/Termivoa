package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Linux shell profiles are accepted next to existing ones, unknown platforms
// are refused, and the session foreign key on shell_profiles is enforced.
func TestLinuxShellProfilesAndForeignKeys(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t, filepath.Join(t.TempDir(), "linux.sqlite"))
	defer store.Close()
	database := store.database
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
	assertPragma(t, database, "foreign_keys", "1")
	old, err := store.GetShellProfile(ctx, profileID)
	if err != nil || old.Platform != PlatformDarwin || old.ExecutablePath != "/bin/zsh" {
		t.Fatalf("kept profile = %+v, %v", old, err)
	}
	linux := ShellProfile{
		ID: "66666666-6666-4666-8666-666666666666", DisplayName: "bash (login)", Platform: PlatformLinux,
		ExecutablePath: "/bin/bash", Arguments: []string{"-l"}, CreatedAt: testTime, UpdatedAt: testTime,
	}
	if err := store.PutShellProfile(ctx, linux); err != nil {
		t.Fatalf("put Linux profile: %v", err)
	}
	if _, err := store.database.ExecContext(ctx, `DELETE FROM shell_profiles WHERE id = ?`, profileID); err == nil {
		t.Fatal("deleting a profile a session still uses succeeded; foreign keys are off")
	}
	if _, err := store.database.ExecContext(ctx,
		`INSERT INTO shell_profiles VALUES ('77777777-7777-4777-8777-777777777777', 'x', 'beos', '/bin/sh', '[]', 1, 1, NULL)`); err == nil {
		t.Fatal("unknown platform accepted")
	}
}
