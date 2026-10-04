-- +goose Up
-- WHO MAY ENCRYPT (docs/E2E-ENCRYPTION.md → Who may encrypt,
-- docs/PERMISSIONS.md → The permissions, internal/e2epolicy). The model is
-- written out in db/migrations/sqlite/00080_e2e_policy.sql.
--
-- providers.e2e_allowed is the platform's ceiling for one tenant (off: nobody
-- there encrypts anything new, administrators included), providers.e2e_policy
-- the tenant's own choice (off | admins | permitted | approval). Both start
-- where filex always was, for every row: an upgrade changes nobody's access.
-- e2e_requests holds what a person asks for under the approval policy.
--
-- ⚠ path is compared exactly, byte for byte: utf8mb4_0900_bin, the NO PAD
-- binary collation, as 00041 chose for nodes.path and file_grants.path_prefix.
-- An approval is found by its exact path, and under the default accent- and
-- case-insensitive collation an approval for "Muhasebe" would also be one for
-- "muhasebe" and "MUHASEBE". Not utf8mb4_bin: that binary collation is PAD
-- SPACE, so the approval would still be one for "Muhasebe " (a trailing
-- space), which SQLite and PostgreSQL never match. Only a 512-character
-- prefix is indexed (InnoDB's key length limit under utf8mb4), the engine
-- re-checks the rest. request_key is VARCHAR because MySQL indexes a bounded
-- column only.
--
-- 00080: filex v0.50.0 numbers its own migrations 00072-00079.
ALTER TABLE providers ADD COLUMN e2e_allowed TINYINT(1) NOT NULL DEFAULT 1;
ALTER TABLE providers ADD COLUMN e2e_policy VARCHAR(16) NOT NULL DEFAULT 'permitted';

CREATE TABLE IF NOT EXISTS e2e_requests (
    id            BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    request_key   VARCHAR(64) NOT NULL,
    provider_id   BIGINT NULL,
    user_id       BIGINT NOT NULL,
    requester     VARCHAR(255) NOT NULL DEFAULT '',
    storage_id    BIGINT NOT NULL,
    path          VARCHAR(2048) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL DEFAULT '',
    kind          VARCHAR(16) NOT NULL,
    reason        TEXT NOT NULL DEFAULT (''),
    status        VARCHAR(16) NOT NULL DEFAULT 'pending',
    decided_by    BIGINT NULL,
    decider       VARCHAR(255) NOT NULL DEFAULT '',
    decided_at    DATETIME(6) NULL,
    decision_note TEXT NOT NULL DEFAULT (''),
    expires_at    DATETIME(6) NOT NULL,
    used_at       DATETIME(6) NULL,
    created_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_e2e_requests_key (request_key),
    INDEX idx_e2e_requests_status (status),
    INDEX idx_e2e_requests_provider (provider_id, status),
    INDEX idx_e2e_requests_target (user_id, storage_id, path(512), status),
    CONSTRAINT fk_e2e_requests_provider FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE,
    CONSTRAINT fk_e2e_requests_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT fk_e2e_requests_storage FOREIGN KEY (storage_id) REFERENCES storages(id) ON DELETE CASCADE,
    CONSTRAINT fk_e2e_requests_decided_by FOREIGN KEY (decided_by) REFERENCES users(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- A SETTINGS KEY IS COMPARED BYTE FOR BYTE, ON MYSQL TOO.
--
-- settings.setting_key was made with utf8mb4_0900_ai_ci (00001), which is
-- case- AND accent-insensitive, and 00041 left setting keys on it as harmless.
-- They are not: the settings API guards ONE key, e2e.policy, by comparing it
-- exactly, and on MySQL a request that spelled it E2E.POLICY or e2é.policy
-- passed that guard (no session asked for, no validator, no audit row of the
-- policy) and still read and wrote the e2e.policy row, so it changed who may
-- encrypt. SQLite and PostgreSQL compare keys exactly. This makes MySQL do the
-- same with utf8mb4_0900_bin, the NO PAD binary collation 00041 chose for names
-- and paths.
--
-- Only the collation changes: the type, the length, NOT NULL and the primary key
-- stay as 00001 and 00035 left them. It cannot fail on existing data, because
-- binary is stricter than ai_ci: keys that were unique ignoring case and accent
-- are unique byte for byte.
ALTER TABLE settings MODIFY setting_key VARCHAR(190) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL;

-- +goose Down
-- ⚠ The first statement fails with a duplicate-key error once two setting keys
-- that differ only by case or accent exist, which is exactly what Up made
-- possible. Rename or remove one of each pair first. It runs first so that a
-- failure leaves the rest of Down undone too.
ALTER TABLE settings MODIFY setting_key VARCHAR(190) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci NOT NULL;
DROP TABLE IF EXISTS e2e_requests;
ALTER TABLE providers DROP COLUMN e2e_policy;
ALTER TABLE providers DROP COLUMN e2e_allowed;
