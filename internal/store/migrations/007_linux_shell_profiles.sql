-- Linux hosts store their shell profiles as platform 'linux'. SQLite cannot
-- change a CHECK constraint in place, so the table is rebuilt with the
-- documented create-copy-drop-rename procedure; applyMigrations runs it with
-- foreign keys off and runs foreign_key_check before it commits.
CREATE TABLE shell_profiles_next (
    id TEXT PRIMARY KEY CHECK (length(id) = 36),
    display_name TEXT NOT NULL CHECK (length(display_name) BETWEEN 1 AND 128),
    platform TEXT NOT NULL CHECK (platform IN ('darwin', 'windows', 'wsl', 'linux')),
    executable_path TEXT NOT NULL CHECK (length(executable_path) BETWEEN 1 AND 32768),
    arguments_json TEXT NOT NULL CHECK (length(arguments_json) BETWEEN 2 AND 65536),
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    disabled_at_ms INTEGER
) STRICT;

INSERT INTO shell_profiles_next (
    id, display_name, platform, executable_path, arguments_json, created_at_ms, updated_at_ms, disabled_at_ms
)
SELECT id, display_name, platform, executable_path, arguments_json, created_at_ms, updated_at_ms, disabled_at_ms
FROM shell_profiles;

DROP TABLE shell_profiles;

ALTER TABLE shell_profiles_next RENAME TO shell_profiles;
