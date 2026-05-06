-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS notifications (
    id              BIGSERIAL PRIMARY KEY,
    event           TEXT NOT NULL,
    severity        TEXT NOT NULL,
    title           TEXT NOT NULL,
    body            TEXT NOT NULL,
    meta_json       JSONB NOT NULL DEFAULT '{}'::jsonb,
    user_id         BIGINT REFERENCES users(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ,
    webhook_status  TEXT NOT NULL DEFAULT 'pending',
    webhook_error   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_notifications_user_read_created
    ON notifications (user_id, read_at, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_notifications_event_created
    ON notifications (event, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_notifications_webhook_status
    ON notifications (webhook_status, created_at DESC);

CREATE TABLE IF NOT EXISTS notification_settings (
    user_id         BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    in_app_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
    muted_events    JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
