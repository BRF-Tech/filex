-- +goose Up
-- The copy/move/delete queue (internal/ops).
--
-- This table existed since the first release, but it was created by hand at
-- boot from internal/ops with SQLite DDL — INTEGER PRIMARY KEY AUTOINCREMENT —
-- run against whatever engine the operator had configured. On SQLite that
-- worked, which is why nobody noticed that on PostgreSQL it failed on every
-- boot and left the server reporting itself healthy while every file
-- operation died on "relation pending_ops does not exist" (issue #19).
--
-- CREATE TABLE IF NOT EXISTS, and dest_storage_id last, so an existing SQLite
-- install (where the column was appended by an ALTER at boot) and a fresh one
-- end up with the same table.
CREATE TABLE IF NOT EXISTS pending_ops (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    kind            TEXT NOT NULL,
    storage_id      INTEGER NOT NULL,
    sources_json    TEXT NOT NULL,
    dest            TEXT,
    total           INTEGER NOT NULL DEFAULT 0,
    done            INTEGER NOT NULL DEFAULT 0,
    failed          INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'pending',
    error           TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at      DATETIME,
    finished_at     DATETIME,
    dest_storage_id INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_pending_ops_status ON pending_ops(status, created_at);

-- +goose Down
DROP TABLE IF EXISTS pending_ops;
