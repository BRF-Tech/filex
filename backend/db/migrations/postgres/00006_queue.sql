-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS ops_queue (
    id            TEXT PRIMARY KEY,
    type          TEXT NOT NULL,
    payload       JSONB NOT NULL DEFAULT '{}'::jsonb,
    status        TEXT NOT NULL DEFAULT 'pending',
    priority      INTEGER NOT NULL DEFAULT 0,
    attempts      INTEGER NOT NULL DEFAULT 0,
    max_attempts  INTEGER NOT NULL DEFAULT 3,
    last_error    TEXT,
    enqueued_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    not_before    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ops_queue_status_pri_at
    ON ops_queue (status, priority DESC, enqueued_at);

CREATE INDEX IF NOT EXISTS idx_ops_queue_type_status
    ON ops_queue (type, status);

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
