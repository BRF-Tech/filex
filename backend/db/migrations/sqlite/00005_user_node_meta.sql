-- +goose Up
-- +goose StatementBegin

-- Per-user node metadata: tags, starred, last_opened, etc.
-- Distinct from node_meta which is shared across all users for a node.
CREATE TABLE IF NOT EXISTS user_node_meta (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id INTEGER NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, node_id, key)
);
CREATE INDEX IF NOT EXISTS idx_user_node_meta_userkey ON user_node_meta(user_id, key, updated_at);
CREATE INDEX IF NOT EXISTS idx_user_node_meta_node ON user_node_meta(node_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_user_node_meta_node;
DROP INDEX IF EXISTS idx_user_node_meta_userkey;
DROP TABLE IF EXISTS user_node_meta;
-- +goose StatementEnd
