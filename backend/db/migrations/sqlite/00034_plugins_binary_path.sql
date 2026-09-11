-- +goose Up
-- Rename plugins.binary -> plugins.binary_path.
--
-- SQLite is the only engine that accepts `binary` as a column name, so 00029
-- created the table here and aborted on both of the others (issue #19): BINARY
-- is a reserved word in PostgreSQL and in MySQL. Those two now create
-- binary_path directly in 00029; SQLite installs — the only ones that ever got
-- a plugins table at all — are renamed here, so all three engines end on the
-- same schema and one set of queries fits every one of them.
ALTER TABLE plugins RENAME COLUMN binary TO binary_path;

-- +goose Down
ALTER TABLE plugins RENAME COLUMN binary_path TO binary;
