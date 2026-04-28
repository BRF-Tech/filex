-- +goose Up
-- +goose StatementBegin

-- Per-user node metadata: tags, starred, last_opened, etc.
-- Distinct from node_meta which is shared across all users for a node.
CREATE TABLE IF NOT EXISTS user_node_meta (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    node_id BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, node_id, key)
);
CREATE INDEX IF NOT EXISTS idx_user_node_meta_userkey ON user_node_meta(user_id, key, updated_at);
CREATE INDEX IF NOT EXISTS idx_user_node_meta_node ON user_node_meta(node_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_user_node_meta_node;
DROP INDEX IF EXISTS idx_user_node_meta_userkey;
DROP TABLE IF EXISTS user_node_meta CASCADE;
-- +goose StatementEnd
