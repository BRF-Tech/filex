-- +goose Up
-- +goose StatementBegin

-- TENANT SELF-SERVICE (docs/TENANT-ADMIN.md): sign-in providers as rows that
-- are bound to tenants, and a tenant's own domains.
--
--   auth_instances           one configured sign-in provider. Until now a
--                            provider was its driver (`auth.ldap.*` settings
--                            meant "the LDAP"); two tenants with two
--                            directories need two LDAPs. slug is the stable
--                            name: the first instance of each driver keeps the
--                            driver's name, so /api/admin/auth-providers/ldap
--                            addresses what it did. origin: environment (built
--                            from FILEX_AUTH_DRIVERS / the config file; the row
--                            only carries its bindings), page (the operator
--                            made it), tenant (a tenant's administrator made
--                            it; owner_provider_id says whose, and it is bound
--                            to that tenant only). config_json holds the
--                            driver's fields, secrets sealed with
--                            FILEX_SECRET_KEY. promoted_json lists the scopes
--                            (tenant ids, 0 = the platform's own) in which an
--                            operating-system instance already promoted its
--                            test account: the first switch-on in a scope
--                            promotes, no later one does.
--   provider_auth_instances  which tenant signs in through which instance
--                            (1-1 and 1-n). source: explicit, upgrade (the
--                            one-time boot step that binds every instance that
--                            existed to every tenant that existed), pin (a
--                            0.50 FILEX_LDAP_PROVIDER / FILEX_HEADER_PROVIDER
--                            pin, read as an implicit binding).
--   provider_domains         a tenant's own domains, proven by a CNAME to the
--                            tenant's platform subdomain. domain is UNIQUE: one
--                            tenant at a time, whatever the state. status:
--                            pending (the CNAME was not seen yet), active,
--                            suspended (it was seen and is gone; the row stays).
--                            tls_cert_pem / tls_key_sealed: a certificate the
--                            tenant brought, the key sealed.
--   providers.allow_insecure_auth
--                            the platform operator's per-tenant switch: that
--                            tenant's own providers may reach an internal
--                            address and plain ldap://. Off by default.
--
-- Purely additive: nothing reads these until the boot step has filled them,
-- and an install that never turns multi-tenancy on signs in as before.
CREATE TABLE IF NOT EXISTS auth_instances (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    slug              TEXT    NOT NULL,
    driver            TEXT    NOT NULL,
    label             TEXT    NOT NULL DEFAULT '',
    origin            TEXT    NOT NULL DEFAULT 'page',
    owner_provider_id INTEGER REFERENCES providers(id) ON DELETE CASCADE,
    enabled           INTEGER NOT NULL DEFAULT 0,
    legacy            INTEGER NOT NULL DEFAULT 0,
    config_json       TEXT    NOT NULL DEFAULT '{}',
    promoted_json     TEXT    NOT NULL DEFAULT '[]',
    created_by        INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_auth_instances_slug ON auth_instances (slug);
CREATE INDEX IF NOT EXISTS idx_auth_instances_owner ON auth_instances (owner_provider_id);

CREATE TABLE IF NOT EXISTS provider_auth_instances (
    provider_id INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    instance_id INTEGER NOT NULL REFERENCES auth_instances(id) ON DELETE CASCADE,
    source      TEXT    NOT NULL DEFAULT 'explicit',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (provider_id, instance_id)
);
CREATE INDEX IF NOT EXISTS idx_provider_auth_instances_instance ON provider_auth_instances (instance_id);

CREATE TABLE IF NOT EXISTS provider_domains (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id    INTEGER NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    domain         TEXT    NOT NULL,
    status         TEXT    NOT NULL DEFAULT 'pending',
    last_error     TEXT    NOT NULL DEFAULT '',
    last_error_code   TEXT NOT NULL DEFAULT '',
    last_error_params TEXT NOT NULL DEFAULT '{}',
    checked_at     DATETIME,
    active_since   DATETIME,
    tls_cert_pem   TEXT    NOT NULL DEFAULT '',
    tls_key_sealed TEXT    NOT NULL DEFAULT '',
    tls_not_after  DATETIME,
    created_by     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_domains_domain ON provider_domains (domain);
CREATE INDEX IF NOT EXISTS idx_provider_domains_provider ON provider_domains (provider_id);

ALTER TABLE providers ADD COLUMN allow_insecure_auth INTEGER NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE providers DROP COLUMN allow_insecure_auth;
DROP TABLE IF EXISTS provider_domains;
DROP TABLE IF EXISTS provider_auth_instances;
DROP TABLE IF EXISTS auth_instances;
-- +goose StatementEnd
