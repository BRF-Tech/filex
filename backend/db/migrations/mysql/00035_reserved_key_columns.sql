-- +goose Up
-- See sqlite/00035_reserved_key_columns.sql. The column had to be backticked
-- in every MySQL statement that touched it, and the shared Go statements it
-- borrows from the SQLite driver cannot carry a backtick — a raw string
-- literal in Go is itself delimited by one. Renaming it is what lets one
-- statement serve both engines.
--
-- ⚠ Do not wrap these statements in a goose statement block: a wrapped block
-- reaches the server as one multi-statement query, which the MySQL driver
-- refuses unless the DSN opts into multiStatements.
--
-- ⚠ RENAME COLUMN needs MySQL 8.0 / MariaDB 10.5.2. The dialect already
-- requires MySQL 8.0.13 for the expression defaults in 00001.
ALTER TABLE settings        RENAME COLUMN `key` TO setting_key;
ALTER TABLE node_meta       RENAME COLUMN `key` TO meta_key;
ALTER TABLE user_node_meta  RENAME COLUMN `key` TO meta_key;

-- +goose Down
ALTER TABLE settings        RENAME COLUMN setting_key TO `key`;
ALTER TABLE node_meta       RENAME COLUMN meta_key TO `key`;
ALTER TABLE user_node_meta  RENAME COLUMN meta_key TO `key`;
