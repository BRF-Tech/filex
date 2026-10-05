-- +goose Up
-- THE APP STORE'S STATE (filex 0.52.0, docs/APP-PLUGINS.md "Installing from a
-- store", "Paid apps"; internal/appstore).
--
-- One small key/value table, read and written by internal/appstore alone:
--
--   instance_id        this installation's opaque id, sent to a store with a
--                      license check (random, made once).
--   trust:<origin>     a store an administrator trusted, and the keys they
--                      saw when they did (the fingerprints the store's
--                      keys.json showed then). A changed key asks again.
--   license:<app>      a paid app's license: the key SEALED with the
--                      installation's secret key (internal/secretbox), its
--                      prefix for the screens, the last signed answer the
--                      store gave and when it has to be asked again.
--   intent:<store>/<token_id>  an install link that was used (installed or
--                      cancelled): the same link is refused afterwards.
--   clock              the proven time: the latest checked_at a store signed
--                      plus the time filex has run since (monotonic), carried
--                      over a restart: a clock turned back cannot stretch a
--                      license's grace.
--
-- ⚠ Not the settings table: every row there is listed by GET
-- /api/admin/settings and written by PUT /api/admin/settings/{key}, which an
-- admin-scoped API key reaches. Trusting a store is a signed-in
-- administrator's decision, and a license key is not for any answer.
--
-- 00081: filex v0.51.0 numbers its own migrations up to 00080.
CREATE TABLE IF NOT EXISTS app_store_state (
    state_key   TEXT NOT NULL PRIMARY KEY,
    state_value TEXT NOT NULL DEFAULT '',
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS app_store_state;
