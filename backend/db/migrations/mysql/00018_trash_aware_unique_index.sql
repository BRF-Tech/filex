-- +goose Up
-- +goose StatementBegin

-- GitHub issue #5: the UNIQUE(storage_id, parent_id, name) key included
-- soft-deleted rows, so one stale trashed row permanently blocked the sync
-- worker from re-creating a node at the same (parent, name).
--
-- MySQL has no partial indexes, and simply adding a NULLABLE column to the
-- unique key would break uniqueness for LIVE rows too. Instead: a STORED
-- generated column `is_live` that is 1 for live rows and NULL for
-- soft-deleted ones. In a MySQL unique key any NULL member exempts the row,
-- so trashed rows never conflict, while live rows (is_live=1, NOT NULL)
-- keep full (storage_id, parent_id, name) uniqueness — exactly matching the
-- sqlite/postgres partial-index semantics.
ALTER TABLE nodes DROP INDEX idx_nodes_storage_parent_name;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE nodes ADD COLUMN is_live TINYINT
    GENERATED ALWAYS AS (IF(deleted_at IS NULL, 1, NULL)) STORED;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE nodes ADD UNIQUE KEY idx_nodes_storage_parent_name
    (storage_id, parent_id, name, is_live);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE nodes DROP INDEX idx_nodes_storage_parent_name;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE nodes DROP COLUMN is_live;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE nodes ADD UNIQUE KEY idx_nodes_storage_parent_name
    (storage_id, parent_id, name);
-- +goose StatementEnd
