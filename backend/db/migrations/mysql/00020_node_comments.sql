-- +goose Up
-- +goose StatementBegin

-- Node comments (v0.6 "Çalışma" wave). Flat chronological comment
-- threads on file/folder nodes, surfaced in the inspector panel.
--
-- Soft delete: `deleted_at` hides a row from listings but keeps it for
-- audit. Hard removal rides the nodes FK CASCADE (node hard-delete on
-- trash purge) plus an explicit purge-hook delete in internal/trash.
CREATE TABLE IF NOT EXISTS node_comments (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    node_id     BIGINT NOT NULL,
    user_id     BIGINT NOT NULL,
    body        TEXT NOT NULL,
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    deleted_at  DATETIME(6),
    INDEX idx_node_comments_node (node_id, deleted_at),
    CONSTRAINT fk_node_comments_node FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE,
    CONSTRAINT fk_node_comments_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS node_comments;
-- +goose StatementEnd
