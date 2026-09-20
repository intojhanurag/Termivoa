ALTER TABLE mutation_receipts ADD COLUMN result_session_json TEXT;

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
