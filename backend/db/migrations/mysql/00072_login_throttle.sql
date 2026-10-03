-- +goose Up
-- +goose StatementBegin

-- SIGN-IN ATTEMPT COUNTERS (internal/loginguard). The model is written out in
-- db/migrations/sqlite/00072_login_throttle.sql: one row per counted account
-- identifier or IP address, kept in the database so a restart or a second
-- instance does not reset an attacker's guesses.
--
-- ⚠ The indexed columns are VARCHAR: MySQL cannot index a TEXT column without
-- a prefix length. The guard shortens a subject to 190 characters (a hash
-- stands in for the tail) before it is stored.
CREATE TABLE IF NOT EXISTS login_throttle (
    id            BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    scope         VARCHAR(16) NOT NULL,
    subject       VARCHAR(190) NOT NULL,
    fails         INT NOT NULL DEFAULT 0,
    lock_level    INT NOT NULL DEFAULT 0,
    window_start  DATETIME(6) NOT NULL,
    locked_until  DATETIME(6) NULL,
    last_fail_at  DATETIME(6) NOT NULL,
    last_ip       VARCHAR(64) NOT NULL DEFAULT '',
    last_protocol VARCHAR(16) NOT NULL DEFAULT '',
    updated_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_login_throttle_subject (scope, subject),
    INDEX idx_login_throttle_locked (locked_until)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS login_throttle;
-- +goose StatementEnd
