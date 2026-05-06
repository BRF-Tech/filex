-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS notifications (
    id              BIGINT AUTO_INCREMENT PRIMARY KEY,
    event           VARCHAR(64) NOT NULL,
    severity        VARCHAR(16) NOT NULL,
    title           VARCHAR(255) NOT NULL,
    body            TEXT NOT NULL,
    meta_json       JSON NOT NULL,
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
    muted_events    JSON NOT NULL,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    CONSTRAINT fk_notification_settings_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notification_settings;
DROP TABLE IF EXISTS notifications;
-- +goose StatementEnd
