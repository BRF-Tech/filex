-- +goose Up
-- Who put a thing in the TRASH.
--
-- The trash listed what was deleted, where from, and when, and nothing about
-- who: in a shared storage the one question a person asks of a missing file
-- ("who deleted this?") had no answer in filex at all.
--
--   deleted_by — the person whose delete it was. It is written on the row and
--                on every row trashed with it (a folder's contents are rows of
--                their own in the trash). NULL when nobody in filex did it:
--                the scanner found the object gone, the virus scan quarantined
--                it — or the row was trashed before this column existed.
--
-- Every soft delete clears it and internal/quotastore then names the acting
-- identity, the way it names the mover of a move (last_actor_id); a restore
-- clears it again. A name therefore never outlives the trip through the trash
-- it was written for.
--
-- ⚠ Backfill is deliberately NOT attempted, for 00038's reason: there is no
-- honest way to know who trashed a row before this column existed, and a
-- guessed name reads exactly like a recorded one.
ALTER TABLE nodes ADD COLUMN deleted_by INTEGER REFERENCES users(id) ON DELETE SET NULL;

-- Deleting a user sets this column to NULL wherever it names them; without an
-- index that is a scan of every row in `nodes`. Partial, like
-- idx_nodes_last_actor: live rows never carry a value.
CREATE INDEX IF NOT EXISTS idx_nodes_deleted_by ON nodes(deleted_by) WHERE deleted_by IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_nodes_deleted_by;
ALTER TABLE nodes DROP COLUMN deleted_by;
