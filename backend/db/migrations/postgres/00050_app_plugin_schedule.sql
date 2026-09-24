-- +goose Up
-- App-plugin schedule — see sqlite/00050_app_plugin_schedule.sql for what the
-- table is and why the wake-up and the work it scheduled share it. Column
-- names must stay identical across the three dialects (schema_parity_test).
CREATE TABLE IF NOT EXISTS app_plugin_schedule (
    plugin_id   BIGINT NOT NULL,
    key         TEXT NOT NULL,
    due_at      TIMESTAMPTZ NOT NULL,
    action_id   TEXT NOT NULL DEFAULT '',
    storage_id  BIGINT NOT NULL DEFAULT 0,
    paths_json  TEXT NOT NULL DEFAULT '[]',
    params_json TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'due',
    attempts    INTEGER NOT NULL DEFAULT 0,
    claimed_by  TEXT NOT NULL DEFAULT '',
    claimed_at  TIMESTAMPTZ,
    job_id      TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id, key)
);
CREATE INDEX IF NOT EXISTS idx_app_plugin_schedule_due ON app_plugin_schedule(status, due_at);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_schedule;
