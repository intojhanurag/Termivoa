package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Migration 6 marks existing sessions as renamed only when their title is
// neither the project's display name nor the "Terminal" fallback.
func TestMigrationMarksOnlyRenamedSessionsCustom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "title-custom.sqlite")
	database, err := openDatabase(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := bundledMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), database, migrations[:5], testTime.UnixMilli()); err != nil {
		t.Fatalf("apply v5 schema: %v", err)
	}
	seedParents(t, &Store{database: database})
	titles := map[string]string{
		"70000000-0000-4000-8000-000000000101": "Résumé 東京", // The project's display name.
		"70000000-0000-4000-8000-000000000102": "Terminal",
		"70000000-0000-4000-8000-000000000103": "my build",
	}
	for id, title := range titles {
		session := fixtureSession(id, testBrokerA, title)
		if _, err := database.Exec(`INSERT INTO session_metadata(
			id, generation, broker_generation, title, project_id, shell_profile_id,
			state, columns_count, rows_count, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, 'starting', 80, 24, ?, ?)`,
			session.ID, session.Generation, session.BrokerGeneration, session.Title, session.ProjectID,
			session.ShellProfileID, millis(testTime), millis(testTime)); err != nil {
			t.Fatalf("insert v5 session: %v", err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded := openTestStore(t, path)
	defer upgraded.Close()
	for id, title := range titles {
		session, err := upgraded.GetSession(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if want := title == "my build"; session.TitleCustom != want {
			t.Errorf("%q custom = %t, want %t", title, session.TitleCustom, want)
		}
	}
}
