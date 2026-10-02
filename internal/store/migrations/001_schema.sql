CREATE TABLE projects (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    display_name TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 128),
    canonical_path TEXT NOT NULL UNIQUE CHECK (length(canonical_path) BETWEEN 1 AND 32768),
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    disabled_at_ms INTEGER
) STRICT;

CREATE TABLE shell_profiles (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    display_name TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 128),
    platform TEXT NOT NULL CHECK (platform IN ('darwin', 'windows', 'wsl', 'linux')),
    executable_path TEXT NOT NULL CHECK (length(executable_path) BETWEEN 1 AND 32768),
    arguments_json TEXT NOT NULL CHECK (length(arguments_json) BETWEEN 2 AND 65536),
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    disabled_at_ms INTEGER
) STRICT;

-- title_custom is 1 once a user renamed the session. Clients then keep its
-- title instead of showing the live working folder.
CREATE TABLE session_metadata (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    generation TEXT NOT NULL UNIQUE CHECK (length(generation) = 36),
    broker_generation TEXT NOT NULL CHECK (length(broker_generation) = 36),
    title TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 256),
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    shell_profile_id TEXT NOT NULL REFERENCES shell_profiles(id) ON DELETE RESTRICT ON UPDATE RESTRICT,
    state TEXT NOT NULL CHECK (state IN ('starting', 'running', 'exiting', 'exited', 'lost')),
    columns_count INTEGER NOT NULL CHECK (columns_count BETWEEN 20 AND 400),
    rows_count INTEGER NOT NULL CHECK (rows_count BETWEEN 5 AND 200),
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    started_at_ms INTEGER,
    ended_at_ms INTEGER,
    exit_code INTEGER CHECK (exit_code IS NULL OR exit_code BETWEEN -2147483648 AND 2147483647),
    title_custom INTEGER NOT NULL DEFAULT 0 CHECK (title_custom IN (0, 1)),
    CHECK (exit_code IS NULL OR state = 'exited'),
    CHECK (
        (state IN ('exited', 'lost') AND ended_at_ms IS NOT NULL) OR
        (state NOT IN ('exited', 'lost') AND ended_at_ms IS NULL)
    )
) STRICT;

CREATE INDEX session_metadata_state_idx
    ON session_metadata(state, updated_at_ms);

CREATE INDEX session_metadata_project_idx
    ON session_metadata(project_id, created_at_ms);

CREATE TABLE mutation_receipts (
    operation_id TEXT PRIMARY KEY CHECK (length(operation_id) = 36),
    operation_kind TEXT NOT NULL CHECK (operation_kind IN ('session_create', 'session_rename', 'session_terminate')),
    request_fingerprint BLOB NOT NULL CHECK (length(request_fingerprint) = 32),
    session_id TEXT NOT NULL REFERENCES session_metadata(id) ON DELETE CASCADE ON UPDATE RESTRICT,
    session_generation TEXT NOT NULL CHECK (length(session_generation) = 36),
    result_title TEXT CHECK (result_title IS NULL OR length(result_title) BETWEEN 1 AND 256),
    result_state TEXT NOT NULL CHECK (result_state IN ('starting', 'running', 'exiting', 'exited', 'lost')),
    result_at_ms INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    result_session_json TEXT,
    CHECK (
        (operation_kind = 'session_rename' AND result_title IS NOT NULL) OR
        (operation_kind <> 'session_rename' AND result_title IS NULL)
    ),
    CHECK (
        (operation_kind = 'session_create' AND result_state = 'running') OR
        (operation_kind = 'session_terminate' AND result_state = 'exiting') OR
        operation_kind = 'session_rename'
    )
) STRICT;

CREATE INDEX mutation_receipts_session_idx
    ON mutation_receipts(session_id, created_at_ms);

CREATE INDEX mutation_receipts_created_idx
    ON mutation_receipts(created_at_ms, operation_id);

CREATE TRIGGER mutation_receipts_snapshot_required_insert
BEFORE INSERT ON mutation_receipts
WHEN NEW.result_session_json IS NULL
BEGIN
    SELECT RAISE(ABORT, 'mutation receipt session snapshot is required');
END;

CREATE TRIGGER mutation_receipts_snapshot_required_update
BEFORE UPDATE OF result_session_json ON mutation_receipts
WHEN NEW.result_session_json IS NULL
BEGIN
    SELECT RAISE(ABORT, 'mutation receipt session snapshot is required');
END;

