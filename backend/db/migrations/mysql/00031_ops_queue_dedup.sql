-- +goose Up
-- +goose StatementBegin

-- ⚠ Column only. MySQL has no partial indexes, so the "unique among PENDING
-- rows" constraint its sqlite/postgres twins carry cannot be expressed here,
-- and a plain UNIQUE index would be wrong (it would block a node's second
-- scan forever, since the finished row keeps the key).
--
-- Nothing is lost today: filex ships sqlite, postgres and redis QUEUE drivers
-- and no MySQL one, so no MySQL deployment enforces this constraint because
-- none reaches this table through a queue driver. The guarded INSERT in the
-- SQL drivers still coalesces correctly on its own under MySQL's
-- single-statement atomicity; the index is a race backstop, not the mechanism.
ALTER TABLE ops_queue ADD COLUMN dedup_key VARCHAR(191) NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE ops_queue DROP COLUMN dedup_key;
-- +goose StatementEnd
