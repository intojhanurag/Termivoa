CREATE TABLE devices (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    label TEXT NOT NULL CHECK (length(label) BETWEEN 1 AND 64),
    created_at_ms INTEGER NOT NULL,
    last_used_at_ms INTEGER NOT NULL CHECK (last_used_at_ms >= created_at_ms),
    revoked_at_ms INTEGER
) STRICT;

CREATE TABLE browser_sessions (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE ON UPDATE RESTRICT,
    credential_hash BLOB NOT NULL UNIQUE CHECK (length(credential_hash) = 32),
    created_at_ms INTEGER NOT NULL,
    last_used_at_ms INTEGER NOT NULL CHECK (last_used_at_ms >= created_at_ms),
    expires_at_ms INTEGER NOT NULL CHECK (expires_at_ms > created_at_ms),
    revoked_at_ms INTEGER
) STRICT;

CREATE INDEX browser_sessions_device_idx ON browser_sessions(device_id);

CREATE TABLE pairing_grants (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    grant_hash BLOB NOT NULL UNIQUE CHECK (length(grant_hash) = 32),
    created_at_ms INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL CHECK (expires_at_ms > created_at_ms),
    redeemed_at_ms INTEGER
) STRICT;

CREATE TABLE pending_pairings (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    grant_id TEXT NOT NULL UNIQUE CHECK (length(grant_id) = 36),
    poll_hash BLOB NOT NULL UNIQUE CHECK (length(poll_hash) = 32),
    verification_code TEXT NOT NULL CHECK (length(verification_code) = 7),
    device_label TEXT NOT NULL CHECK (length(device_label) BETWEEN 1 AND 64),
    created_at_ms INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL CHECK (expires_at_ms > created_at_ms),
    approved_at_ms INTEGER,
    failed_polls INTEGER NOT NULL DEFAULT 0 CHECK (failed_polls >= 0)
) STRICT;

CREATE TABLE settings (
    key TEXT PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 64),
    value TEXT NOT NULL CHECK (length(value) <= 4096),
    updated_at_ms INTEGER NOT NULL
) STRICT;
