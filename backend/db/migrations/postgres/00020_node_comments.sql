-- +goose Up
-- +goose StatementBegin

-- Node comments (v0.6 "Çalışma" wave). Flat chronological comment
-- threads on file/folder nodes, surfaced in the inspector panel.
--
-- Soft delete: `deleted_at` hides a row from listings but keeps it for
-- audit. Hard removal rides the nodes FK CASCADE (node hard-delete on
-- trash purge) plus an explicit purge-hook delete in internal/trash.
CREATE TABLE IF NOT EXISTS node_comments (
    id          BIGSERIAL PRIMARY KEY,
    node_id     BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_node_comments_node
    ON node_comments (node_id, deleted_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS node_comments;
-- +goose StatementEnd
