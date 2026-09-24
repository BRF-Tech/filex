-- +goose Up
-- app_plugin_state keeps the file's path beside its hash.
--
-- The hash is what a lookup needs, but it is one-way: with only a hash an
-- app could never answer "which files am I keeping this key on?", which is
-- the question a home screen is. Joining the node table instead would work
-- only for files that have been indexed, so a document recorded seconds
-- after upload would be missing from the very list it belongs in.
ALTER TABLE app_plugin_state ADD COLUMN rel TEXT NOT NULL DEFAULT '';

-- +goose Down
-- Dropping a column is not worth a table rebuild; the column is harmless.
