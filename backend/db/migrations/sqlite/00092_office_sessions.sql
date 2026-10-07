-- +goose Up
-- +goose StatementBegin

-- THE VERSION EACH OFFICE EDITING SESSION OPENED (#184, internal/onlyoffice
-- session_base.go, docs/ONLYOFFICE.md → When the document changes while it is
-- open).
--
-- One row per ONLYOFFICE document key filex handed an editing config for: the
-- version the storage driver reported for the file at that moment. A save of
-- the session is written over the file only while the file is still that
-- version; otherwise it goes beside it as a conflict copy. Kept here and not
-- only in process memory so that a restart, or a second instance behind the
-- same database, still knows which version a running session stands on (an
-- in-process cache sits in front of it, internal/memcache; the decision at a
-- save is always read from this table).
--
-- doc_key          the session's document.key (md5 hex for filex's own keys).
-- node_id          the document's node.
-- file_size        the size the driver reported.
-- file_mtime_ns    its modification time, Unix NANOSECONDS: an integer, so
--                  the value comes back exactly as it went in on every engine
--                  (a timestamp column would round to microseconds on some,
--                  and a re-read of the same file would no longer match).
-- file_etag        the driver's etag, '' when it has none (a disk).
-- version_unknown  1: the session is known to be on an OLDER version, but not
--                  which one (asked about after a restart) - never current.
-- dropped          1: the person chose the outside version; the session's
--                  save, when it comes, is not written.
-- expires_unix     when the row may be removed (Unix seconds); the session's
--                  end removes it earlier.
CREATE TABLE IF NOT EXISTS office_sessions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    doc_key         TEXT NOT NULL,
    node_id         INTEGER NOT NULL,
    file_size       INTEGER NOT NULL DEFAULT 0,
    file_mtime_ns   INTEGER NOT NULL DEFAULT 0,
    file_etag       TEXT NOT NULL DEFAULT '',
    version_unknown INTEGER NOT NULL DEFAULT 0,
    dropped         INTEGER NOT NULL DEFAULT 0,
    expires_unix    INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_office_sessions_key ON office_sessions (doc_key);
CREATE INDEX IF NOT EXISTS idx_office_sessions_expires ON office_sessions (expires_unix);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS office_sessions;
-- +goose StatementEnd
