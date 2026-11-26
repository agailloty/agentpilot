-- +goose Up
CREATE TABLE IF NOT EXISTS session_messages (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'done',
    exit_code INTEGER,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_session_messages_session_id ON session_messages(session_id, created_at);

ALTER TABLE sessions ADD COLUMN options_json TEXT NOT NULL DEFAULT '{}';

-- +goose Down
DROP INDEX IF EXISTS idx_session_messages_session_id;
DROP TABLE IF EXISTS session_messages;
