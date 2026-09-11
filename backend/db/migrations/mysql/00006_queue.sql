-- +goose Up
-- +goose StatementBegin

-- ⚠ TIMESTAMP(6), not TIMESTAMP. The dequeue order is
-- `ORDER BY priority DESC, enqueued_at ASC`, and a whole-second column makes
-- every op enqueued in the same second a tie the server may break any way it
-- likes — forty equal-priority ops came back shuffled, so "oldest first"
-- silently stopped holding. Postgres has had microseconds all along.
CREATE TABLE IF NOT EXISTS ops_queue (
    id            VARCHAR(64) PRIMARY KEY,
    type          VARCHAR(64) NOT NULL,
    payload       JSON NOT NULL DEFAULT ('{}'),
    status        VARCHAR(16) NOT NULL DEFAULT 'pending',
    priority      INT NOT NULL DEFAULT 0,
    attempts      INT NOT NULL DEFAULT 0,
    max_attempts  INT NOT NULL DEFAULT 3,
    last_error    TEXT,
    enqueued_at   TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    started_at    TIMESTAMP(6) NULL DEFAULT NULL,
    finished_at   TIMESTAMP(6) NULL DEFAULT NULL,
    not_before    TIMESTAMP(6) NULL DEFAULT NULL,
    INDEX idx_ops_queue_status_pri_at (status, priority, enqueued_at),
    INDEX idx_ops_queue_type_status (type, status),
    INDEX idx_ops_queue_finished_at (finished_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS ops_queue;
-- +goose StatementEnd
