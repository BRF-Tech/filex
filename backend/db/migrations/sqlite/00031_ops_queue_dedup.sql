-- +goose Up
-- +goose StatementBegin

-- Op coalescing key (queue.Op.DedupKey). While a PENDING op holds a key,
-- Enqueue of another op with the same key is refused with queue.ErrDuplicate.
-- First user: the antivirus save-scan window, where a burst of Ctrl+S in the
-- browser editor must collapse to exactly one scheduled scan per file.
ALTER TABLE ops_queue ADD COLUMN dedup_key TEXT;

-- Partial, so the key is released as soon as the op leaves `pending`: a save
-- arriving after the scan has been picked up schedules a new scan instead of
-- being silently dropped. A plain UNIQUE index would block a node's second
-- scan forever, because the finished row keeps the key.
CREATE UNIQUE INDEX IF NOT EXISTS idx_ops_queue_dedup_pending
    ON ops_queue (dedup_key)
    WHERE dedup_key IS NOT NULL AND status = 'pending';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_ops_queue_dedup_pending;
ALTER TABLE ops_queue DROP COLUMN dedup_key;
-- +goose StatementEnd
