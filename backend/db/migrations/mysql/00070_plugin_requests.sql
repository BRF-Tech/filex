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
-- ⚠ The indexed columns are VARCHAR: MySQL cannot index a TEXT column without
-- a prefix length. source_key is a sha256 (64 hex), request_key 32 hex.
--
-- 00069: the highest number in every filex worktree on disk was 00068.
CREATE TABLE IF NOT EXISTS plugin_requests (
    id               BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    request_key      VARCHAR(64) NOT NULL,
    kind             VARCHAR(16) NOT NULL,
    op               VARCHAR(16) NOT NULL,
    name             VARCHAR(190) NOT NULL DEFAULT '',
    plugin_id        BIGINT NULL,
    source_kind      VARCHAR(16) NOT NULL,
    source_json      TEXT NOT NULL DEFAULT ('{}'),
    source_key       VARCHAR(64) NOT NULL,
    version          VARCHAR(64) NOT NULL DEFAULT '',
    from_version     VARCHAR(64) NOT NULL DEFAULT '',
    manifest_json    MEDIUMTEXT NOT NULL DEFAULT (''),
    review_json      MEDIUMTEXT NOT NULL DEFAULT (''),
    sha256           VARCHAR(64) NOT NULL DEFAULT '',
    manifest_sha256  VARCHAR(64) NOT NULL DEFAULT '',
    permissions_json TEXT NOT NULL DEFAULT ('[]'),
    requested_by     BIGINT NULL,
    requester        VARCHAR(255) NOT NULL DEFAULT '',
    token_id         BIGINT NULL,
    token_label      VARCHAR(255) NOT NULL DEFAULT '',
    reason           TEXT NOT NULL DEFAULT (''),
    status           VARCHAR(16) NOT NULL DEFAULT 'pending',
    decided_by       BIGINT NULL,
    decider          VARCHAR(255) NOT NULL DEFAULT '',
    decided_at       DATETIME(6) NULL,
    decision_note    TEXT NOT NULL DEFAULT (''),
    result_json      MEDIUMTEXT NOT NULL DEFAULT (''),
    expires_at       DATETIME(6) NOT NULL,
    created_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_plugin_requests_key (request_key),
    INDEX idx_plugin_requests_status (status),
    INDEX idx_plugin_requests_source (source_key, status),
    CONSTRAINT fk_plugin_requests_requested_by FOREIGN KEY (requested_by) REFERENCES users(id) ON DELETE SET NULL,
    CONSTRAINT fk_plugin_requests_decided_by FOREIGN KEY (decided_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plugin_requests;
-- +goose StatementEnd
