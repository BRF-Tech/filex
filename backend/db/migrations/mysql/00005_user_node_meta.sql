-- +goose Up
-- +goose StatementBegin

-- Per-user node metadata: tags, starred, last_opened, etc.
-- Distinct from node_meta which is shared across all users for a node.
CREATE TABLE IF NOT EXISTS user_node_meta (
    user_id BIGINT NOT NULL,
    node_id BIGINT NOT NULL,
    `key` VARCHAR(190) NOT NULL,
    value TEXT,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (user_id, node_id, `key`),
    KEY idx_user_node_meta_userkey (user_id, `key`, updated_at),
    KEY idx_user_node_meta_node (node_id),
    CONSTRAINT fk_unm_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_unm_node FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_node_meta;
-- +goose StatementEnd
