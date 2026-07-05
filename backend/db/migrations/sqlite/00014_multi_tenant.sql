-- +goose Up
-- Native multi-tenancy — FOUNDATION (schema only; inert until FILEX_MULTI_TENANT=1).
--
-- A "provider" is a tenant: an auth realm (OIDC or local) bound to a host and
-- linked to one or more storages (via provider_storages). Users carry a
-- provider_id tag. This migration is purely ADDITIVE and backfills a single
-- "default" provider so every existing user has a home — single-tenant
-- behaviour is unchanged because nothing reads provider_id while multi-tenant
-- mode is off. The users email-unique swap to (provider_id, email) lands in a
-- LATER migration, together with the login/JIT code that needs it, so this one
-- stays non-destructive and portable across all three dialects.
-- See docs/MULTI-TENANCY.md.

CREATE TABLE IF NOT EXISTS providers (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    slug               TEXT NOT NULL UNIQUE,
    name               TEXT NOT NULL DEFAULT '',
    host               TEXT,
    auth_type          TEXT NOT NULL DEFAULT 'oidc',
    oidc_issuer        TEXT,
    oidc_client_id     TEXT,
    oidc_client_secret TEXT,
    oidc_redirect_url  TEXT,
    role_claim         TEXT,
    admin_group        TEXT,
    is_supertenant     INTEGER NOT NULL DEFAULT 0,
    enabled            INTEGER NOT NULL DEFAULT 1,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_providers_host ON providers(host);

CREATE TABLE IF NOT EXISTS provider_storages (
    provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    storage_id  INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    PRIMARY KEY (provider_id, storage_id)
);

ALTER TABLE users ADD COLUMN provider_id INTEGER;
ALTER TABLE users ADD COLUMN oidc_subject TEXT;
CREATE INDEX IF NOT EXISTS idx_users_provider ON users(provider_id);

-- The original org = the default tenant + platform supertenant (both inert
-- while single-tenant). Every pre-existing user joins it.
INSERT INTO providers (slug, name, is_supertenant) VALUES ('default', 'Default', 1);
UPDATE users SET provider_id = (SELECT id FROM providers WHERE slug = 'default')
    WHERE provider_id IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_users_provider;
ALTER TABLE users DROP COLUMN oidc_subject;
ALTER TABLE users DROP COLUMN provider_id;
DROP TABLE IF EXISTS provider_storages;
DROP TABLE IF EXISTS providers;
