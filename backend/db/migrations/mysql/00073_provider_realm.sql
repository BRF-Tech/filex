-- +goose Up
-- TENANT REALM. The model is written out in
-- db/migrations/sqlite/00073_provider_realm.sql: a tenant's sign-in name, set
-- once when the tenant is created and never changed, NULL for the supertenant.
-- Existing tenants get their slug of today, lower-cased.
--
-- VARCHAR(191) and not TEXT: MySQL cannot index a TEXT column without a
-- prefix length, and 191 is the slug column's own width, so every existing
-- slug fits.
ALTER TABLE providers ADD COLUMN realm VARCHAR(191) NULL;
UPDATE providers SET realm = LOWER(slug) WHERE is_supertenant = 0 AND realm IS NULL;
CREATE UNIQUE INDEX idx_providers_realm ON providers (realm);

-- +goose Down
DROP INDEX idx_providers_realm ON providers;
ALTER TABLE providers DROP COLUMN realm;
