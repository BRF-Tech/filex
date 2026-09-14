-- +goose Up
-- See sqlite/00038_node_actor_and_origin.sql for the model. owner_id (00004)
-- says who put the thing here; these say who touched it last and whether it
-- arrived through an anonymous drop link. NULL means system in both cases —
-- nobody is invented for it, and nothing is backfilled.
--
-- ⚠ Do not wrap these statements in a goose statement block: a wrapped block
-- reaches the server as one multi-statement query, which the MySQL driver
-- refuses unless the DSN opts into multiStatements. That wrapper is what made
-- five migrations fail on the FIRST boot of every MySQL install (issue #19).
--
-- ⚠ Both columns, the index and the foreign key in ONE statement. MySQL has
-- neither ADD COLUMN IF NOT EXISTS nor CREATE INDEX IF NOT EXISTS, and its DDL
-- is not transactional, so splitting this up can leave a half-applied
-- migration that fails differently on the retry.
--
-- ⚠ The index is declared BEFORE the foreign key on purpose: MySQL creates an
-- index of its own for a referencing column that has none, so declaring ours
-- first means the constraint adopts it instead of leaving two.
--
-- ⚠ No partial index. MySQL has no `WHERE` on an index, so unlike SQLite and
-- PostgreSQL this one also carries the NULL (system) rows. That is a size
-- difference, not a behaviour one.
ALTER TABLE nodes ADD COLUMN last_actor_id BIGINT NULL, ADD COLUMN external_upload TINYINT(1) NOT NULL DEFAULT 0, ADD INDEX idx_nodes_last_actor (last_actor_id), ADD CONSTRAINT fk_nodes_last_actor FOREIGN KEY (last_actor_id) REFERENCES users(id) ON DELETE SET NULL;

-- Who asked for the queued copy/move/delete -- see sqlite/00038. No foreign
-- key: a short-lived queue row must not be able to block deleting a user.
ALTER TABLE pending_ops ADD COLUMN actor_id BIGINT NULL;

-- +goose Down
ALTER TABLE nodes DROP FOREIGN KEY fk_nodes_last_actor;
ALTER TABLE nodes DROP COLUMN external_upload, DROP COLUMN last_actor_id;
ALTER TABLE pending_ops DROP COLUMN actor_id;
