-- +goose Up
-- +goose StatementBegin

-- REPLICATION THAT RUNS (#186). The model is written out in
-- db/migrations/sqlite/00094_replica_initial_copy.sql: replica_failures learns
-- the storage a failure belongs to (unique key (storage_id, path, op), rows
-- from before carry 0), and replica_initial_copies holds each linked storage's
-- initial copy - one row per storage, its place in the walk, its counters and
-- the lease of the worker running a slice of it. Times are Unix seconds in
-- BIGINT columns.
ALTER TABLE replica_failures ADD COLUMN IF NOT EXISTS storage_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE replica_failures DROP CONSTRAINT IF EXISTS replica_failures_path_op_uniq;
ALTER TABLE replica_failures ADD CONSTRAINT replica_failures_storage_path_op_uniq UNIQUE (storage_id, path, op);

CREATE TABLE IF NOT EXISTS replica_initial_copies (
    storage_id      BIGINT PRIMARY KEY,
    target_id       BIGINT NOT NULL DEFAULT 0,
    phase           TEXT NOT NULL DEFAULT 'pending',
    walk_cursor     TEXT NOT NULL DEFAULT '',
    counted         INTEGER NOT NULL DEFAULT 0,
    total_files     BIGINT NOT NULL DEFAULT 0,
    copied_files    BIGINT NOT NULL DEFAULT 0,
    present_files   BIGINT NOT NULL DEFAULT 0,
    excluded_files  BIGINT NOT NULL DEFAULT 0,
    failed_files    BIGINT NOT NULL DEFAULT 0,
    copied_bytes    BIGINT NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    started_unix    BIGINT NOT NULL DEFAULT 0,
    updated_unix    BIGINT NOT NULL DEFAULT 0,
    finished_unix   BIGINT NOT NULL DEFAULT 0,
    lease_owner     TEXT NOT NULL DEFAULT '',
    lease_until     BIGINT NOT NULL DEFAULT 0,
    revision        BIGINT NOT NULL DEFAULT 0
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS replica_initial_copies;
DELETE FROM replica_failures a USING replica_failures b
 WHERE a.path = b.path AND a.op = b.op AND a.id > b.id;
ALTER TABLE replica_failures DROP CONSTRAINT IF EXISTS replica_failures_storage_path_op_uniq;
ALTER TABLE replica_failures DROP COLUMN IF EXISTS storage_id;
ALTER TABLE replica_failures ADD CONSTRAINT replica_failures_path_op_uniq UNIQUE (path, op);
-- +goose StatementEnd
