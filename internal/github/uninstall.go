package github

import (
	"context"
	"errors"
	"path/filepath"
)

// DisconnectStoredLogin is one-shot offline maintenance after the broker has
// stopped. The caller must hold its exclusive data-directory lock through
// this call and the requested directory removal, using the canonical path.
// It never constructs a full Service, starts background work, scans Git,
// refreshes a token or exposes token strings. It uses Disconnect's existing
// restricted client and endpoint. Remote revocation remains best effort.
func DisconnectStoredLogin(ctx context.Context, dataDir string) (found, revoked bool, err error) {
	return disconnectStoredLogin(ctx, dataDir, defaultOptions)
}

func disconnectStoredLogin(ctx context.Context, dataDir string, opts options) (found, revoked bool, err error) {
	if err := ctx.Err(); err != nil {
		return false, false, err
	}
	if !filepath.IsAbs(dataDir) || filepath.Clean(dataDir) != dataDir {
		return false, false, errors.New("github: invalid cleanup directory")
	}
	acc, err := loadAccount(dataDir)
	if err != nil {
		return false, false, errors.New("github: stored login could not be read")
	}
	if acc == nil {
		return false, true, nil
	}
	// Only the existing request fields are initialized: no New constructor,
	// account ownership, polling, clone sweep or git subprocess is created.
	requestOnly := &Service{opts: opts, client: newHTTPClient(opts)}
	defer requestOnly.client.CloseIdleConnections()
	revoked = requestOnly.revoke(ctx, acc)
	if err := ctx.Err(); err != nil {
		return true, revoked, err
	}
	if err := deleteAccount(dataDir); err != nil {
		return true, revoked, errors.New("github: stored login could not be deleted")
	}
	return true, revoked, nil
}
