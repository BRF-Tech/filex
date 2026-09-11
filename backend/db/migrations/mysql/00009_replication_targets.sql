-- +goose Up
-- See sqlite/00009_replication_targets.sql for the rationale.
--
-- ⚠ This file was missing from the MySQL set entirely until issue #19: the
-- other two dialects have had it since v0.1.16, so a MySQL install came up
-- with no replication_targets table and no storages.replica_target_id, and
-- the very first CreateStorage failed with "Unknown column". Every migration
-- must exist in all three directories; backend/internal/db's schema-parity
-- test is what now says so out loud.
--
-- ⚠ Do not wrap these statements in a goose statement block: a wrapped block
-- reaches the server as one multi-statement query, which the MySQL driver
-- refuses unless the DSN opts into multiStatements.

CREATE TABLE IF NOT EXISTS replication_targets (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name        VARCHAR(190) NOT NULL UNIQUE,
    driver      VARCHAR(64) NOT NULL,
    config_json JSON NOT NULL DEFAULT ('{}'),
    mode        VARCHAR(16) NOT NULL DEFAULT 'async',
    enabled     TINYINT(1) NOT NULL DEFAULT 1,
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE storages ADD COLUMN replica_target_id BIGINT NULL;

-- Carry over the rows that modelled a target as a storage (role='replica').
INSERT IGNORE INTO replication_targets (id, name, driver, config_json, mode, enabled, created_at)
SELECT id, name, driver, config_json,
       COALESCE(replica_mode, 'async'),
       enabled, created_at
FROM storages
WHERE role = 'replica';

UPDATE storages
SET replica_target_id = replica_of_id
WHERE replica_of_id IS NOT NULL
  AND replica_of_id IN (SELECT id FROM replication_targets);

DELETE FROM storages WHERE role = 'replica';

-- +goose Down
-- One-way migration — revert by restoring from backup.
