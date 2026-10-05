-- +goose Up
-- THE APP STORE'S STATE (filex 0.52.0, internal/appstore). The rows are written
-- out in db/migrations/sqlite/00081_app_store.sql: the installation's id, the
-- stores an administrator trusted, the paid apps' licenses (the key sealed),
-- the install links already used and the proven time (license grace).
--
-- ⚠ Not the settings table, which an admin-scoped API key lists and writes.
--
-- ⚠ state_key is compared byte for byte (utf8mb4_0900_bin, the NO PAD binary
-- collation 00041 and 00080 chose): under the default accent- and
-- case-insensitive collation `trust:https://Store.example` would be the same
-- row as `trust:https://store.example`, which SQLite and PostgreSQL never
-- match. VARCHAR because MySQL keys a bounded column only.
--
-- 00081: filex v0.51.0 numbers its own migrations up to 00080.
CREATE TABLE IF NOT EXISTS app_store_state (
    state_key   VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL,
    state_value TEXT NOT NULL DEFAULT (''),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (state_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS app_store_state;
