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
-- 00080: filex v0.50.0 numbers its own migrations 00072-00079.
ALTER TABLE providers ADD COLUMN IF NOT EXISTS e2e_allowed BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE providers ADD COLUMN IF NOT EXISTS e2e_policy TEXT NOT NULL DEFAULT 'permitted';

CREATE TABLE IF NOT EXISTS e2e_requests (
    id            BIGSERIAL PRIMARY KEY,
    request_key   TEXT NOT NULL,
    provider_id   BIGINT REFERENCES providers(id) ON DELETE CASCADE,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requester     TEXT NOT NULL DEFAULT '',
    storage_id    BIGINT NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path          TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'pending',
    decided_by    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    decider       TEXT NOT NULL DEFAULT '',
    decided_at    TIMESTAMPTZ,
    decision_note TEXT NOT NULL DEFAULT '',
    expires_at    TIMESTAMPTZ NOT NULL,
    used_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_e2e_requests_key ON e2e_requests (request_key);
CREATE INDEX IF NOT EXISTS idx_e2e_requests_status ON e2e_requests (status);
CREATE INDEX IF NOT EXISTS idx_e2e_requests_provider ON e2e_requests (provider_id, status);
CREATE INDEX IF NOT EXISTS idx_e2e_requests_target ON e2e_requests (user_id, storage_id, path, status);

-- +goose Down
DROP TABLE IF EXISTS e2e_requests CASCADE;
ALTER TABLE providers DROP COLUMN IF EXISTS e2e_policy;
ALTER TABLE providers DROP COLUMN IF EXISTS e2e_allowed;
