-- +goose Up
-- ⚠ Do not wrap these statements in a goose statement block: goose hands a
-- wrapped block to the server as ONE query, and the MySQL driver refuses a
-- query that carries several statements unless the DSN opts into
-- multiStatements. A wrapper here is what made this migration fail on the
-- FIRST boot of every MySQL install (issue #19). Leave the statements
-- unwrapped, or wrap them one at a time.

CREATE TABLE IF NOT EXISTS notifications (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    event           VARCHAR(64) NOT NULL,
    severity        VARCHAR(16) NOT NULL,
    title           VARCHAR(255) NOT NULL,
    body            TEXT NOT NULL,
    meta_json       JSON NOT NULL DEFAULT ('{}'),
    user_id         BIGINT NULL,
    read_at         TIMESTAMP NULL DEFAULT NULL,
    webhook_status  VARCHAR(16) NOT NULL DEFAULT 'pending',
    webhook_error   TEXT,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_notifications_user_read_created (user_id, read_at, created_at),
    INDEX idx_notifications_event_created     (event, created_at),
    INDEX idx_notifications_webhook_status    (webhook_status, created_at),
    CONSTRAINT fk_notifications_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS notification_settings (
    user_id         BIGINT PRIMARY KEY,
    in_app_enabled  TINYINT(1) NOT NULL DEFAULT 1,
    muted_events    JSON NOT NULL DEFAULT ('[]'),
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_notification_settings_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;


-- +goose Down
DROP TABLE IF EXISTS notification_settings;
DROP TABLE IF EXISTS notifications;
