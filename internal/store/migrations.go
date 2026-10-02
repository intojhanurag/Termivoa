package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
)

//go:embed migrations/001_schema.sql
var schemaSQL string

const schemaName = "001_schema.sql"

// applySchema creates the schema in a fresh database and records its name and
// checksum in schema_migrations. Termivoa keeps no migrations (AGENTS.md rule
// 26): a database made by a build with any other schema is refused, and the
// owner deletes it to start fresh. The caller must close database if this
// fails.
func applySchema(ctx context.Context, database *sql.DB, name, schema string, nowMillis int64) error {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin schema setup: %w", err)
	}
	defer transaction.Rollback() //nolint:errcheck

	if _, err := transaction.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        version INTEGER PRIMARY KEY CHECK (version > 0),
        name TEXT NOT NULL UNIQUE CHECK (length(name) BETWEEN 1 AND 128),
        checksum BLOB NOT NULL CHECK (length(checksum) = 32),
        applied_at_ms INTEGER NOT NULL
    ) STRICT`); err != nil {
		return fmt.Errorf("store: initialize schema ledger: %w", err)
	}
	digest := sha256.Sum256([]byte(schema))
	var count int
	if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		return fmt.Errorf("store: read schema ledger: %w", err)
	}
	if count == 0 {
		if _, err := transaction.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("store: create schema: %w", err)
		}
		if _, err := transaction.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, checksum, applied_at_ms) VALUES (1, ?, ?, ?)`,
			name, digest[:], nowMillis,
		); err != nil {
			return fmt.Errorf("store: record schema: %w", err)
		}
		return transaction.Commit()
	}
	var storedName string
	var checksum []byte
	err = transaction.QueryRowContext(ctx, `SELECT name, checksum FROM schema_migrations WHERE version = 1`).Scan(&storedName, &checksum)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: read schema ledger: %w", err)
	}
	if count != 1 || err != nil || storedName != name || !bytes.Equal(checksum, digest[:]) {
		return errors.New("store: database schema does not match this build; delete the database file to start fresh")
	}
	return transaction.Commit()
}
