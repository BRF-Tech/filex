-- +goose Up
-- App-plugin schedule: the hourly wake-up, and the work it scheduled.
--
-- An app that holds the `schedule` permission is woken once an hour and
-- asked what it wants done and WHEN; filex runs each answer at the minute it
-- named. Both halves live in THIS one table, because they are the same
-- thing — a row that comes due — and one mechanism is one set of bugs:
--
--   * `key = ''` is the WAKE-UP itself, one row per app. Its due_at is the
--     next hourly boundary; running it calls the app's `tick` export and
--     re-arms the row. The empty key is reserved for the host: an app's own
--     key is 1..64 characters, so the two can never collide.
--   * `key <> ''` is one piece of WORK the app asked for, keyed by its own
--     name for it. Naming the same key again moves the item instead of
--     adding a second one — which is why a restart cannot run it twice and
--     why an app can change its mind each hour.
--
-- Durability: the row is written when the wake-up answers, not when the work
-- runs, so the schedule survives a restart. A process that is down at 03:00
-- finds the row still `due` when it comes back and runs it then — once, late
-- rather than never.
--
-- Two processes on one database: `status` is the lease. Claiming is a single
-- conditional UPDATE (`SET status='running' … WHERE status='due'`) and only
-- the process whose UPDATE touched a row proceeds, so the same item is never
-- run twice. claimed_by names the node that took it.
--
-- Statuses: due → running → queued (the ops job exists; app_plugin_jobs and
-- pending_ops own it from there) | failed | skipped (the app was stopped,
-- uninstalled, or had the permission withdrawn at the moment it came due).
CREATE TABLE IF NOT EXISTS app_plugin_schedule (
    plugin_id   INTEGER NOT NULL,
    key         TEXT NOT NULL,
    due_at      DATETIME NOT NULL,
    action_id   TEXT NOT NULL DEFAULT '',
    storage_id  INTEGER NOT NULL DEFAULT 0,
    paths_json  TEXT NOT NULL DEFAULT '[]',
    params_json TEXT NOT NULL DEFAULT '{}',
    status      TEXT NOT NULL DEFAULT 'due',
    attempts    INTEGER NOT NULL DEFAULT 0,
    claimed_by  TEXT NOT NULL DEFAULT '',
    claimed_at  DATETIME,
    job_id      TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_id, key)
);
CREATE INDEX IF NOT EXISTS idx_app_plugin_schedule_due ON app_plugin_schedule(status, due_at);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_schedule;
