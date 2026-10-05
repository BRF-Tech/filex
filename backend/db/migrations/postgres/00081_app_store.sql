-- +goose Up
-- THE APP STORE'S STATE (filex 0.52.0, internal/appstore). The rows are written
-- out in db/migrations/sqlite/00081_app_store.sql: the installation's id, the
-- stores an administrator trusted, the paid apps' licenses (the key sealed),
-- the install links already used and the proven time (license grace).
--
-- ⚠ Not the settings table, which an admin-scoped API key lists and writes.
--
-- 00081: filex v0.51.0 numbers its own migrations up to 00080.
CREATE TABLE IF NOT EXISTS app_store_state (
    state_key   TEXT NOT NULL PRIMARY KEY,
    state_value TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS app_store_state CASCADE;
