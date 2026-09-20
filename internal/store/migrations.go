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
		migrations = append(migrations, migration{version: version, name: filename, sql: string(contents)})
	}
	sort.Slice(migrations, func(left, right int) bool { return migrations[left].version < migrations[right].version })
	for index, candidate := range migrations {
		if candidate.version != int64(index+1) {
			return nil, fmt.Errorf("store: migrations must be contiguous from version 1, found %d", candidate.version)
		}
	}
	return migrations, nil
}

func applyMigrations(ctx context.Context, database *sql.DB, migrations []migration, nowMillis int64) error {
	if _, err := database.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
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
	rows, err := database.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
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
		transaction, err := database.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("store: begin migration %d: %w", candidate.version, err)
		}
		if _, err := transaction.ExecContext(ctx, candidate.sql); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("store: apply migration %d: %w", candidate.version, err)
		}
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, checksum, applied_at_ms) VALUES (?, ?, ?, ?)`,
			candidate.version, candidate.name, digest[:], nowMillis,
		); err != nil {
			_ = transaction.Rollback()
			return fmt.Errorf("store: record migration %d: %w", candidate.version, err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("store: commit migration %d: %w", candidate.version, err)
		}
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
