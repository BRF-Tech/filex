-- +goose Up
-- See sqlite/00037_storage_uid.sql. A storage's name is its address on every
-- file protocol, so renaming one breaks every mount that used it; the uid is
-- the address that never moves.
--
-- gen_random_uuid() is in core since PostgreSQL 13, which is the minimum this
-- project supports (docs/DATABASES.md). It is volatile, so the UPDATE gives
-- each row its own value.
ALTER TABLE storages ADD COLUMN IF NOT EXISTS uid TEXT;

UPDATE storages SET uid = gen_random_uuid()::text WHERE uid IS NULL OR uid = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_storages_uid ON storages(uid);

-- +goose Down
DROP INDEX IF EXISTS idx_storages_uid;
