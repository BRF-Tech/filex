-- +goose Up
-- The notification digest. See sqlite/00087_notify_digest.sql.
-- BIGINT on user_id: it must match users.id exactly for InnoDB to accept the
-- foreign key.

-- +goose StatementBegin
ALTER TABLE notification_settings ADD COLUMN urgent_overrides TEXT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS notify_digest_state (
    user_id     BIGINT NOT NULL,
    through_id  BIGINT NOT NULL DEFAULT 0,
    due_at      TIMESTAMP NULL DEFAULT NULL,
    updated_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id),
    INDEX idx_notify_digest_state_due (due_at),
    CONSTRAINT fk_notify_digest_state_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS notify_digest_policy (
    scope_id        BIGINT NOT NULL,
    window_minutes  INT NOT NULL DEFAULT 1,
    urgent_events   TEXT NULL,
    updated_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (scope_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS notify_digest_policy;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS notify_digest_state;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE notification_settings DROP COLUMN urgent_overrides;
-- +goose StatementEnd
