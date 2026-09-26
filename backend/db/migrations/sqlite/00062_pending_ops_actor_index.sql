-- +goose Up
-- A PERSON'S OWN OPERATIONS ARE FOUND THROUGH AN INDEX.
--
-- Everybody but an administrator is shown the operations they queued
-- (handlers.opsViewer, ops.Viewer.Own): the explorer's operations centre asks
-- `WHERE actor_id = ? ORDER BY id DESC LIMIT 200` when it opens and every two
-- seconds while something runs. pending_ops keeps every finished row, and
-- nothing indexed actor_id, so every one of those polls read the whole table.
--
-- 00062: the highest number in every filex worktree on disk was 00061.
CREATE INDEX IF NOT EXISTS idx_pending_ops_actor ON pending_ops(actor_id, id);

-- +goose Down
DROP INDEX IF EXISTS idx_pending_ops_actor;
