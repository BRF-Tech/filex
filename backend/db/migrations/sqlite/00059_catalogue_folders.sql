-- +goose Up
-- +goose StatementBegin

-- PER-FOLDER CATALOGUE STATE — the lazy catalogue (issue #45, sync_mode lazy).
--
-- Every other sync mode builds a storage's catalogue by walking the whole
-- storage, so "is this folder catalogued?" had one answer for every folder:
-- yes, once the first scan finished. A lazy storage catalogues a folder when
-- somebody opens it, and (behaviour A) fills in the rest in the background, so
-- the answer is per folder and has to survive a restart. See
-- docs/LAZY-CATALOGUE.md.
--
-- One row per folder the lazy catalogue knows about. No row = not catalogued.
--   state       'uncatalogued' — seen in a parent's listing, never listed
--                                 itself (the background filler's work list)
--               'catalogued'   — its listing has been applied
--               'watched'      — catalogued, and under an fsnotify watch in the
--                                 running process (demoted at every start)
--   reconciled_at      when its last complete listing was applied
--   visited_at         when a person last opened it (the watch budget's LRU)
--   watched_at         when its current watch was placed (NULL: none)
--   reconcile_on_open  its watch was evicted/expired/lost: reconcile first
--   entries, held_back what that listing saw, and the deletions its guard
--                      held back
--
-- ⚠⚠ This table never decides that a FILE is gone. The delete pass only ever
-- runs on a folder that has just been listed, and only over that folder's
-- direct children (internal/sync/lazy.go). What it records is what was listed
-- and when.
CREATE TABLE IF NOT EXISTS catalogue_folders (
    storage_id        INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path_hash         TEXT NOT NULL,
    path              TEXT NOT NULL,
    depth             INTEGER NOT NULL DEFAULT 0,
    state             TEXT NOT NULL DEFAULT 'uncatalogued',
    reconciled_at     DATETIME,
    visited_at        DATETIME,
    watched_at        DATETIME,
    reconcile_on_open INTEGER NOT NULL DEFAULT 0,
    entries           INTEGER NOT NULL DEFAULT 0,
    held_back         INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (storage_id, path_hash)
);

-- The filler's frontier ("uncatalogued, shallowest first") and the per-state
-- counts the coverage figures read are range scans on this index.
CREATE INDEX IF NOT EXISTS idx_catalogue_folders_state ON catalogue_folders (storage_id, state, depth);

-- "Is anything below this folder still uncatalogued?" (a folder size is only
-- complete when nothing is) is a prefix range on path.
CREATE INDEX IF NOT EXISTS idx_catalogue_folders_path ON catalogue_folders (storage_id, path);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_catalogue_folders_path;
DROP INDEX IF EXISTS idx_catalogue_folders_state;
DROP TABLE IF EXISTS catalogue_folders;
-- +goose StatementEnd
