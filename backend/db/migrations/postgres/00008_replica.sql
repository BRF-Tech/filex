-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS replica_rules (
    id              BIGSERIAL PRIMARY KEY,
    path_pattern    TEXT NOT NULL,
    mode            TEXT NOT NULL,
    priority        INTEGER NOT NULL DEFAULT 100,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    description     TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_replica_rules_priority
    ON replica_rules (priority, enabled);

CREATE TABLE IF NOT EXISTS replica_failures (
    id              BIGSERIAL PRIMARY KEY,
    path            TEXT NOT NULL,
    op              TEXT NOT NULL,
    error_code      TEXT NOT NULL DEFAULT '',
    error_msg       TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 1,
    last_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,
    CONSTRAINT replica_failures_path_op_uniq UNIQUE (path, op)
);
CREATE INDEX IF NOT EXISTS idx_replica_failures_unresolved
    ON replica_failures (resolved_at, last_attempt_at DESC);

CREATE TABLE IF NOT EXISTS replica_status_reports (
    id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    generated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    total_files     BIGINT NOT NULL DEFAULT 0,
    failed_count    BIGINT NOT NULL DEFAULT 0,
    repaired_count  BIGINT NOT NULL DEFAULT 0,
    summary_json    JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS replica_settings (
    id              SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    report_cron     TEXT NOT NULL DEFAULT '',
    report_enabled  BOOLEAN NOT NULL DEFAULT FALSE,
    default_mode    TEXT NOT NULL DEFAULT 'mirror',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE storages ADD COLUMN IF NOT EXISTS role           TEXT    NOT NULL DEFAULT 'primary';
ALTER TABLE storages ADD COLUMN IF NOT EXISTS replica_of_id  BIGINT;
ALTER TABLE storages ADD COLUMN IF NOT EXISTS replica_mode   TEXT    NOT NULL DEFAULT 'async';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_replica_failures_unresolved;
DROP INDEX IF EXISTS idx_replica_rules_priority;
DROP TABLE IF EXISTS replica_settings;
DROP TABLE IF EXISTS replica_status_reports;
DROP TABLE IF EXISTS replica_failures;
DROP TABLE IF EXISTS replica_rules;
ALTER TABLE storages DROP COLUMN IF EXISTS replica_mode;
ALTER TABLE storages DROP COLUMN IF EXISTS replica_of_id;
ALTER TABLE storages DROP COLUMN IF EXISTS role;
-- +goose StatementEnd
