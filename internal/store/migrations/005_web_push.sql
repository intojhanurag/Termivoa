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
