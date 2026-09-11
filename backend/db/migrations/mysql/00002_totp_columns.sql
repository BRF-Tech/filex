-- +goose Up
-- ⚠ Do not wrap these statements in a goose statement block: goose hands a
-- wrapped block to the server as ONE query, and the MySQL driver refuses a
-- query that carries several statements unless the DSN opts into
-- multiStatements. A wrapper here is what made this migration fail on the
-- FIRST boot of every MySQL install (issue #19). Leave the statements
-- unwrapped, or wrap them one at a time.

-- TOTP / 2FA support columns on users.
-- `totp_secret` already exists in 00001_init.sql; add the missing fields here.
ALTER TABLE users
    ADD COLUMN totp_pending_secret VARCHAR(255),
    ADD COLUMN totp_enabled TINYINT(1) NOT NULL DEFAULT 0,
    ADD COLUMN totp_recovery_codes_json JSON NOT NULL DEFAULT ('[]');

-- MySQL pre-8.0.13 disallows DEFAULT for JSON; back-fill empty array for existing rows.
UPDATE users SET totp_recovery_codes_json = '[]' WHERE totp_recovery_codes_json IS NULL OR JSON_TYPE(totp_recovery_codes_json) IS NULL;


-- +goose Down
ALTER TABLE users
    DROP COLUMN totp_recovery_codes_json,
    DROP COLUMN totp_enabled,
    DROP COLUMN totp_pending_secret;
