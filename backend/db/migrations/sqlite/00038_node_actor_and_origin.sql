-- +goose Up
-- Who put a thing here, who touched it last, and whether it came from outside.
--
-- `nodes.owner_id` already exists (00004) but was only ever read by the quota
-- accounting, and the listing never carried it — so the UI could show a file's
-- size and date and nothing at all about whose file it is. These two columns
-- complete the model that column started:
--
--   owner_id       — who put the thing here. NULL means SYSTEM: nobody put it
--                    here through filex (the scanner found it, it was written
--                    straight into the bucket, or the row predates 00004).
--                    "System" is the honest word for ownerless; it is not a
--                    user and no user row is invented for it.
--   last_actor_id  — who touched it last. NULL means system for the same
--                    reason: a change that arrived from outside filex (the
--                    S3 side changed, a sync found new bytes) has no actor.
--   external_upload — the thing arrived through an anonymous drop link / file
--                    request. The OWNER is the person who created the link
--                    (they asked for the file, it is theirs and it is billed
--                    to their quota), but the row has to be able to say the
--                    bytes were handed over by somebody else. The uploader is
--                    anonymous by design and gets no identity here.
--
-- ⚠ Backfill is deliberately NOT attempted. There is no honest way to guess
-- who last touched a row written before this migration, and a guessed actor
-- reads exactly like a measured one. Existing rows stay NULL = system.
ALTER TABLE nodes ADD COLUMN last_actor_id INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE nodes ADD COLUMN external_upload INTEGER NOT NULL DEFAULT 0;

-- Who asked for the queued copy/move/delete. The ops worker runs on a
-- server-lifetime context long after the request is gone, so without this
-- column a pasted file could only be attributed by guessing -- it was billed
-- to whoever owned the ORIGINAL, which makes a copy somebody else's file.
--
-- No foreign key on purpose: this is a short-lived queue row, and a delete of
-- the user must not be able to fail on a job nobody is waiting for any more.
ALTER TABLE pending_ops ADD COLUMN actor_id INTEGER;

-- Partial, like idx_nodes_owner above it: the overwhelming majority of rows on
-- a scanned storage are system rows, and indexing NULL buys nothing.
CREATE INDEX IF NOT EXISTS idx_nodes_last_actor ON nodes(last_actor_id) WHERE last_actor_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_nodes_last_actor;
ALTER TABLE pending_ops DROP COLUMN actor_id;
ALTER TABLE nodes DROP COLUMN external_upload;
ALTER TABLE nodes DROP COLUMN last_actor_id;
