-- +goose Up

-- TENANT SELF-SERVICE. The model is written out in
-- db/migrations/sqlite/00076_tenant_self_service.sql: sign-in providers as
-- rows (auth_instances) bound to tenants (provider_auth_instances), a
-- tenant's own domains (provider_domains, one tenant per domain), and the
-- operator's per-tenant switch for insecure and internal-network providers.
--
-- VARCHAR where a column is indexed (MySQL cannot index TEXT without a prefix
-- length): a slug as wide as the providers slug, a domain name at most 253
-- characters. Expression defaults ('') on the TEXT columns, so they are NOT
-- NULL with a default on every engine (TestSchemaParityAcrossEngines).
CREATE TABLE IF NOT EXISTS auth_instances (
    id                BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    slug              VARCHAR(191) NOT NULL,
    driver            VARCHAR(32)  NOT NULL,
    label             VARCHAR(255) NOT NULL DEFAULT '',
    origin            VARCHAR(16)  NOT NULL DEFAULT 'page',
    owner_provider_id BIGINT NULL,
    enabled           TINYINT(1) NOT NULL DEFAULT 0,
    legacy            TINYINT(1) NOT NULL DEFAULT 0,
    config_json       TEXT NOT NULL DEFAULT ('{}'),
    promoted_json     TEXT NOT NULL DEFAULT ('[]'),
    created_by        BIGINT NULL,
    created_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY idx_auth_instances_slug (slug),
    INDEX idx_auth_instances_owner (owner_provider_id),
    CONSTRAINT fk_auth_instances_owner FOREIGN KEY (owner_provider_id) REFERENCES providers(id) ON DELETE CASCADE,
    CONSTRAINT fk_auth_instances_created_by FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS provider_auth_instances (
    provider_id BIGINT NOT NULL,
    instance_id BIGINT NOT NULL,
    source      VARCHAR(16) NOT NULL DEFAULT 'explicit',
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (provider_id, instance_id),
    INDEX idx_provider_auth_instances_instance (instance_id),
    CONSTRAINT fk_provider_auth_instances_provider FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE,
    CONSTRAINT fk_provider_auth_instances_instance FOREIGN KEY (instance_id) REFERENCES auth_instances(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS provider_domains (
    id             BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    provider_id    BIGINT NOT NULL,
    domain         VARCHAR(253) NOT NULL,
    status         VARCHAR(16)  NOT NULL DEFAULT 'pending',
    last_error     VARCHAR(1000) NOT NULL DEFAULT '',
    last_error_code   VARCHAR(64)   NOT NULL DEFAULT '',
    last_error_params VARCHAR(2000) NOT NULL DEFAULT '{}',
    checked_at     DATETIME(6) NULL,
    active_since   DATETIME(6) NULL,
    tls_cert_pem   MEDIUMTEXT NOT NULL DEFAULT (''),
    tls_key_sealed TEXT NOT NULL DEFAULT (''),
    tls_not_after  DATETIME(6) NULL,
    created_by     BIGINT NULL,
    created_at     DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY idx_provider_domains_domain (domain),
    INDEX idx_provider_domains_provider (provider_id),
    CONSTRAINT fk_provider_domains_provider FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE,
    CONSTRAINT fk_provider_domains_created_by FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE providers ADD COLUMN allow_insecure_auth TINYINT(1) NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE providers DROP COLUMN allow_insecure_auth;
DROP TABLE IF EXISTS provider_domains;
DROP TABLE IF EXISTS provider_auth_instances;
DROP TABLE IF EXISTS auth_instances;
