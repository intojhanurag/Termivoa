-- Multi-computer (docs/multi-computer.md, "Pairing a second computer"). Home
-- pins the Tailscale node of each computer in a device's list when the app
-- adds it, and confirms only the same node. NULL (rows from before this
-- migration) never matches a node, so such an entry must be added again.
ALTER TABLE computers ADD COLUMN node_id TEXT
    CHECK (node_id IS NULL OR length(node_id) BETWEEN 1 AND 64);
