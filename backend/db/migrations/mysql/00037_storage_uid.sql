-- +goose Up
-- See sqlite/00037_storage_uid.sql. A storage's name is its address on every
-- file protocol, so renaming one breaks every mount that used it; the uid is
-- the address that never moves.
--
-- ⚠ Do not wrap these statements in a goose statement block: a wrapped block
-- reaches the server as one multi-statement query, which the MySQL driver
-- refuses unless the DSN opts into multiStatements.
--
-- ⚠ Column and index in ONE statement. MySQL has neither
-- ADD COLUMN IF NOT EXISTS nor CREATE INDEX IF NOT EXISTS, and its DDL is not
-- transactional, so two statements can leave a half-applied migration that
-- fails differently on the retry.
--
-- ⚠ VARCHAR(36), not TEXT: MySQL cannot put a UNIQUE index on a TEXT column
-- without a prefix length, and a prefix index is not a uniqueness guarantee.
ALTER TABLE storages ADD COLUMN uid VARCHAR(36) NULL, ADD UNIQUE KEY idx_storages_uid (uid);

UPDATE storages SET uid = lower(uuid()) WHERE uid IS NULL OR uid = '';

-- +goose Down
ALTER TABLE storages DROP INDEX idx_storages_uid;
