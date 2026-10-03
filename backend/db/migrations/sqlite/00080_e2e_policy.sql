-- +goose Up
-- WHO MAY ENCRYPT (docs/E2E-ENCRYPTION.md → Who may encrypt,
-- docs/PERMISSIONS.md → The permissions, internal/e2epolicy).
--
-- Encrypting is CREATING a folder marker (.filex-e2e.json) where there was
-- none, or a new single encrypted file (*.fxe). Three layers must all agree:
--
-- providers.e2e_allowed  the platform's ceiling for one tenant, which only the
--                        supertenant changes. Off: nobody in that tenant
--                        encrypts anything new, administrators included.
-- providers.e2e_policy   the tenant's own choice: off | admins | permitted |
--                        approval. A single-tenant install keeps its policy in
--                        the settings table instead (e2e.policy).
--
-- and the person's files.encrypt permission on the path. Both columns start
-- where filex always was, for every row that exists and every row made later
-- (on, permitted): an upgrade changes nobody's access.
--
-- E2E_REQUESTS — under the approval policy a person asks first, and an
-- administrator of the tenant approves or rejects.
--
-- request_key    random, unique: what the store reads a new row back by.
-- provider_id    the tenant whose administrators decide. NULL on a
--                single-tenant install. A tenant that goes takes its
--                requests with it.
-- user_id        who asked (the request goes with the account). requester is
--                the name shown when they asked.
-- storage_id     where, with path: the folder that becomes encrypted (kind
--                folder), or the folder the encrypted file goes into (kind
--                file). Storage-relative, no leading slash, '' = the root.
-- kind           folder | file.
-- reason         the requester's words.
-- status         pending | approved | rejected | expired | used.
-- decided_by/decider/decided_at  who closed it and when (expired: nobody).
-- decision_note  a rejection's reason.
-- expires_at     a pending request past this becomes expired, and so does an
--                approval nobody used: seven days each (e2epolicy.ApprovalTTL).
-- used_at        when the approved encryption happened. An approval is good
--                for one.
--
-- 00080: filex v0.50.0 numbers its own migrations 00072-00079.
ALTER TABLE providers ADD COLUMN e2e_allowed INTEGER NOT NULL DEFAULT 1;
ALTER TABLE providers ADD COLUMN e2e_policy TEXT NOT NULL DEFAULT 'permitted';

CREATE TABLE IF NOT EXISTS e2e_requests (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    request_key   TEXT NOT NULL,
    provider_id   INTEGER REFERENCES providers(id) ON DELETE CASCADE,
    user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requester     TEXT NOT NULL DEFAULT '',
    storage_id    INTEGER NOT NULL REFERENCES storages(id) ON DELETE CASCADE,
    path          TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,
    reason        TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'pending',
    decided_by    INTEGER REFERENCES users(id) ON DELETE SET NULL,
    decider       TEXT NOT NULL DEFAULT '',
    decided_at    DATETIME,
    decision_note TEXT NOT NULL DEFAULT '',
    expires_at    DATETIME NOT NULL,
    used_at       DATETIME,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_e2e_requests_key ON e2e_requests (request_key);
CREATE INDEX IF NOT EXISTS idx_e2e_requests_status ON e2e_requests (status);
CREATE INDEX IF NOT EXISTS idx_e2e_requests_provider ON e2e_requests (provider_id, status);
CREATE INDEX IF NOT EXISTS idx_e2e_requests_target ON e2e_requests (user_id, storage_id, path, status);

-- +goose Down
DROP TABLE IF EXISTS e2e_requests;
ALTER TABLE providers DROP COLUMN e2e_policy;
ALTER TABLE providers DROP COLUMN e2e_allowed;
