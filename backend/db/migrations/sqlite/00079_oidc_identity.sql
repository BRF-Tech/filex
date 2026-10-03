-- +goose Up
-- THE SSO IDENTITY AN ACCOUNT IS BOUND TO (filex 0.50, docs/SSO.md "Which
-- account an SSO sign-in opens").
--
-- An OIDC sign-in used to find its account by the e-mail address the identity
-- provider sent, and nothing else: the `sub` it kept (00014) was never
-- compared, and `email_verified` was never read. Now the first sign-in that
-- matches an account binds it to the identity that signed in, the pair
-- (issuer, sub), and every later sign-in finds it by that pair; the same
-- address arriving with another identity is refused.
--
--   oidc_issuer      the `iss` of the identity the account is bound to, NULL
--                    for none. oidc_subject (00014) is its `sub`. An account
--                    that kept a subject before this release and no issuer
--                    gets its issuer at its next sign-in with that subject.
--   disabled_reason  why the account is disabled when the server, not an
--                    administrator, did it: `pending_approval` for an account
--                    opened by an SSO sign-in whose address the identity
--                    provider did not confirm. Cleared whenever an
--                    administrator switches the account on or off.
--
-- One identity, one account per tenant: the unique index. The issuer is new,
-- so every existing row has NULL there, and NULLs never collide on any engine:
-- an upgrade cannot fail on it, whatever subjects existing accounts share.
--
-- providers.oidc_trust_email is a tenant's own OIDC's "trust this provider's
-- email addresses" (`trust_email` of every other OIDC): an address it sends
-- counts as verified whatever its `email_verified` says. Off for a provider
-- made from now on.
--
-- An UPGRADE keeps the behaviour of the providers that already exist: every
-- one of them gets trust ON (the owner's decision, 2026-10-02: nobody is
-- locked out by the upgrade) and the page says that the upgrade set it. That
-- is done once, at the first start, by authsetup.UpgradeOIDCTrust, which reads
-- the mark below: written only when the database already has accounts, i.e.
-- never on a fresh install.
ALTER TABLE users ADD COLUMN oidc_issuer TEXT;
ALTER TABLE users ADD COLUMN disabled_reason TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_oidc_identity ON users (provider_id, oidc_issuer, oidc_subject);
ALTER TABLE providers ADD COLUMN oidc_trust_email INTEGER NOT NULL DEFAULT 0;
INSERT INTO settings (setting_key, value) SELECT 'auth.oidc_trust.upgrade', 'pending' WHERE EXISTS (SELECT 1 FROM users);

-- +goose Down
DELETE FROM settings WHERE setting_key LIKE 'auth.oidc_trust.%';
ALTER TABLE providers DROP COLUMN oidc_trust_email;
DROP INDEX IF EXISTS idx_users_oidc_identity;
ALTER TABLE users DROP COLUMN disabled_reason;
ALTER TABLE users DROP COLUMN oidc_issuer;
