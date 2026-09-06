-- +goose Up
-- Repair storage_key on rows a move or rename left behind.
--
-- Store.MoveNode updated parent_id, name, path and path_hash but NOT
-- storage_key, so every file that was ever renamed or moved (and, since the
-- folder-move fix, every descendant of a moved folder) kept the key it had at
-- its old path. The driver code is fixed; these rows are not, and nothing
-- else repairs them: the periodic storage walk only touches seen_at and
-- UpdateNodeMeta, neither of which writes this column, so a wrong value
-- survives every sync run indefinitely.
--
-- It matters because storage_key is what internal/versioning (Snapshot and
-- Restore), the antivirus quarantine, the id-addressed download and the sync
-- tombstone pass hand to the driver IN PREFERENCE TO path -- and each of them
-- fails silently on a miss rather than loudly: no snapshot is taken before an
-- overwrite, quarantine reports success while the infected bytes stay live,
-- and a file that is perfectly fine gets tombstoned.
--
-- ⚠ LIVE rows only. On a trashed row storage_key deliberately holds the
-- ORIGINAL path while path points into `.filex-trash/` -- that is what
-- trash.Service.Restore puts the file back with, and what
-- sync.reconcileTrash reads to tell a restorable deletion from a row that
-- must be hard-deleted. Rewriting those would destroy both.
--
-- ⚠ NULL / empty storage_key is left alone: every reader already falls back
-- to path when it is empty, so those rows are correct as they stand and
-- touching them would only churn legacy data.
UPDATE nodes
   SET storage_key = path
 WHERE deleted_at IS NULL
   AND storage_key IS NOT NULL
   AND storage_key <> ''
   AND storage_key <> path;

-- +goose Down
-- Irreversible on purpose: the pre-repair values were wrong, and which rows
-- held which wrong value is not recorded anywhere. Rolling back would mean
-- re-breaking history, quarantine and downloads for the same files.
SELECT 1;
