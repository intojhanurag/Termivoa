package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int64
	name    string
	sql     string
	apply   func(context.Context, *sql.Tx) error
}

func bundledMigrations() ([]migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("store: read embedded migrations: %w", err)
	}
	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		parts := strings.SplitN(strings.TrimSuffix(filename, ".sql"), "_", 2)
		version, parseErr := strconv.ParseInt(parts[0], 10, 64)
		if len(parts) != 2 || !strings.HasSuffix(filename, ".sql") || parseErr != nil || version <= 0 || parts[1] == "" {
			return nil, fmt.Errorf("store: invalid migration filename %q", entry.Name())
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read migration %q: %w", entry.Name(), err)
		}
		candidate := migration{version: version, name: filename, sql: string(contents)}
		if version == 3 {
			candidate.apply = backfillMutationReceiptSnapshots
		}
		migrations = append(migrations, candidate)
	}
	sort.Slice(migrations, func(left, right int) bool { return migrations[left].version < migrations[right].version })
	for index, candidate := range migrations {
		if candidate.version != int64(index+1) {
			return nil, fmt.Errorf("store: migrations must be contiguous from version 1, found %d", candidate.version)
		}
	}
	return migrations, nil
}

// applyMigrations applies every pending migration in one transaction. It
// runs with foreign keys off, as SQLite's table-rebuild procedure requires
// (migration 7), then checks every foreign key before it commits and turns
// foreign keys on again. The caller must close database if this fails.
func applyMigrations(ctx context.Context, database *sql.DB, migrations []migration, nowMillis int64) (returnErr error) {
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: reserve migration connection: %w", err)
	}
	defer connection.Close()
	// PRAGMA foreign_keys is a no-op inside a transaction, so it is set first.
	if _, err := connection.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return fmt.Errorf("store: disable foreign keys for migration: %w", err)
	}
	defer func() {
		if _, err := connection.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("store: re-enable foreign keys: %w", err))
		}
	}()
	transaction, err := connection.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin migration set: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck

	if _, err := transaction.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY CHECK (version > 0),
        name TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 128),
        checksum BLOB NOT NULL CHECK (length(checksum) = 32),
        applied_at_ms INTEGER NOT NULL
    ) STRICT`); err != nil {
		return fmt.Errorf("store: initialize migration ledger: %w", err)
	}

	applied := make(map[int64]struct {
		name     string
		checksum []byte
	})
	rows, err := transaction.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("store: read migration ledger: %w", err)
	}
	for rows.Next() {
		var version int64
		var name string
		var checksum []byte
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: scan migration ledger: %w", err)
		}
		applied[version] = struct {
			name     string
			checksum []byte
		}{name: name, checksum: append([]byte(nil), checksum...)}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("store: close migration ledger rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: iterate migration ledger: %w", err)
	}

	for version := range applied {
		if version < 1 || version > int64(len(migrations)) {
			return fmt.Errorf("store: database schema version %d is newer than this build", version)
		}
	}
	for _, candidate := range migrations {
		digest := sha256.Sum256([]byte(candidate.sql))
		if record, ok := applied[candidate.version]; ok {
			if record.name != candidate.name || !equalBytes(record.checksum, digest[:]) {
				return fmt.Errorf("store: migration %d checksum or name does not match this build", candidate.version)
			}
			continue
		}
		if _, err := transaction.ExecContext(ctx, candidate.sql); err != nil {
			return fmt.Errorf("store: apply migration %d: %w", candidate.version, err)
		}
		if candidate.apply != nil {
			if err := candidate.apply(ctx, transaction); err != nil {
				return fmt.Errorf("store: migrate data for version %d: %w", candidate.version, err)
			}
		}
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, checksum, applied_at_ms) VALUES (?, ?, ?, ?)`,
			candidate.version, candidate.name, digest[:], nowMillis,
		); err != nil {
			return fmt.Errorf("store: record migration %d: %w", candidate.version, err)
		}
	}
	var violations int
	if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil {
		return fmt.Errorf("store: check foreign keys after migration: %w", err)
	}
	if violations != 0 {
		return fmt.Errorf("store: migration left %d foreign key violation(s)", violations)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("store: commit migration set: %w", err)
	}
	return nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

var errNoMigrations = errors.New("store: no bundled migrations")
