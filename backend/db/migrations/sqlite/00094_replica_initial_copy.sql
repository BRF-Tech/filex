-- +goose Up
-- +goose StatementBegin

-- REPLICATION THAT RUNS (#186, docs/REPLICATION.md).
--
-- 1. replica_failures learns which storage a failure belongs to.
--
--    A failure row is a path and an operation, and a path is relative to a
--    storage. Until 0.53 no storage was ever wrapped for replication, so the
--    table only had to hold one; now several storages can replicate at once,
--    to different targets, and a repair has to know whose wrapper to replay
--    it through. The unique key moves from (path, op) to
--    (storage_id, path, op): the same path failing on two storages is two
--    failures. Rows from before carry storage_id 0, which no storage claims
--    (a repair resolves them as having nothing left to repair).
--
--    SQLite cannot change a table's UNIQUE constraint in place, so the table
--    is rebuilt with its rows.
--
-- 2. replica_initial_copies: the copy of the files a storage already held
--    when it was linked to a target. One row per storage, for the target it is
--    linked to now. The copy runs from the queue in short slices and keeps its
--    place in walk_cursor (internal/replica initial.go, walk.go), so a restart
--    picks it up where it stopped.
--
--    phase           pending | counting | copying | waiting | done
--    walk_cursor     the last file the walk finished with ('' = start)
--    counted         1: total_files is final
--    *_files         the counters the Replication page shows
--    *_unix          Unix seconds (integers: every engine returns them as
--                    they went in)
--    lease_owner     the worker running a slice now, lease_until until when
--    revision        moves on every write, so an UPDATE always changes the row

CREATE TABLE replica_failures_0094 (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    storage_id      INTEGER NOT NULL DEFAULT 0,
    path            TEXT NOT NULL,
    op              TEXT NOT NULL,
    error_code      TEXT NOT NULL DEFAULT '',
    error_msg       TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 1,
    last_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at     DATETIME,
    UNIQUE(storage_id, path, op)
);
INSERT INTO replica_failures_0094 (id, storage_id, path, op, error_code, error_msg, attempts, last_attempt_at, resolved_at)
SELECT id, 0, path, op, error_code, error_msg, attempts, last_attempt_at, resolved_at FROM replica_failures;
DROP INDEX IF EXISTS idx_replica_failures_unresolved;
DROP TABLE replica_failures;
ALTER TABLE replica_failures_0094 RENAME TO replica_failures;
CREATE INDEX IF NOT EXISTS idx_replica_failures_unresolved
    ON replica_failures (resolved_at, last_attempt_at DESC);

CREATE TABLE IF NOT EXISTS replica_initial_copies (
    storage_id      INTEGER PRIMARY KEY,
    target_id       INTEGER NOT NULL DEFAULT 0,
    phase           TEXT NOT NULL DEFAULT 'pending',
    walk_cursor     TEXT NOT NULL DEFAULT '',
    counted         INTEGER NOT NULL DEFAULT 0,
    total_files     INTEGER NOT NULL DEFAULT 0,
    copied_files    INTEGER NOT NULL DEFAULT 0,
    present_files   INTEGER NOT NULL DEFAULT 0,
    excluded_files  INTEGER NOT NULL DEFAULT 0,
    failed_files    INTEGER NOT NULL DEFAULT 0,
    copied_bytes    INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT NOT NULL DEFAULT '',
    started_unix    INTEGER NOT NULL DEFAULT 0,
    updated_unix    INTEGER NOT NULL DEFAULT 0,
    finished_unix   INTEGER NOT NULL DEFAULT 0,
    lease_owner     TEXT NOT NULL DEFAULT '',
    lease_until     INTEGER NOT NULL DEFAULT 0,
    revision        INTEGER NOT NULL DEFAULT 0
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS replica_initial_copies;

CREATE TABLE replica_failures_0008 (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    path            TEXT NOT NULL,
    op              TEXT NOT NULL,
    error_code      TEXT NOT NULL DEFAULT '',
    error_msg       TEXT NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 1,
    last_attempt_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at     DATETIME,
    UNIQUE(path, op)
);
INSERT OR IGNORE INTO replica_failures_0008 (id, path, op, error_code, error_msg, attempts, last_attempt_at, resolved_at)
SELECT id, path, op, error_code, error_msg, attempts, last_attempt_at, resolved_at FROM replica_failures ORDER BY id;
DROP INDEX IF EXISTS idx_replica_failures_unresolved;
DROP TABLE replica_failures;
ALTER TABLE replica_failures_0008 RENAME TO replica_failures;
CREATE INDEX IF NOT EXISTS idx_replica_failures_unresolved
    ON replica_failures (resolved_at, last_attempt_at DESC);
-- +goose StatementEnd
