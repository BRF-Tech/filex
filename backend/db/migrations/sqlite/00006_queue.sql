-- +goose Up
-- +goose StatementBegin

-- Persistent driver-based op queue. Replaces the hand-rolled pending_ops
-- stand-in with a generic schema that copy/move/delete, replica retry,
-- reconcile, thumb generation and report cron all share. See
-- internal/queue/driver.go for the contract.
CREATE TABLE IF NOT EXISTS ops_queue (
    id            TEXT PRIMARY KEY,
    type          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    status        TEXT NOT NULL DEFAULT 'pending',
    priority      INTEGER NOT NULL DEFAULT 0,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT,
    enqueued_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at    DATETIME,
    finished_at   DATETIME,
    not_before    DATETIME
);

-- Hot path: pick the next pending op for a worker. The DESC priority +
-- ASC enqueued_at composite makes Dequeue() index-only.
CREATE INDEX IF NOT EXISTS idx_ops_queue_status_pri_at
    ON ops_queue (status, priority DESC, enqueued_at);

-- For type-filtered Dequeue (workers register a subset of types).
CREATE INDEX IF NOT EXISTS idx_ops_queue_type_status
    ON ops_queue (type, status);

-- For "Done in last 24h" stat.
CREATE INDEX IF NOT EXISTS idx_ops_queue_finished_at
    ON ops_queue (finished_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_ops_queue_finished_at;
DROP INDEX IF EXISTS idx_ops_queue_type_status;
DROP INDEX IF EXISTS idx_ops_queue_status_pri_at;
DROP TABLE IF EXISTS ops_queue;
-- +goose StatementEnd
