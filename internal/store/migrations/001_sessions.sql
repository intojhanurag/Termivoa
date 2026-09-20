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
    platform TEXT NOT NULL CHECK (platform IN ('darwin', 'windows', 'wsl')),
    executable_path TEXT NOT NULL CHECK (length(executable_path) BETWEEN 1 AND 32768),
    arguments_json TEXT NOT NULL CHECK (length(arguments_json) BETWEEN 2 AND 65536),
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    disabled_at_ms INTEGER
) STRICT;

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
