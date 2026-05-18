-- +goose Up
-- Replication targets — separate entity from `storages`. A target
-- is purely a fan-out sink; operators never write to it directly,
-- it never shows up in the Depolar / Explore listings.
--
-- The previous v0.1.16 design tried to model the target inside the
-- storages table (role='replica' + replica_of_id pointing back), but
-- that polluted every multi-storage listing with a row the user
-- couldn't navigate. The new schema:
--
--   replication_targets         one row per backup target
--   storages.replica_target_id  optional FK — primary→target link
--
-- Existing role='replica' rows are migrated into the new table,
-- their previous primary's replica_of_id is rewritten to point at
-- the new target id, and the old role/replica_of_id columns stay
-- in place (SQLite can't DROP COLUMN cleanly) but become unused.

CREATE TABLE IF NOT EXISTS replication_targets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT    NOT NULL UNIQUE,
    driver      TEXT    NOT NULL,
    config_json TEXT    NOT NULL DEFAULT '{}',
    mode        TEXT    NOT NULL DEFAULT 'async',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

ALTER TABLE storages ADD COLUMN replica_target_id INTEGER;

-- Migrate existing role='replica' rows into the new table.
INSERT INTO replication_targets (id, name, driver, config_json, mode, enabled, created_at)
SELECT id, name, driver, config_json,
       COALESCE(replica_mode, 'async'),
       enabled,
       created_at
FROM storages
WHERE role = 'replica';

-- Re-point primaries from `replica_of_id` (old) to `replica_target_id` (new).
UPDATE storages
SET replica_target_id = replica_of_id
WHERE replica_of_id IS NOT NULL
  AND replica_of_id IN (SELECT id FROM replication_targets);

-- Drop the storages rows that were really replicas.
DELETE FROM storages WHERE role = 'replica';

-- +goose Down
-- One-way migration — revert by restoring from backup.
