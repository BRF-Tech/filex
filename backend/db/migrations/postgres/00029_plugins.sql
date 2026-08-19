-- +goose Up
-- Storage plugins — see the sqlite variant for the reasoning. In short: the
-- admin's registration of an out-of-process storage driver (binary filex
-- launches, or remote service it connects to); runtime state is derived, not
-- stored. See internal/plugin and docs/PLUGINS.md.
CREATE TABLE IF NOT EXISTS plugins (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT NOT NULL UNIQUE,
    kind         TEXT NOT NULL DEFAULT 'binary',
    binary       TEXT NOT NULL DEFAULT '',
    sha256       TEXT NOT NULL DEFAULT '',
    address      TEXT NOT NULL DEFAULT '',
    token_sealed TEXT NOT NULL DEFAULT '',
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    version      TEXT NOT NULL DEFAULT '',
    driver       TEXT NOT NULL DEFAULT '',
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS plugins;
