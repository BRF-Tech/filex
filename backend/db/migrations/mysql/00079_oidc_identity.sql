-- +goose Up
-- The SSO identity an account is bound to. See
-- sqlite/00079_oidc_identity.sql for the model: (oidc_issuer, oidc_subject)
-- is the identity, disabled_reason says why the server disabled an account,
-- the unique index cannot fail on upgrade (every issuer is NULL), and the
-- upgrade mark is written only when the database already has accounts.
--
-- MySQL only: the table's collation (utf8mb4_0900_ai_ci) compares without
-- case or accents, and an identity provider's `sub` is case-sensitive, so the
-- identity columns compare byte for byte (utf8mb4_bin). The index stays under
-- InnoDB's 3072-byte key limit: 8 + 4*500 + 4*255.
ALTER TABLE users MODIFY oidc_subject VARCHAR(255) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL;
ALTER TABLE users ADD COLUMN oidc_issuer VARCHAR(500) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL, ADD COLUMN disabled_reason VARCHAR(64) NULL;
CREATE UNIQUE INDEX idx_users_oidc_identity ON users (provider_id, oidc_issuer, oidc_subject);
ALTER TABLE providers ADD COLUMN oidc_trust_email TINYINT(1) NOT NULL DEFAULT 0;
INSERT INTO settings (setting_key, value) SELECT 'auth.oidc_trust.upgrade', 'pending' FROM DUAL WHERE EXISTS (SELECT 1 FROM users);

-- +goose Down
DELETE FROM settings WHERE setting_key LIKE 'auth.oidc_trust.%';
ALTER TABLE providers DROP COLUMN oidc_trust_email;
DROP INDEX idx_users_oidc_identity ON users;
ALTER TABLE users DROP COLUMN disabled_reason, DROP COLUMN oidc_issuer;
ALTER TABLE users MODIFY oidc_subject VARCHAR(255) NULL;
