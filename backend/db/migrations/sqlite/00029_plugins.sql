-- +goose Up
-- Storage plugins: an out-of-process storage driver the admin registered.
--
-- # What a row is
--
-- The admin's INTENT — which plugin, from where, on or off. What the plugin
-- is doing right now (running, crashed, refused, which driver it registered)
-- is runtime state the plugin manager derives by starting it, and is never
-- written here; a restart re-derives it. Only the last describe (driver,
-- version) and the last failure are kept, so a stopped plugin still shows
-- something useful in the list.
--
-- # Two kinds
--
--   binary — a program under <data-dir>/plugins/<name>/ that filex launches
--            and supervises. The sha256 is checked on EVERY start: a file that
--            changed under filex is refused, not run.
--   remote — a service the operator runs; filex connects to `address` with
--            the bearer token in token_sealed (secretbox, FILEX_SECRET_KEY —
--            registering one without a key is refused rather than stored in
--            plaintext). Binary plugins get a token minted per start and it
--            is never stored.
--
-- See internal/plugin and docs/PLUGINS.md.
CREATE TABLE IF NOT EXISTS plugins (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    kind         TEXT NOT NULL DEFAULT 'binary',
    binary       TEXT NOT NULL DEFAULT '',
    sha256       TEXT NOT NULL DEFAULT '',
    address      TEXT NOT NULL DEFAULT '',
    token_sealed TEXT NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL DEFAULT 1,
    version      TEXT NOT NULL DEFAULT '',
    driver       TEXT NOT NULL DEFAULT '',
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS plugins;
