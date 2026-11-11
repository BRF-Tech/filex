-- +goose Up
-- UPLOADERS (filex 0.60): a person's upload destinations - a screenshot
-- tool, a phone shortcut, a script - each with its own key, folder and link
-- rule, and a deletion link per upload.
--
-- # Why the key is not an API token
--
-- An API token is accepted by every door a token opens: the agent API, the
-- explorer's routes, WebDAV, MCP. The narrowest token a person could mint for
-- a screenshot tool (`write` + a `root:` folder) still listed, overwrote and
-- moved files in that folder, and the tool keeps it in a settings file that
-- is copied between machines and pasted into chats. An uploader's key is
-- looked up here and nowhere else: ONE door (POST /api/uploads) knows it, and
-- that door only adds a new file to the uploader's folder. Like an API token
-- it is a bearer credential the server only compares, so it is stored as its
-- sha256 (key_hash) and shown once.
--
-- The folder is held by the storage's id, which never moves, and tenant_id
-- is the tenant the storage was in when the uploader was made (its first
-- provider_storages row, 0 for none - db.StorageStamp): an upload is refused
-- once that tenant no longer has the storage (lesson #1787). link_days 0 asks
-- for the longest link the server allows. Times are unix seconds, so no
-- engine's timestamp spelling is involved (as captcha_marks, 00134).
CREATE TABLE IF NOT EXISTS uploaders (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    storage_id   INTEGER NOT NULL,
    folder       TEXT NOT NULL DEFAULT '',
    visibility   TEXT NOT NULL DEFAULT 'public',
    link_days    INTEGER NOT NULL DEFAULT 0,
    tenant_id    INTEGER NOT NULL DEFAULT 0,
    key_hash     TEXT NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    key_set_at   INTEGER NOT NULL,
    last_used_at INTEGER
);
CREATE INDEX IF NOT EXISTS uploaders_user_idx ON uploaders(user_id);

-- One row per upload: its deletion link. The link names the FILE by its
-- catalogue row (node_id) on a storage by id, so a file moved or renamed since
-- is still the one it deletes and another file that later takes its old name
-- is never touched. share_id is the public link the upload answered with (0
-- for a private one); deleting revokes it. uploader_id is kept without a
-- foreign key on purpose: deleting an uploader revokes its KEY, while the
-- deletion links it already handed out keep working for what was uploaded.
-- The row goes when its link is used, or with the account.
CREATE TABLE IF NOT EXISTS upload_deletions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash  TEXT NOT NULL UNIQUE,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    uploader_id INTEGER NOT NULL DEFAULT 0,
    storage_id  INTEGER NOT NULL,
    node_id     INTEGER NOT NULL,
    share_id    INTEGER NOT NULL DEFAULT 0,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
    name        TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS upload_deletions_user_idx ON upload_deletions(user_id);

-- +goose Down
DROP INDEX IF EXISTS upload_deletions_user_idx;
DROP TABLE IF EXISTS upload_deletions;
DROP INDEX IF EXISTS uploaders_user_idx;
DROP TABLE IF EXISTS uploaders;
