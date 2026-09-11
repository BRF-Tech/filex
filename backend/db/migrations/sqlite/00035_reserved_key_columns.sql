-- +goose Up
-- Rename the three `key` columns away from a reserved word.
--
-- KEY is reserved in MySQL. SQLite and PostgreSQL both accept it unquoted, so
-- it survived in the shared statements the MySQL driver borrows from the
-- SQLite Store, and every one of them was a syntax error on MySQL — saving a
-- setting, starring a file, tagging one (issue #19). Quoting was not an
-- option: backticks are MySQL's spelling, double quotes are PostgreSQL's, and
-- Go raw string literals cannot contain a backtick at all. The name is the fix.
ALTER TABLE settings        RENAME COLUMN key TO setting_key;
ALTER TABLE node_meta       RENAME COLUMN key TO meta_key;
ALTER TABLE user_node_meta  RENAME COLUMN key TO meta_key;

-- +goose Down
ALTER TABLE settings        RENAME COLUMN setting_key TO key;
ALTER TABLE node_meta       RENAME COLUMN meta_key TO key;
ALTER TABLE user_node_meta  RENAME COLUMN meta_key TO key;
