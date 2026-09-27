-- title_custom is 1 once a user renamed the session. Clients then keep its
-- title instead of showing the live working folder. Existing sessions count as
-- renamed only when their title differs from both default titles: the
-- project's display name and "Terminal".
ALTER TABLE session_metadata
    ADD COLUMN title_custom INTEGER NOT NULL DEFAULT 0 CHECK (title_custom IN (0, 1));

UPDATE session_metadata SET title_custom = 1
    WHERE title <> 'Terminal'
      AND title <> COALESCE((SELECT display_name FROM projects WHERE projects.id = session_metadata.project_id), '');
