-- +goose Up
-- Per-folder catalogue state for the lazy catalogue (issue #45). See
-- sqlite/00059_catalogue_folders.sql for what each column means.
--
-- path is VARCHAR(2048) like nodes.path, but BINARY-collated: "anything
-- uncatalogued below this folder?" is the byte range `path > '/a/' AND
-- path < '/a0'`, and under the default accent- and case-insensitive collation
-- that range means something else. Only a 512-character prefix is indexed
-- (InnoDB's key length limit under utf8mb4); the engine re-checks the rest.

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS catalogue_folders (
    storage_id        BIGINT NOT NULL,
    path_hash         VARCHAR(64) NOT NULL,
    path              VARCHAR(2048) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL,
    depth             INT NOT NULL DEFAULT 0,
    state             VARCHAR(16) NOT NULL DEFAULT 'uncatalogued',
    reconciled_at     TIMESTAMP NULL DEFAULT NULL,
    visited_at        TIMESTAMP NULL DEFAULT NULL,
    watched_at        TIMESTAMP NULL DEFAULT NULL,
    reconcile_on_open TINYINT(1) NOT NULL DEFAULT 0,
    entries           INT NOT NULL DEFAULT 0,
    held_back         INT NOT NULL DEFAULT 0,
    PRIMARY KEY (storage_id, path_hash),
    INDEX idx_catalogue_folders_state (storage_id, state, depth),
    INDEX idx_catalogue_folders_path (storage_id, path(512)),
    CONSTRAINT fk_catalogue_folders_storage FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS catalogue_folders;
-- +goose StatementEnd
