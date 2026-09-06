-- +goose Up
-- +goose StatementBegin

ALTER TABLE ops_queue ADD COLUMN IF NOT EXISTS dedup_key TEXT;

-- Partial unique index: the key is held only while the op is pending, so a
-- request arriving after the op was dequeued schedules a new one. See the
-- sqlite twin for the full reasoning.
CREATE UNIQUE INDEX IF NOT EXISTS idx_ops_queue_dedup_pending
    ON ops_queue (dedup_key)
    WHERE dedup_key IS NOT NULL AND status = 'pending';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_ops_queue_dedup_pending;
ALTER TABLE ops_queue DROP COLUMN IF EXISTS dedup_key;
-- +goose StatementEnd
