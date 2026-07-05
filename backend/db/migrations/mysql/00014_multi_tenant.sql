-- +goose Up
-- Native multi-tenancy — FOUNDATION (schema only; inert until FILEX_MULTI_TENANT=1).
--
-- A "provider" is a tenant: an auth realm (OIDC or local) bound to a host and
-- linked to one or more storages (via provider_storages). Users carry a
-- provider_id tag. This migration is purely ADDITIVE and backfills a single
-- "default" provider so every existing user has a home — single-tenant
-- behaviour is unchanged because nothing reads provider_id while multi-tenant
-- mode is off. The users email-unique swap to (provider_id, email) lands in a
-- LATER migration, together with the login/JIT code that needs it.
-- See docs/MULTI-TENANCY.md.

CREATE TABLE IF NOT EXISTS providers (
    id                 BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    slug               VARCHAR(191) NOT NULL UNIQUE,
    name               VARCHAR(255) NOT NULL DEFAULT '',
    host               VARCHAR(255),
    auth_type          VARCHAR(16) NOT NULL DEFAULT 'oidc',
    oidc_issuer        TEXT,
    oidc_client_id     VARCHAR(255),
    oidc_client_secret TEXT,
    oidc_redirect_url  VARCHAR(512),
    role_claim         VARCHAR(255),
    admin_group        VARCHAR(255),
    is_supertenant     TINYINT(1) NOT NULL DEFAULT 0,
    enabled            TINYINT(1) NOT NULL DEFAULT 1,
    created_at         DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at         DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_providers_host (host)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS provider_storages (
    provider_id BIGINT NOT NULL,
    storage_id  BIGINT NOT NULL,
    PRIMARY KEY (provider_id, storage_id),
    CONSTRAINT fk_provider_storages_provider FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE,
    CONSTRAINT fk_provider_storages_storage  FOREIGN KEY (storage_id)  REFERENCES storages(id)  ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE users ADD COLUMN provider_id BIGINT NULL;
ALTER TABLE users ADD COLUMN oidc_subject VARCHAR(255) NULL;
ALTER TABLE users ADD KEY idx_users_provider (provider_id);

-- The original org = the default tenant + platform supertenant (both inert
-- while single-tenant). Every pre-existing user joins it.
INSERT INTO providers (slug, name, is_supertenant) VALUES ('default', 'Default', 1);
UPDATE users SET provider_id = (SELECT id FROM providers WHERE slug = 'default')
    WHERE provider_id IS NULL;

-- +goose Down
ALTER TABLE users DROP KEY idx_users_provider;
ALTER TABLE users DROP COLUMN oidc_subject;
ALTER TABLE users DROP COLUMN provider_id;
DROP TABLE IF EXISTS provider_storages;
DROP TABLE IF EXISTS providers;
