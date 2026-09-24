-- +goose Up
-- +goose StatementBegin

-- Per-reader read state for broadcasts. See sqlite/00043_notification_reads.sql.
CREATE TABLE IF NOT EXISTS notification_read_through (
    user_id    BIGINT NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    through_id BIGINT NOT NULL,
    read_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS notification_reads (
    notification_id BIGINT NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (notification_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_notification_reads_user ON notification_reads (user_id);
CREATE INDEX IF NOT EXISTS idx_notifications_unread_since ON notifications (user_id, read_at, id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notifications_unread_since;
DROP INDEX IF EXISTS idx_notification_reads_user;
DROP TABLE IF EXISTS notification_reads CASCADE;
DROP TABLE IF EXISTS notification_read_through CASCADE;
-- +goose StatementEnd
