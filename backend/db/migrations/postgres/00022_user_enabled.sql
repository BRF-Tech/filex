-- +goose Up
-- Per-user on/off switch (olivov G4). Cuts a user's access without deleting
-- the account: a disabled user cannot start a session (auth.LoginAllowed
-- refuses them on local login, OIDC and /dav alike) while their files, quota
-- and grants stay exactly where they are.
--
-- Defaults to enabled so every existing row keeps working.
ALTER TABLE users ADD COLUMN IF NOT EXISTS enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS enabled;
