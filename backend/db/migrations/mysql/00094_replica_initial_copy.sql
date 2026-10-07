-- +goose Up
-- REPLICATION THAT RUNS (#186). The model is written out in
-- db/migrations/sqlite/00094_replica_initial_copy.sql: replica_failures learns
-- the storage a failure belongs to (unique key (storage_id, path, op), rows
-- from before carry 0), and replica_initial_copies holds each linked storage's
-- initial copy.
--
-- ⚠ One statement per block: the MySQL driver refuses a multi-statement
-- query. The column and the key change are ONE ALTER, because MySQL DDL is
-- not transactional and two statements could leave the table half-changed.
-- path keeps its 255-character key prefix (00008) and its binary collation
-- (00041).

-- +goose StatementBegin
ALTER TABLE replica_failures
    ADD COLUMN storage_id BIGINT NOT NULL DEFAULT 0,
    DROP INDEX uniq_replica_failures_path_op,
    ADD UNIQUE KEY uniq_replica_failures_storage_path_op (storage_id, path(255), op);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS replica_initial_copies (
    storage_id      BIGINT NOT NULL PRIMARY KEY,
    target_id       BIGINT NOT NULL DEFAULT 0,
    phase           VARCHAR(16) NOT NULL DEFAULT 'pending',
    walk_cursor     TEXT NOT NULL DEFAULT (''),
    counted         INT NOT NULL DEFAULT 0,
    total_files     BIGINT NOT NULL DEFAULT 0,
    copied_files    BIGINT NOT NULL DEFAULT 0,
    present_files   BIGINT NOT NULL DEFAULT 0,
    excluded_files  BIGINT NOT NULL DEFAULT 0,
    failed_files    BIGINT NOT NULL DEFAULT 0,
    copied_bytes    BIGINT NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT (''),
    started_unix    BIGINT NOT NULL DEFAULT 0,
    updated_unix    BIGINT NOT NULL DEFAULT 0,
    finished_unix   BIGINT NOT NULL DEFAULT 0,
    lease_owner     VARCHAR(64) NOT NULL DEFAULT '',
    lease_until     BIGINT NOT NULL DEFAULT 0,
    revision        BIGINT NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS replica_initial_copies;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE replica_failures
    DROP INDEX uniq_replica_failures_storage_path_op,
    DROP COLUMN storage_id,
    ADD UNIQUE KEY uniq_replica_failures_path_op (path(255), op);
-- +goose StatementEnd
