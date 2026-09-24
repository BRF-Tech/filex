-- +goose Up
-- App-plugin schedule — see sqlite/00050_app_plugin_schedule.sql for what the
-- table is and why the wake-up and the work it scheduled share it.
--
-- ⚠ Not wrapped in a goose statement block (a wrapped block reaches the server
-- as one multi-statement query, which the driver refuses — see 00036).
-- ⚠ The index is declared inside CREATE TABLE: MySQL has no CREATE INDEX IF
-- NOT EXISTS. `key` is a reserved word on MySQL and is quoted.
CREATE TABLE IF NOT EXISTS app_plugin_schedule (
    plugin_id   BIGINT NOT NULL,
    `key`       VARCHAR(64) NOT NULL,
    due_at      DATETIME NOT NULL,
    action_id   VARCHAR(64) NOT NULL DEFAULT '',
    storage_id  BIGINT NOT NULL DEFAULT 0,
    paths_json  TEXT NOT NULL DEFAULT ('[]'),
    params_json TEXT NOT NULL DEFAULT ('{}'),
    status      VARCHAR(32) NOT NULL DEFAULT 'due',
    attempts    INT NOT NULL DEFAULT 0,
    claimed_by  VARCHAR(64) NOT NULL DEFAULT '',
    claimed_at  DATETIME,
    job_id      VARCHAR(64) NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT (''),
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_id, `key`),
    INDEX idx_app_plugin_schedule_due (status, due_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS app_plugin_schedule;
