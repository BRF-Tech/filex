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
--   state is LONGTEXT: MySQL's TEXT holds 64 KiB, a conversation with a few
--   files read in it is more. Its default is the expression ('') - MySQL
--   takes no literal default on a TEXT column - so an INSERT that leaves it
--   out works here as it does on SQLite and PostgreSQL.
CREATE TABLE IF NOT EXISTS assistant_conversations (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    user_id     BIGINT NOT NULL,
    conv_key    VARCHAR(64) NOT NULL,
    title       VARCHAR(255) NOT NULL DEFAULT '',
    state       LONGTEXT NOT NULL DEFAULT (''),
    rev         BIGINT NOT NULL DEFAULT 1,
    created_ms  BIGINT NOT NULL DEFAULT 0,
    updated_ms  BIGINT NOT NULL DEFAULT 0,
    UNIQUE KEY uq_assistant_conversations_key (conv_key),
    INDEX idx_assistant_conversations_user (user_id, updated_ms),
    INDEX idx_assistant_conversations_updated (updated_ms),
    CONSTRAINT fk_assistant_conversations_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS assistant_conversations;
