-- +goose Up
-- THE AI ASSISTANT'S CONVERSATIONS (task #269, docs/AI-ASSISTANT.md).
--
-- One row per conversation a person had with the assistant built into the
-- explorer. Kept on the server, for the person alone, so a conversation
-- follows them from the browser to the desktop app and a "waiting for your
-- approval" step survives a closed tab. Kept for the retention the
-- administrator set (Admin -> Settings -> AI assistant), counted from the
-- last time the conversation was used; the person can delete theirs at any
-- time (User settings -> AI assistant).
--
--   user_id     whose conversation it is. A deleted account takes its
--               conversations with it (ON DELETE CASCADE).
--   conv_key    a random key the server writes the row with, to read it back
--               the same way on every engine (PostgreSQL has no
--               LastInsertId, MySQL no RETURNING).
--   title       the first words the person wrote, cut short.
--   state       the conversation as one JSON document (internal/assistant):
--               the frozen system prompt, the messages, the tool steps with
--               their undo, the batch that waits for an approval.
--   rev         moves by one with every write (compare-and-set): two tabs
--               writing the same conversation cannot interleave.
--   created_ms, updated_ms
--               unix milliseconds - numbers compare the same on every engine.
CREATE TABLE IF NOT EXISTS assistant_conversations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    conv_key    TEXT NOT NULL UNIQUE,
    title       TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT '',
    rev         INTEGER NOT NULL DEFAULT 1,
    created_ms  INTEGER NOT NULL DEFAULT 0,
    updated_ms  INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_assistant_conversations_user ON assistant_conversations (user_id, updated_ms);
CREATE INDEX IF NOT EXISTS idx_assistant_conversations_updated ON assistant_conversations (updated_ms);

-- +goose Down
DROP TABLE IF EXISTS assistant_conversations;
