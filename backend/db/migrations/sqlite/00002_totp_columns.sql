-- +goose Up
-- +goose StatementBegin

-- TOTP / 2FA support columns on users.
-- `totp_secret` already exists in 00001_init.sql; add the missing fields here.
ALTER TABLE users ADD COLUMN totp_pending_secret TEXT;
ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN totp_recovery_codes_json TEXT NOT NULL DEFAULT '[]';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN totp_recovery_codes_json;
ALTER TABLE users DROP COLUMN totp_enabled;
ALTER TABLE users DROP COLUMN totp_pending_secret;
-- +goose StatementEnd
