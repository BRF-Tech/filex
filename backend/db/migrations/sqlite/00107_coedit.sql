-- +goose Up
-- +goose StatementBegin

-- EDITING TOGETHER: THE RELAY'S SESSIONS (#189, internal/coedit,
-- docs/APP-PLUGINS-API.md → Editing together).
--
-- An app's interface with `files:co-edit` (the office editor) edits a file
-- with other people: every member's editor applies the same log entries in
-- the same order. filex's page seals each entry with the session's key, and
-- the relay puts the sealed entries in one order WITHOUT reading them. These
-- four tables are the relay's whole state, so a restart, or a second process
-- on the same database, still knows every session. A session's state moves
-- by compare-and-set on `rev`: two processes that race on one session cannot
-- both win, and the loser reads the row again.
--
-- coedit_sessions: one row per file being edited together (doc_key is a
-- digest of the storage and the path, unique: one file, one session).
--   sid             the session id (32 hex), what the members name.
--   storage_id,
--   path            the file, for the explorer and for saving.
--   app, build      the app and the build its members run (version + the
--                   package's digest): one build per session.
--   base_sig,
--   saved_sig       the file as the session started from it (size:mtime /
--                   etag) and after its last save.
--   key_mode        'server': the session key is the server's, sealed here
--                   with FILEX_SECRET_KEY (an ordinary folder - the server
--                   holds the file anyway); 'folder': sealed by the members'
--                   folder key in their browsers, which the server cannot
--                   open (an encrypted folder).
--   sealed_key      the sealed session key ('' until it is set).
--   user_salt       hex; a member's `user` is HMAC(salt, account), never the
--                   account id.
--   rev             the compare-and-set counter.
--   head            the last entry's seq; changes_head the last `changes`
--                   entry's; saved_through the last entry the file holds.
--   log_bytes,
--   blob_bytes      what the sealed log and blobs take, against the limits.
--   has_base        1 once the base document is stored: nobody but the
--                   creator joins before.
--   members         how many members are in it now.
--   next_client,
--   next_index      the last connection id and editor user index handed
--                   out (an index is never given twice in a session).
--   lease_holder,
--   lease_until_ms  the changes lease: one writer at a time.
--   created_ms,
--   active_ms       Unix MILLISECONDS, integers on every engine.
CREATE TABLE IF NOT EXISTS coedit_sessions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    sid             TEXT NOT NULL,
    doc_key         TEXT NOT NULL,
    storage_id      INTEGER NOT NULL DEFAULT 0,
    path            TEXT NOT NULL DEFAULT '',
    app             TEXT NOT NULL DEFAULT '',
    build           TEXT NOT NULL DEFAULT '',
    base_sig        TEXT NOT NULL DEFAULT '',
    saved_sig       TEXT NOT NULL DEFAULT '',
    key_mode        TEXT NOT NULL DEFAULT '',
    sealed_key      TEXT NOT NULL DEFAULT '',
    user_salt       TEXT NOT NULL DEFAULT '',
    rev             INTEGER NOT NULL DEFAULT 0,
    head            INTEGER NOT NULL DEFAULT 0,
    changes_head    INTEGER NOT NULL DEFAULT 0,
    saved_through   INTEGER NOT NULL DEFAULT 0,
    log_bytes       INTEGER NOT NULL DEFAULT 0,
    blob_bytes      INTEGER NOT NULL DEFAULT 0,
    has_base        INTEGER NOT NULL DEFAULT 0,
    members         INTEGER NOT NULL DEFAULT 0,
    next_client     INTEGER NOT NULL DEFAULT 0,
    next_index      INTEGER NOT NULL DEFAULT 0,
    lease_holder    TEXT NOT NULL DEFAULT '',
    lease_until_ms  INTEGER NOT NULL DEFAULT 0,
    created_ms      INTEGER NOT NULL DEFAULT 0,
    active_ms       INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_sessions_sid ON coedit_sessions (sid);
CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_sessions_doc ON coedit_sessions (doc_key);
CREATE INDEX IF NOT EXISTS idx_coedit_sessions_active ON coedit_sessions (members, active_ms);

-- coedit_members: who is in a session now. The server says who a member is
-- and whether it may write (the account signed in on the request and the
-- file's rules), never the joining connection.
--   client          the relay's id for the connection ("c1", "c2"...).
--   token_hash      hex SHA-256 of the member's token: every request that
--                   names the member carries it, so two people behind one
--                   shared proxy token (one account) cannot act as each
--                   other. The token itself is never stored.
--   user_id         the account (never handed to the app).
--   user_ref        the opaque id the app is told.
--   name            the display name the others see.
--   index_user      the editor's per-session user index.
--   can_edit        1: may append, take the lease, save.
--   join_seq        the seq of its join entry (who joined first saves).
--   last_ctr        the writer's counter: it only grows.
--   seen_ms         the last sign of life of its page; a member unseen for
--                   45 s is dropped (a leave entry).
CREATE TABLE IF NOT EXISTS coedit_members (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sid         TEXT NOT NULL,
    client      TEXT NOT NULL,
    token_hash  TEXT NOT NULL DEFAULT '',
    user_id     INTEGER NOT NULL DEFAULT 0,
    user_ref    TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    index_user  INTEGER NOT NULL DEFAULT 0,
    can_edit    INTEGER NOT NULL DEFAULT 0,
    join_seq    INTEGER NOT NULL DEFAULT 0,
    last_ctr    INTEGER NOT NULL DEFAULT 0,
    seen_ms     INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_members_client ON coedit_members (sid, client);
CREATE INDEX IF NOT EXISTS idx_coedit_members_seen ON coedit_members (seen_ms);

-- coedit_entries: the log. A member's entry carries its sealed payload (ct)
-- and its counter; the relay's own (join, leave, saved) carry plain
-- metadata: the member who joined (JSON), the part of the log a save holds
-- and the file's signature after it. (sid, seq) is unique: an entry lands at
-- exactly one place or not at all.
CREATE TABLE IF NOT EXISTS coedit_entries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sid         TEXT NOT NULL,
    seq         INTEGER NOT NULL,
    kind        TEXT NOT NULL DEFAULT '',
    client      TEXT NOT NULL DEFAULT '',
    at_ms       INTEGER NOT NULL DEFAULT 0,
    ctr         INTEGER NOT NULL DEFAULT 0,
    ct          BLOB,
    member      TEXT NOT NULL DEFAULT '',
    through     INTEGER NOT NULL DEFAULT 0,
    sig         TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_entries_seq ON coedit_entries (sid, seq);

-- coedit_blobs: the session's sealed blobs (the base document, the images
-- added while editing), written once each. The bytes are files under
-- <data_dir>/coedit/<sid>/; this row says they are complete.
CREATE TABLE IF NOT EXISTS coedit_blobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sid         TEXT NOT NULL,
    name        TEXT NOT NULL,
    size        INTEGER NOT NULL DEFAULT 0,
    sha256      TEXT NOT NULL DEFAULT '',
    created_ms  INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_blobs_name ON coedit_blobs (sid, name);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS coedit_blobs;
DROP TABLE IF EXISTS coedit_entries;
DROP TABLE IF EXISTS coedit_members;
DROP TABLE IF EXISTS coedit_sessions;
-- +goose StatementEnd