-- Multi-computer (docs/multi-computer.md, section 4). A device, pairing grant
-- or pending pairing may be bound to one app origin: the Termivoa app served
-- by another computer in the same tailnet. app_origin NULL means "this
-- computer's own origin only". app_node_* pin the app's Tailscale node: its
-- stable ID, its owner's login (NULL for tagged nodes) and its tags as a JSON
-- array.
CREATE TABLE devices (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    label TEXT NOT NULL CHECK (length(label) BETWEEN 1 AND 64),
    created_at_ms INTEGER NOT NULL,
    last_used_at_ms INTEGER NOT NULL CHECK (last_used_at_ms >= created_at_ms),
    revoked_at_ms INTEGER,
    app_origin TEXT CHECK (app_origin IS NULL OR length(app_origin) BETWEEN 8 AND 261),
    app_node_id TEXT CHECK ((app_node_id IS NULL) = (app_origin IS NULL) AND (app_node_id IS NULL OR length(app_node_id) BETWEEN 1 AND 64)),
    app_node_owner TEXT CHECK (app_node_owner IS NULL OR length(app_node_owner) BETWEEN 1 AND 256),
    app_node_tags TEXT CHECK (app_node_tags IS NULL OR json_valid(app_node_tags))
) STRICT;

CREATE INDEX devices_app_origin_idx ON devices(app_origin) WHERE app_origin IS NOT NULL;

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
    redeemed_at_ms INTEGER,
    app_origin TEXT CHECK (app_origin IS NULL OR length(app_origin) BETWEEN 8 AND 261),
    app_node_id TEXT CHECK ((app_node_id IS NULL) = (app_origin IS NULL) AND (app_node_id IS NULL OR length(app_node_id) BETWEEN 1 AND 64)),
    app_node_owner TEXT CHECK (app_node_owner IS NULL OR length(app_node_owner) BETWEEN 1 AND 256),
    app_node_tags TEXT CHECK (app_node_tags IS NULL OR json_valid(app_node_tags))
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
    failed_polls INTEGER NOT NULL DEFAULT 0 CHECK (failed_polls >= 0),
    app_origin TEXT CHECK (app_origin IS NULL OR length(app_origin) BETWEEN 8 AND 261),
    app_node_id TEXT CHECK ((app_node_id IS NULL) = (app_origin IS NULL) AND (app_node_id IS NULL OR length(app_node_id) BETWEEN 1 AND 64)),
    app_node_owner TEXT CHECK (app_node_owner IS NULL OR length(app_node_owner) BETWEEN 1 AND 256),
    app_node_tags TEXT CHECK (app_node_tags IS NULL OR json_valid(app_node_tags))
) STRICT;

CREATE TABLE settings (
    key TEXT PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 64),
    value TEXT NOT NULL CHECK (length(value) <= 4096),
    updated_at_ms INTEGER NOT NULL
) STRICT;

CREATE TABLE push_subscriptions (
    endpoint TEXT PRIMARY KEY CHECK (length(endpoint) BETWEEN 12 AND 2048),
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE ON UPDATE RESTRICT,
    p256dh BLOB NOT NULL CHECK (length(p256dh) = 65),
    auth BLOB NOT NULL CHECK (length(auth) = 16),
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE INDEX push_subscriptions_device_idx ON push_subscriptions(device_id, created_at_ms);

CREATE TABLE push_vapid_key (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    private_key BLOB NOT NULL CHECK (length(private_key) = 32),
    created_at_ms INTEGER NOT NULL
) STRICT;

-- Multi-computer (docs/multi-computer.md). The home computer's per-device list
-- of peer computers. A pending row only widens that device's CSP while it
-- pairs and expires; a confirmed row has no expiry. Removing a device removes
-- its list. node_id pins the Tailscale node of each computer when the app adds
-- it, and home confirms only the same node.
CREATE TABLE computers (
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE ON UPDATE RESTRICT,
    origin TEXT NOT NULL CHECK (length(origin) BETWEEN 8 AND 261),
    label TEXT NOT NULL CHECK (length(label) BETWEEN 1 AND 64),
    state TEXT NOT NULL CHECK (state IN ('pending', 'confirmed')),
    expires_at_ms INTEGER,
    created_at_ms INTEGER NOT NULL,
    node_id TEXT CHECK (node_id IS NULL OR length(node_id) BETWEEN 1 AND 64),
    CHECK ((state = 'pending') = (expires_at_ms IS NOT NULL)),
    CHECK (expires_at_ms IS NULL OR expires_at_ms > created_at_ms),
    PRIMARY KEY (device_id, origin)
) STRICT;

CREATE INDEX computers_origin_idx ON computers(origin);
