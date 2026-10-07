-- +goose Up
-- +goose StatementBegin

-- EVERY STORAGE IN A FOLDER OF ITS OWN ON ITS REPLICATION TARGET (#186). The
-- model is written out in db/migrations/sqlite/00095_replica_links.sql: one
-- row per storage, the folder it writes into on its target, chosen once and
-- unique per target by its lowercased key.
CREATE TABLE IF NOT EXISTS replica_links (
    storage_id    BIGINT PRIMARY KEY,
    target_id     BIGINT NOT NULL DEFAULT 0,
    folder        TEXT NOT NULL DEFAULT '',
    folder_key    TEXT NOT NULL DEFAULT '',
    created_unix  BIGINT NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_replica_links_target_folder ON replica_links (target_id, folder_key);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS replica_links;
-- +goose StatementEnd
