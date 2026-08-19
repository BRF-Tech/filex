-- +goose Up
-- +goose StatementBegin
-- Storage plugins — see the sqlite variant for the reasoning. In short: the
-- admin's registration of an out-of-process storage driver; runtime state is
-- derived, not stored. See internal/plugin and docs/PLUGINS.md.
--
-- ⚠ VARCHAR on name: MySQL cannot put a UNIQUE index on TEXT without a
-- prefix length, and name is what a plugin is looked up by.
CREATE TABLE IF NOT EXISTS plugins (
    id           BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name         VARCHAR(64) NOT NULL UNIQUE,
    kind         VARCHAR(16) NOT NULL DEFAULT 'binary',
    binary       VARCHAR(255) NOT NULL DEFAULT '',
    sha256       VARCHAR(64) NOT NULL DEFAULT '',
    address      TEXT NOT NULL,
    token_sealed TEXT NOT NULL,
    enabled      TINYINT(1) NOT NULL DEFAULT 1,
    version      VARCHAR(64) NOT NULL DEFAULT '',
    driver       VARCHAR(64) NOT NULL DEFAULT '',
    last_error   TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plugins;
-- +goose StatementEnd
