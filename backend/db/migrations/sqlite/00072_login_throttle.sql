-- +goose Up
-- +goose StatementBegin

-- SIGN-IN ATTEMPT COUNTERS (internal/loginguard, docs/CONFIGURATION.md → Sign-in
-- attempt limits).
--
-- One row per thing that is counted: an ACCOUNT (the identifier a person typed,
-- normalized — whether or not such an account exists, so the answer to "how many
-- tries are left" cannot tell the two apart) or an IP address. The counters live
-- here and not in process memory so that a restart, a second instance behind the
-- same database, or a deploy does not hand an attacker a fresh set of guesses.
--
-- scope         account | ip.
-- subject       the normalized identifier, or the address. Long identifiers are
--               shortened to a fixed form before they get here (the unique key
--               below has to fit MySQL's index size).
-- fails         wrong attempts inside the current window.
-- lock_level    how many times this subject has been locked in a row: the
--               lock's length doubles with it, up to a ceiling. It falls back
--               to 0 after a success or a quiet spell.
-- window_start  when the current counting window began.
-- locked_until  NULL, or the instant the lock ends.
-- last_fail_at  the most recent wrong attempt.
-- last_ip       the address of the most recent wrong attempt (account rows).
-- last_protocol web | dav | ftp | sftp | s3 — the door it came through.
CREATE TABLE IF NOT EXISTS login_throttle (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    scope         TEXT NOT NULL,
    subject       TEXT NOT NULL,
    fails         INTEGER NOT NULL DEFAULT 0,
    lock_level    INTEGER NOT NULL DEFAULT 0,
    window_start  DATETIME NOT NULL,
    locked_until  DATETIME,
    last_fail_at  DATETIME NOT NULL,
    last_ip       TEXT NOT NULL DEFAULT '',
    last_protocol TEXT NOT NULL DEFAULT '',
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_login_throttle_subject ON login_throttle (scope, subject);
CREATE INDEX IF NOT EXISTS idx_login_throttle_locked ON login_throttle (locked_until);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS login_throttle;
-- +goose StatementEnd
