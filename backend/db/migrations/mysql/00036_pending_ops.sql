-- +goose Up
-- See sqlite/00036_pending_ops.sql. On MySQL this table never existed either
-- (issue #19).
--
-- ⚠ Do not wrap these statements in a goose statement block: a wrapped block
-- reaches the server as one multi-statement query, which the MySQL driver
-- refuses unless the DSN opts into multiStatements.
--
-- ⚠ The index is declared inside CREATE TABLE: MySQL has no
-- CREATE INDEX IF NOT EXISTS, so a separate statement would fail on the second
-- run of a migration that had partly applied.
CREATE TABLE IF NOT EXISTS pending_ops (
    id              BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    kind            VARCHAR(32) NOT NULL,
    storage_id      BIGINT NOT NULL,
    sources_json    TEXT NOT NULL DEFAULT (''),
    dest            TEXT,
    total           BIGINT NOT NULL DEFAULT 0,
    done            BIGINT NOT NULL DEFAULT 0,
    failed          BIGINT NOT NULL DEFAULT 0,
    status          VARCHAR(32) NOT NULL DEFAULT 'pending',
    error           TEXT,
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    started_at      DATETIME(6),
    finished_at     DATETIME(6),
    dest_storage_id BIGINT NOT NULL DEFAULT 0,
    INDEX idx_pending_ops_status (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS pending_ops;
