-- +goose Up
-- +goose StatementBegin

-- Per-folder catalogue state for the lazy catalogue (issue #45). See
-- sqlite/00059_catalogue_folders.sql for what each column means.
CREATE TABLE IF NOT EXISTS catalogue_folders (
    storage_id        BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path_hash         VARCHAR(64) NOT NULL,
    path              TEXT COLLATE "C" NOT NULL,
    depth             INTEGER NOT NULL DEFAULT 0,
    state             VARCHAR(16) NOT NULL DEFAULT 'uncatalogued',
    reconciled_at     TIMESTAMPTZ,
    visited_at        TIMESTAMPTZ,
    watched_at        TIMESTAMPTZ,
    reconcile_on_open BOOLEAN NOT NULL DEFAULT FALSE,
    entries           INTEGER NOT NULL DEFAULT 0,
    held_back         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (storage_id, path_hash)
);

CREATE INDEX IF NOT EXISTS idx_catalogue_folders_state ON catalogue_folders (storage_id, state, depth);
-- COLLATE "C" on path: "anything uncatalogued below this folder?" is the byte
-- range `path > '/a/' AND path < '/a0'`, which only a byte-ordered column
-- answers correctly (and from this index) on every engine.
CREATE INDEX IF NOT EXISTS idx_catalogue_folders_path ON catalogue_folders (storage_id, path);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_catalogue_folders_path;
DROP INDEX IF EXISTS idx_catalogue_folders_state;
DROP TABLE IF EXISTS catalogue_folders CASCADE;
-- +goose StatementEnd
