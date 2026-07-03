-- +goose Up
-- +goose StatementBegin

-- Path-based rules. priority asc → first match wins. Empty rule set
-- defaults to "mirror" (Burak E2 / SPEC §4.4).
CREATE TABLE IF NOT EXISTS replica_rules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    path_pattern    TEXT NOT NULL,
    mode            TEXT NOT NULL,            -- mirror | append_only | skip
    priority        INTEGER NOT NULL DEFAULT 100,
    enabled         INTEGER NOT NULL DEFAULT 1,
    description     TEXT NOT NULL DEFAULT '',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_replica_rules_priority
    ON replica_rules (priority, enabled);

-- One row per (path, op). attempts++ on retry; resolved_at flips to
-- non-NULL when reconcile/retry succeeds (or the path is deleted).
CREATE TABLE IF NOT EXISTS replica_failures (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    path            TEXT NOT NULL,
    op              TEXT NOT NULL,            -- write | delete | move | copy
    error_code      TEXT NOT NULL DEFAULT '',
    error_msg       TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 1,
    last_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at     DATETIME,
    UNIQUE(path, op)
);
CREATE INDEX IF NOT EXISTS idx_replica_failures_unresolved
    ON replica_failures (resolved_at, last_attempt_at DESC);

-- Singleton: only id=1 may ever exist; the cron job UPSERTs.
CREATE TABLE IF NOT EXISTS replica_status_reports (
    id              INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    generated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    total_files     INTEGER NOT NULL DEFAULT 0,
    failed_count    INTEGER NOT NULL DEFAULT 0,
    repaired_count  INTEGER NOT NULL DEFAULT 0,
    summary_json    TEXT NOT NULL DEFAULT '{}'
);

-- Singleton too — global replica policy.
CREATE TABLE IF NOT EXISTS replica_settings (
    id              INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    report_cron     TEXT NOT NULL DEFAULT '',
    report_enabled  INTEGER NOT NULL DEFAULT 0,
    default_mode    TEXT NOT NULL DEFAULT 'mirror',
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Storage role + replica relationship. SQLite doesn't enforce the FK
-- on ALTER TABLE ADD COLUMN, but the column is informational anyway —
-- the application checks role before treating a row as a replica.
ALTER TABLE storages ADD COLUMN role           TEXT    NOT NULL DEFAULT 'primary';
ALTER TABLE storages ADD COLUMN replica_of_id  INTEGER;
ALTER TABLE storages ADD COLUMN replica_mode   TEXT    NOT NULL DEFAULT 'async';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- SQLite ALTER TABLE DROP COLUMN landed in 3.35; we guard the rollback
-- by simply leaving the columns in place (idempotent for re-up).
DROP INDEX IF EXISTS idx_replica_failures_unresolved;
DROP INDEX IF EXISTS idx_replica_rules_priority;
DROP TABLE IF EXISTS replica_settings;
DROP TABLE IF EXISTS replica_status_reports;
DROP TABLE IF EXISTS replica_failures;
DROP TABLE IF EXISTS replica_rules;
-- +goose StatementEnd
