-- Multi-computer (docs/multi-computer.md, section 4). A grant, pending
-- pairing or device may be bound to one app origin: the Termivoa app served by
-- another computer in the same tailnet. app_origin NULL means "this
-- computer's own origin only", so every existing row keeps its meaning.
-- app_node_* pin the app's Tailscale node: its stable ID, its owner's login
-- (NULL for tagged nodes) and its tags as a JSON array.
ALTER TABLE pairing_grants ADD COLUMN app_origin TEXT
    CHECK (app_origin IS NULL OR length(app_origin) BETWEEN 8 AND 261);
ALTER TABLE pairing_grants ADD COLUMN app_node_id TEXT
    CHECK ((app_node_id IS NULL) = (app_origin IS NULL) AND (app_node_id IS NULL OR length(app_node_id) BETWEEN 1 AND 64));
ALTER TABLE pairing_grants ADD COLUMN app_node_owner TEXT
    CHECK (app_node_owner IS NULL OR length(app_node_owner) BETWEEN 1 AND 256);
ALTER TABLE pairing_grants ADD COLUMN app_node_tags TEXT
    CHECK (app_node_tags IS NULL OR json_valid(app_node_tags));

ALTER TABLE pending_pairings ADD COLUMN app_origin TEXT
    CHECK (app_origin IS NULL OR length(app_origin) BETWEEN 8 AND 261);
ALTER TABLE pending_pairings ADD COLUMN app_node_id TEXT
    CHECK ((app_node_id IS NULL) = (app_origin IS NULL) AND (app_node_id IS NULL OR length(app_node_id) BETWEEN 1 AND 64));
ALTER TABLE pending_pairings ADD COLUMN app_node_owner TEXT
    CHECK (app_node_owner IS NULL OR length(app_node_owner) BETWEEN 1 AND 256);
ALTER TABLE pending_pairings ADD COLUMN app_node_tags TEXT
    CHECK (app_node_tags IS NULL OR json_valid(app_node_tags));

ALTER TABLE devices ADD COLUMN app_origin TEXT
    CHECK (app_origin IS NULL OR length(app_origin) BETWEEN 8 AND 261);
ALTER TABLE devices ADD COLUMN app_node_id TEXT
    CHECK ((app_node_id IS NULL) = (app_origin IS NULL) AND (app_node_id IS NULL OR length(app_node_id) BETWEEN 1 AND 64));
ALTER TABLE devices ADD COLUMN app_node_owner TEXT
    CHECK (app_node_owner IS NULL OR length(app_node_owner) BETWEEN 1 AND 256);
ALTER TABLE devices ADD COLUMN app_node_tags TEXT
    CHECK (app_node_tags IS NULL OR json_valid(app_node_tags));

CREATE INDEX devices_app_origin_idx ON devices(app_origin) WHERE app_origin IS NOT NULL;

-- The home computer's per-device list of peer computers. A pending row only
-- widens that device's CSP while it pairs and expires; a confirmed row has no
-- expiry. Removing a device removes its list.
CREATE TABLE computers (
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE ON UPDATE RESTRICT,
    origin TEXT NOT NULL CHECK (length(origin) BETWEEN 8 AND 261),
    label TEXT NOT NULL CHECK (length(label) BETWEEN 1 AND 64),
    state TEXT NOT NULL CHECK (state IN ('pending', 'confirmed')),
    expires_at_ms INTEGER,
    created_at_ms INTEGER NOT NULL,
    CHECK ((state = 'pending') = (expires_at_ms IS NOT NULL)),
    CHECK (expires_at_ms IS NULL OR expires_at_ms > created_at_ms),
    PRIMARY KEY (device_id, origin)
) STRICT;

CREATE INDEX computers_origin_idx ON computers(origin);
