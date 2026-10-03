-- +goose Up
-- TENANT REALM. The model is written out in
-- db/migrations/sqlite/00073_provider_realm.sql: a tenant's sign-in name, set
-- once when the tenant is created and never changed, NULL for the supertenant.
-- Existing tenants get their slug of today, lower-cased.
ALTER TABLE providers ADD COLUMN IF NOT EXISTS realm TEXT;
UPDATE providers SET realm = LOWER(slug) WHERE is_supertenant = FALSE AND realm IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_providers_realm ON providers (realm);

-- +goose Down
DROP INDEX IF EXISTS idx_providers_realm;
ALTER TABLE providers DROP COLUMN IF EXISTS realm;
