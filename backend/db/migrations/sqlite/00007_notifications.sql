-- +goose Up
-- +goose StatementBegin

-- In-app notification audit + bell history. Each row is either
-- broadcast (user_id IS NULL → admin-visible) or scoped to a user.
-- Webhook delivery status is recorded so the admin UI can spot a
-- stuck endpoint.
CREATE TABLE IF NOT EXISTS notifications (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    event           TEXT NOT NULL,
    severity        TEXT NOT NULL,
    title           TEXT NOT NULL,
    body            TEXT NOT NULL,
    meta_json       TEXT NOT NULL DEFAULT '{}',
    user_id         INTEGER REFERENCES users(id) ON DELETE CASCADE,
    read_at         DATETIME,
    webhook_status  TEXT NOT NULL DEFAULT 'pending',
    webhook_error   TEXT,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_read_created
    ON notifications (user_id, read_at, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_notifications_event_created
    ON notifications (event, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_notifications_webhook_status
    ON notifications (webhook_status, created_at DESC);

-- Per-user opt-in matrix. A missing row is treated as in_app_enabled=1.
CREATE TABLE IF NOT EXISTS notification_settings (
    user_id         INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    in_app_enabled  INTEGER NOT NULL DEFAULT 1,
    muted_events    TEXT NOT NULL DEFAULT '[]',
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notifications_webhook_status;
DROP INDEX IF EXISTS idx_notifications_event_created;
DROP INDEX IF EXISTS idx_notifications_user_read_created;
DROP TABLE IF EXISTS notification_settings;
DROP TABLE IF EXISTS notifications;
-- +goose StatementEnd
