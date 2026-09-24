-- +goose Up
-- Per-reader read state for broadcasts. See sqlite/00043_notification_reads.sql.
-- BIGINT on every key: it must match notifications.id and users.id exactly for
-- InnoDB to accept the foreign keys.

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS notification_read_through (
    user_id    BIGINT NOT NULL,
    through_id BIGINT NOT NULL,
    read_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id),
    CONSTRAINT fk_notification_read_through_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS notification_reads (
    notification_id BIGINT NOT NULL,
    user_id         BIGINT NOT NULL,
    read_at         TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (notification_id, user_id),
    INDEX idx_notification_reads_user (user_id),
    CONSTRAINT fk_notification_reads_notification FOREIGN KEY (notification_id) REFERENCES notifications(id) ON DELETE CASCADE,
    CONSTRAINT fk_notification_reads_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX idx_notifications_unread_since ON notifications (user_id, read_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_notifications_unread_since ON notifications;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_reads;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_read_through;
-- +goose StatementEnd
