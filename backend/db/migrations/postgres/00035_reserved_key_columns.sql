-- +goose Up
-- See sqlite/00035_reserved_key_columns.sql. KEY is reserved in MySQL, which
-- borrows these statements from the SQLite Store; PostgreSQL accepts the old
-- name and the new one alike, and renames the column so the three engines keep
-- one schema and one set of queries.
ALTER TABLE settings        RENAME COLUMN key TO setting_key;
ALTER TABLE node_meta       RENAME COLUMN key TO meta_key;
ALTER TABLE user_node_meta  RENAME COLUMN key TO meta_key;

-- +goose Down
ALTER TABLE settings        RENAME COLUMN setting_key TO key;
ALTER TABLE node_meta       RENAME COLUMN meta_key TO key;
ALTER TABLE user_node_meta  RENAME COLUMN meta_key TO key;
