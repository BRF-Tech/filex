-- +goose Up
-- See sqlite/00038_node_actor_and_origin.sql for the model. owner_id (00004)
-- says who put the thing here; these say who touched it last and whether it
-- arrived through an anonymous drop link. NULL means system in both cases —
-- nobody is invented for it, and nothing is backfilled.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS last_actor_id BIGINT REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS external_upload BOOLEAN NOT NULL DEFAULT FALSE;

-- Who asked for the queued copy/move/delete -- see sqlite/00038. No foreign
-- key: a short-lived queue row must not be able to block deleting a user.
ALTER TABLE pending_ops ADD COLUMN IF NOT EXISTS actor_id BIGINT;

CREATE INDEX IF NOT EXISTS idx_nodes_last_actor ON nodes(last_actor_id) WHERE last_actor_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_nodes_last_actor;
ALTER TABLE pending_ops DROP COLUMN IF EXISTS actor_id;
ALTER TABLE nodes DROP COLUMN IF EXISTS external_upload;
ALTER TABLE nodes DROP COLUMN IF EXISTS last_actor_id;
