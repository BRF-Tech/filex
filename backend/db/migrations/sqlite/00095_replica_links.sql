-- +goose Up
-- +goose StatementBegin

-- EVERY STORAGE IN A FOLDER OF ITS OWN ON ITS REPLICATION TARGET (#186,
-- internal/replica folder.go, docs/REPLICATION.md).
--
-- Several storages may share one target and their paths are relative to each
-- storage, so each writes inside its own folder there. The folder is chosen
-- once, when the storage is first linked to the target (its name made safe for
-- every backend, with the storage id appended when another storage on the
-- target already uses that name), and kept here: renaming the storage does not
-- move it, a target switched off and on keeps it. Changing it is its own
-- operation. A storage linked before this migration gets its folder the first
-- time its driver is built.
--
-- storage_id    one row per storage
-- target_id     the target the folder is on
-- folder        the folder's name on the target
-- folder_key    folder lowercased: unique per target (a case-insensitive share
--               would merge two names that differ only in case)
-- created_unix  Unix seconds
CREATE TABLE IF NOT EXISTS replica_links (
    storage_id    INTEGER PRIMARY KEY,
    target_id     INTEGER NOT NULL DEFAULT 0,
    folder        TEXT NOT NULL DEFAULT '',
    folder_key    TEXT NOT NULL DEFAULT '',
    created_unix  INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_replica_links_target_folder ON replica_links (target_id, folder_key);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS replica_links;
-- +goose StatementEnd
