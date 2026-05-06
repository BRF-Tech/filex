-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS ops_queue (
    id            VARCHAR(64) PRIMARY KEY,
    type          VARCHAR(64) NOT NULL,
    payload       JSON NOT NULL,
    status        VARCHAR(16) NOT NULL DEFAULT 'pending',
    priority      INT NOT NULL DEFAULT 0,
    attempts      INT NOT NULL DEFAULT 0,
    max_attempts  INT NOT NULL DEFAULT 3,
    last_error    TEXT,
    enqueued_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at    TIMESTAMP NULL DEFAULT NULL,
    finished_at   TIMESTAMP NULL DEFAULT NULL,
    not_before    TIMESTAMP NULL DEFAULT NULL,
    INDEX idx_ops_queue_status_pri_at (status, priority, enqueued_at),
    INDEX idx_ops_queue_type_status (type, status),
    INDEX idx_ops_queue_finished_at (finished_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ops_queue;
-- +goose StatementEnd
