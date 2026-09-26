-- +goose Up
-- A person's own operations are found through an index. See
-- sqlite/00062_pending_ops_actor_index.sql.
--
-- ⚠ Plain CREATE INDEX: MySQL has no CREATE INDEX IF NOT EXISTS (see
-- mysql/00036_pending_ops.sql), and goose runs this file once.
CREATE INDEX idx_pending_ops_actor ON pending_ops (actor_id, id);

-- +goose Down
DROP INDEX idx_pending_ops_actor ON pending_ops;
