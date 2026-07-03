-- +goose Up
-- See sqlite/00009_replication_targets.sql for the rationale.

CREATE TABLE IF NOT EXISTS replication_targets (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    driver      TEXT    NOT NULL,
    config_json JSONB   NOT NULL DEFAULT '{}',
    mode        TEXT    NOT NULL DEFAULT 'async',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE storages ADD COLUMN IF NOT EXISTS replica_target_id BIGINT;

INSERT INTO replication_targets (id, name, driver, config_json, mode, enabled, created_at)
SELECT id, name, driver, config_json::jsonb,
       COALESCE(replica_mode, 'async'),
       enabled, created_at
FROM storages
WHERE role = 'replica'
ON CONFLICT (id) DO NOTHING;

UPDATE storages
SET replica_target_id = replica_of_id
WHERE replica_of_id IS NOT NULL
  AND replica_of_id IN (SELECT id FROM replication_targets);

DELETE FROM storages WHERE role = 'replica';

-- Bump the bigserial sequence past the highest carried-over id.
SELECT setval(pg_get_serial_sequence('replication_targets', 'id'),
              GREATEST(COALESCE(MAX(id), 1), 1), true)
FROM replication_targets;

-- +goose Down
