-- +goose Up
-- +goose StatementBegin

-- TENANT SELF-SERVICE. The model is written out in
-- db/migrations/sqlite/00076_tenant_self_service.sql: sign-in providers as
-- rows (auth_instances) bound to tenants (provider_auth_instances), a
-- tenant's own domains (provider_domains, one tenant per domain), and the
-- operator's per-tenant switch for insecure and internal-network providers.
CREATE TABLE IF NOT EXISTS auth_instances (
    id                BIGSERIAL PRIMARY KEY,
    slug              TEXT    NOT NULL,
    driver            TEXT    NOT NULL,
    label             TEXT    NOT NULL DEFAULT '',
    origin            TEXT    NOT NULL DEFAULT 'page',
    owner_provider_id BIGINT REFERENCES providers(id) ON DELETE CASCADE,
    enabled           BOOLEAN NOT NULL DEFAULT FALSE,
    legacy            BOOLEAN NOT NULL DEFAULT FALSE,
    config_json       TEXT    NOT NULL DEFAULT '{}',
    promoted_json     TEXT    NOT NULL DEFAULT '[]',
    created_by        BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_auth_instances_slug ON auth_instances (slug);
CREATE INDEX IF NOT EXISTS idx_auth_instances_owner ON auth_instances (owner_provider_id);

CREATE TABLE IF NOT EXISTS provider_auth_instances (
    provider_id BIGINT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    instance_id BIGINT NOT NULL REFERENCES auth_instances(id) ON DELETE CASCADE,
    source      TEXT   NOT NULL DEFAULT 'explicit',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider_id, instance_id)
);
CREATE INDEX IF NOT EXISTS idx_provider_auth_instances_instance ON provider_auth_instances (instance_id);

CREATE TABLE IF NOT EXISTS provider_domains (
    id             BIGSERIAL PRIMARY KEY,
    provider_id    BIGINT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    domain         TEXT   NOT NULL,
    status         TEXT   NOT NULL DEFAULT 'pending',
    last_error     TEXT   NOT NULL DEFAULT '',
    last_error_code   TEXT NOT NULL DEFAULT '',
    last_error_params TEXT NOT NULL DEFAULT '{}',
    checked_at     TIMESTAMPTZ,
    active_since   TIMESTAMPTZ,
    tls_cert_pem   TEXT   NOT NULL DEFAULT '',
    tls_key_sealed TEXT   NOT NULL DEFAULT '',
    tls_not_after  TIMESTAMPTZ,
    created_by     BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_provider_domains_domain ON provider_domains (domain);
CREATE INDEX IF NOT EXISTS idx_provider_domains_provider ON provider_domains (provider_id);

ALTER TABLE providers ADD COLUMN IF NOT EXISTS allow_insecure_auth BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE providers DROP COLUMN IF EXISTS allow_insecure_auth;
DROP TABLE IF EXISTS provider_domains CASCADE;
DROP TABLE IF EXISTS provider_auth_instances CASCADE;
DROP TABLE IF EXISTS auth_instances CASCADE;
-- +goose StatementEnd
