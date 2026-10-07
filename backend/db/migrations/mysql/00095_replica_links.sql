-- +goose Up
-- EVERY STORAGE IN A FOLDER OF ITS OWN ON ITS REPLICATION TARGET (#186). The
-- model is written out in db/migrations/sqlite/00095_replica_links.sql.
--
-- ⚠ folder_key is VARCHAR with a BINARY collation: the key is already
-- lowercased by filex, and MySQL's default collation is accent-insensitive
-- too, so "Arşiv" and "Arsiv" would collide in a unique key that SQLite and
-- PostgreSQL keep apart. The table and its key are ONE statement: MySQL DDL
-- is not transactional.

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS replica_links (
    storage_id    BIGINT NOT NULL PRIMARY KEY,
    target_id     BIGINT NOT NULL DEFAULT 0,
    folder        VARCHAR(255) NOT NULL DEFAULT '',
    folder_key    VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL DEFAULT '',
    created_unix  BIGINT NOT NULL DEFAULT 0,
    UNIQUE INDEX idx_replica_links_target_folder (target_id, folder_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS replica_links;
-- +goose StatementEnd
