-- +goose Up
-- +goose StatementBegin

-- PLUGIN INSTALL REQUESTS (docs/APP-PLUGINS.md → Install requests,
-- docs/PLUGINS.md → Install requests).
--
-- An API token (an agent, a script, the CLI) may no longer install, upgrade,
-- remove or re-permission a plugin: those need an administrator signed in to
-- the admin panel. What a token CAN do is leave a request. filex resolves the
-- source at once (the install review's dry run) and FREEZES what it found —
-- the manifest, the hash, the permissions — so the administrator approves
-- exactly what the requester saw. Approval installs those bytes and nothing
-- else: when the source answers different bytes by then, the request is
-- `superseded` instead of installed.
--
-- request_key      random hex, unique: what the store reads a new row back by.
-- kind             app | storage.
-- op               install | upgrade.
-- name             the plugin's name (an app's manifest name; the name a
--                  storage plugin is installed under).
-- plugin_id        upgrade: the installed plugin (app_plugins.id or plugins.id,
--                  per kind). NULL for an install.
-- source_kind      github | url | source | from_source.
-- source_json      the source as the requester gave it (repository + ref,
--                  addresses + pin, a storage plugin's feed).
-- source_key       sha256 of the normalized source: one PENDING request per
--                  source — a second request for it answers the first.
-- version          the version the source answered; from_version the one an
--                  upgrade replaces.
-- manifest_json    the resolved snapshot: an app's filex-app.json as fetched,
--                  a storage plugin's filex-storage.json feed.
-- review_json      the dry run's review (for the approval screen).
-- sha256           what approval holds the bytes to: an app's module (its
--                  manifest when it has none), a storage plugin's binary.
-- manifest_sha256  an app's manifest hash (the module does not pin it).
-- permissions_json the permissions the request asks to grant, frozen.
-- requested_by     the account that asked; requester its name as shown then;
-- token_id/label   the API key it came through (NULL/'' from a session).
-- reason           the requester's words.
-- status           pending | approved | rejected | expired | superseded.
-- decided_by/decider/decided_at  who closed it and when (expired: nobody).
-- decision_note    a rejection's reason, or why it was superseded.
-- result_json      what approval did: the installed plugin, or the last
--                  attempt's refusal while the request stays pending.
-- expires_at       a pending request past this becomes `expired`
--                  (FILEX_PLUGIN_REQUEST_TTL_DAYS, 14 by default).
--
-- 00069: the highest number in every filex worktree on disk was 00068.
CREATE TABLE IF NOT EXISTS plugin_requests (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    request_key      TEXT NOT NULL,
    kind             TEXT NOT NULL,
    op               TEXT NOT NULL,
    name             TEXT NOT NULL DEFAULT '',
    plugin_id        INTEGER,
    source_kind      TEXT NOT NULL,
    source_json      TEXT NOT NULL DEFAULT '{}',
    source_key       TEXT NOT NULL,
    version          TEXT NOT NULL DEFAULT '',
    from_version     TEXT NOT NULL DEFAULT '',
    manifest_json    TEXT NOT NULL DEFAULT '',
    review_json      TEXT NOT NULL DEFAULT '',
    sha256           TEXT NOT NULL DEFAULT '',
    manifest_sha256  TEXT NOT NULL DEFAULT '',
    permissions_json TEXT NOT NULL DEFAULT '[]',
    requested_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    requester        TEXT NOT NULL DEFAULT '',
    token_id         INTEGER,
    token_label      TEXT NOT NULL DEFAULT '',
    reason           TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'pending',
    decided_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
    decider          TEXT NOT NULL DEFAULT '',
    decided_at       DATETIME,
    decision_note    TEXT NOT NULL DEFAULT '',
    result_json      TEXT NOT NULL DEFAULT '',
    expires_at       DATETIME NOT NULL,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_requests_key ON plugin_requests (request_key);
CREATE INDEX IF NOT EXISTS idx_plugin_requests_status ON plugin_requests (status);
CREATE INDEX IF NOT EXISTS idx_plugin_requests_source ON plugin_requests (source_key, status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plugin_requests;
-- +goose StatementEnd
