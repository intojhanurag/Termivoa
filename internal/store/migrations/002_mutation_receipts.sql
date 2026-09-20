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
