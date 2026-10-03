-- +goose Up
-- TENANT REALM (docs/MULTI-TENANCY.md, Realms).
--
-- A realm is a tenant's sign-in name: the Realm field of the sign-in form, and
-- `realm/name` in the user name of a protocol that carries no address (SFTP).
-- It tells two tenants' `alex` apart, and it is part of the e-mail address an
-- account gets from a provider that knows only a login name (alex@acme.local).
--
-- realm   the tenant's realm, lower case. Chosen when the tenant is created
--         (the slug by default) and never changed afterwards: the store's
--         UpdateProvider does not write it. NULL for the platform's own tenant
--         (the supertenant), which is signed in to with an empty realm.
--
-- Existing tenants get their slug of today, lower-cased. The unique index
-- allows any number of NULLs on every engine.
ALTER TABLE providers ADD COLUMN realm TEXT;
UPDATE providers SET realm = LOWER(slug) WHERE is_supertenant = 0 AND realm IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_providers_realm ON providers (realm);

-- +goose Down
DROP INDEX IF EXISTS idx_providers_realm;
ALTER TABLE providers DROP COLUMN realm;
