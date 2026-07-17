-- +goose Up
-- +goose StatementBegin

-- GitHub issue #5: the UNIQUE(storage_id, parent_id, name) index included
-- soft-deleted rows, so one stale trashed row permanently blocked the sync
-- worker from re-creating a node at the same (parent, name) — every cycle
-- failed with a unique-constraint conflict ("sync: create node failed",
-- every 15 min, forever). Rebuild it as a partial index over LIVE rows only;
-- trashed rows can pile up freely without wedging future creates.
DROP INDEX IF EXISTS idx_nodes_storage_parent_name;
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_storage_parent_name
    ON nodes(storage_id, parent_id, name) WHERE deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_nodes_storage_parent_name;
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_storage_parent_name
    ON nodes(storage_id, parent_id, name);
-- +goose StatementEnd
