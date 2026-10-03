-- +goose Up
-- +goose StatementBegin

-- SIGN-IN ATTEMPT COUNTERS (internal/loginguard). The model is written out in
-- db/migrations/sqlite/00072_login_throttle.sql: one row per counted account
-- identifier or IP address, kept in the database so a restart or a second
-- instance does not reset an attacker's guesses.
CREATE TABLE IF NOT EXISTS login_throttle (
    id            BIGSERIAL PRIMARY KEY,
    scope         TEXT NOT NULL,
    subject       TEXT NOT NULL,
    fails         INTEGER NOT NULL DEFAULT 0,
    lock_level    INTEGER NOT NULL DEFAULT 0,
    window_start  TIMESTAMPTZ NOT NULL,
    locked_until  TIMESTAMPTZ,
    last_fail_at  TIMESTAMPTZ NOT NULL,
    last_ip       TEXT NOT NULL DEFAULT '',
    last_protocol TEXT NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_login_throttle_subject ON login_throttle (scope, subject);
CREATE INDEX IF NOT EXISTS idx_login_throttle_locked ON login_throttle (locked_until);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS login_throttle;
-- +goose StatementEnd
