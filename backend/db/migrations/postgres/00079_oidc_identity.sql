-- +goose Up
-- The SSO identity an account is bound to. See
-- sqlite/00079_oidc_identity.sql for the model: (oidc_issuer, oidc_subject)
-- is the identity, disabled_reason says why the server disabled an account,
-- the unique index cannot fail on upgrade (every issuer is NULL), and the
-- upgrade mark is written only when the database already has accounts.
ALTER TABLE users ADD COLUMN IF NOT EXISTS oidc_issuer TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_reason TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_oidc_identity ON users (provider_id, oidc_issuer, oidc_subject);
ALTER TABLE providers ADD COLUMN IF NOT EXISTS oidc_trust_email BOOLEAN NOT NULL DEFAULT FALSE;
INSERT INTO settings (setting_key, value) SELECT 'auth.oidc_trust.upgrade', 'pending' WHERE EXISTS (SELECT 1 FROM users);

-- +goose Down
DELETE FROM settings WHERE setting_key LIKE 'auth.oidc_trust.%';
ALTER TABLE providers DROP COLUMN IF EXISTS oidc_trust_email;
DROP INDEX IF EXISTS idx_users_oidc_identity;
ALTER TABLE users DROP COLUMN IF EXISTS disabled_reason;
ALTER TABLE users DROP COLUMN IF EXISTS oidc_issuer;
