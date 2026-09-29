-- +goose Up
-- +goose StatementBegin

-- PLUGIN INSTALL REQUESTS (docs/APP-PLUGINS.md → Install requests,
-- docs/PLUGINS.md → Install requests). The model is written out in
-- db/migrations/sqlite/00069_plugin_requests.sql.
--
-- An API token may no longer install, upgrade, remove or re-permission a
-- plugin; it leaves a request, filex freezes what the source answered, and an
-- administrator signed in to the admin panel approves or rejects it.
--
-- 00069: the highest number in every filex worktree on disk was 00068.
CREATE TABLE IF NOT EXISTS plugin_requests (
    id               BIGSERIAL PRIMARY KEY,
    request_key      TEXT NOT NULL,
    kind             TEXT NOT NULL,
    op               TEXT NOT NULL,
    name             TEXT NOT NULL DEFAULT '',
    plugin_id        BIGINT,
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
    requested_by     BIGINT REFERENCES users(id) ON DELETE SET NULL,
    requester        TEXT NOT NULL DEFAULT '',
    token_id         BIGINT,
    token_label      TEXT NOT NULL DEFAULT '',
    reason           TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'pending',
    decided_by       BIGINT REFERENCES users(id) ON DELETE SET NULL,
    decider          TEXT NOT NULL DEFAULT '',
    decided_at       TIMESTAMPTZ,
    decision_note    TEXT NOT NULL DEFAULT '',
    result_json      TEXT NOT NULL DEFAULT '',
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_requests_key ON plugin_requests (request_key);
CREATE INDEX IF NOT EXISTS idx_plugin_requests_status ON plugin_requests (status);
CREATE INDEX IF NOT EXISTS idx_plugin_requests_source ON plugin_requests (source_key, status);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plugin_requests;
-- +goose StatementEnd
