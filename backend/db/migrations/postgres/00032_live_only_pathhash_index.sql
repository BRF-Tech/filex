-- +goose Up
-- +goose StatementBegin

-- The storage sync used to UN-DELETE a soft-deleted row whenever it walked
-- past an object at that row's path: it cleared deleted_at and carried on.
-- The rule was written for a real problem — UNIQUE(storage_id, path_hash)
-- counted soft-deleted rows, so a stale trashed row permanently blocked a
-- fresh create at the same path — but it answered that problem by reviving
-- the trash, which is how a deleted file came back on its own and how an
-- infected file left quarantine at the next pass.
--
-- Migration 00018 already did exactly this to the (storage_id, parent_id,
-- name) index for the same reason (issue #5). path_hash was left global, so
-- it kept the resurrection rule alive. Make it live-only too: trashed rows
-- may now pile up at any path, and the sync can catalogue a genuinely
-- reappeared object as a NEW row instead of reviving another file's identity.
DROP INDEX IF EXISTS idx_nodes_storage_pathhash;
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_storage_pathhash
    ON nodes(storage_id, path_hash) WHERE deleted_at IS NULL;

-- A partial index cannot serve GetNodeByPathIncludingDeleted, which is the
-- lookup the sync uses to notice a trashed row at a path. Keep a plain one.
CREATE INDEX IF NOT EXISTS idx_nodes_storage_pathhash_all
    ON nodes(storage_id, path_hash);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_nodes_storage_pathhash_all;
DROP INDEX IF EXISTS idx_nodes_storage_pathhash;
CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_storage_pathhash
    ON nodes(storage_id, path_hash);
-- +goose StatementEnd
