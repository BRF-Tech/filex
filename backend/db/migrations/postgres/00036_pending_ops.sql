-- +goose Up
-- See sqlite/00036_pending_ops.sql. On PostgreSQL this table never existed:
-- the SQLite DDL internal/ops ran at boot was a syntax error here, so every
-- copy, move and delete failed on an install that otherwise looked healthy
-- (issue #19).
CREATE TABLE IF NOT EXISTS pending_ops (
    id              BIGSERIAL PRIMARY KEY,
    kind            TEXT NOT NULL,
    storage_id      BIGINT NOT NULL,
    sources_json    TEXT NOT NULL,
    dest            TEXT,
    total           BIGINT NOT NULL DEFAULT 0,
    done            BIGINT NOT NULL DEFAULT 0,
    failed          BIGINT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending',
    error           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ,
    dest_storage_id BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_pending_ops_status ON pending_ops(status, created_at);

-- +goose Down
DROP TABLE IF EXISTS pending_ops;
