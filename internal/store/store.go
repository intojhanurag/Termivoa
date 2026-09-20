package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const busyTimeoutMillis = 5000

var (
	ErrSessionMissing        = errors.New("store: session does not exist")
	ErrProjectMissing        = errors.New("store: project does not exist")
	ErrShellProfileMissing   = errors.New("store: shell profile does not exist")
	ErrMetadataConflict      = errors.New("store: immutable metadata changed")
	ErrGenerationChanged     = errors.New("store: session generation changed")
	ErrInvalidTransition     = errors.New("store: invalid session lifecycle transition")
	ErrTimestampRegression   = errors.New("store: timestamp moved backward")
	ErrBrokerNotStarted      = errors.New("store: broker generation has not started")
	ErrBrokerAlreadyStarted  = errors.New("store: broker generation already started")
	ErrOperationConflict     = errors.New("store: operation ID was already used for a different request")
	ErrOperationReceiptLimit = errors.New("store: operation receipt capacity reached")
)

type Store struct {
	database *sql.DB
	mu       sync.RWMutex
	broker   string

	retainedTerminalSessions int
	sessionListLimit         int
	retainedTerminalReceipts int
	operationReceiptLimit    int
}

func Open(ctx context.Context, path string) (*Store, error) {
	if ctx == nil {
		return nil, errors.New("store: context must not be nil")
	}
	if path == "" {
		return nil, errors.New("store: database path must not be empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve database path: %w", err)
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}
	if closeErr := file.Close(); closeErr != nil {
		return nil, fmt.Errorf("store: close database file: %w", closeErr)
	}
	if err := os.Chmod(absolute, 0o600); err != nil {
		return nil, fmt.Errorf("store: restrict database file: %w", err)
	}

	database, err := openDatabase(ctx, absolute)
	if err != nil {
		return nil, err
	}
	migrations, err := bundledMigrations()
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	if len(migrations) == 0 {
		_ = database.Close()
		return nil, errNoMigrations
	}
	if err := applyMigrations(ctx, database, migrations, time.Now().UTC().UnixMilli()); err != nil {
		_ = database.Close()
		return nil, err
	}
	return &Store{
		database:                 database,
		retainedTerminalSessions: defaultRetainedTerminalSessions,
		sessionListLimit:         defaultSessionListLimit,
		retainedTerminalReceipts: defaultRetainedTerminalReceipts,
		operationReceiptLimit:    defaultOperationReceiptLimit,
	}, nil
}

func openDatabase(ctx context.Context, absolutePath string) (*sql.DB, error) {
	dsnURL := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolutePath)}
	query := dsnURL.Query()
	query.Set("_foreign_keys", "1")
	query.Set("_busy_timeout", fmt.Sprint(busyTimeoutMillis))
	query.Set("_journal_mode", "WAL")
	query.Set("_synchronous", "NORMAL")
	query.Set("_txlock", "immediate")
	query.Set("_dqs", "0")
	query.Set("_defensive", "1")
	dsnURL.RawQuery = query.Encode()
	database, err := sql.Open("sqlite", dsnURL.String())
	if err != nil {
		return nil, fmt.Errorf("store: open SQLite: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("store: initialize SQLite: %w", err)
	}
	return database, nil
}

func (store *Store) Close() error {
	if store == nil || store.database == nil {
		return nil
	}
	return store.database.Close()
}

func (store *Store) activeBroker() (string, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.broker == "" {
		return "", ErrBrokerNotStarted
	}
	return store.broker, nil
}
