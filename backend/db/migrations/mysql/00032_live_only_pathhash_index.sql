-- +goose Up
-- +goose StatementBegin

-- The sqlite/postgres twin of this migration makes idx_nodes_storage_pathhash
-- a PARTIAL unique index over live rows, so a soft-deleted row no longer
-- blocks a fresh create at the same path — which is what forced the sync
-- worker to un-delete trashed rows instead of catalogueing the object it
-- actually found.
--
-- MySQL has no partial indexes. Migration 00018 already solved that shape for
-- (storage_id, parent_id, name) with a STORED generated column `is_live`
-- (1 for live rows, NULL for soft-deleted ones): in a MySQL unique key any
-- NULL member exempts the row. Reuse the same column here — it already
-- exists, so this migration only rebuilds the key.
ALTER TABLE nodes DROP INDEX idx_nodes_storage_pathhash;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE nodes ADD UNIQUE KEY idx_nodes_storage_pathhash
    (storage_id, path_hash, is_live);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE nodes DROP INDEX idx_nodes_storage_pathhash;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE nodes ADD UNIQUE KEY idx_nodes_storage_pathhash
    (storage_id, path_hash);
-- +goose StatementEnd
