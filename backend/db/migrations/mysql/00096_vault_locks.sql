-- +goose Up
-- +goose StatementBegin

-- THE VAULT'S WRITE LOCK (#94, internal/vaultlock, docs/E2E-VAULT-FORMAT.md
-- → The write lock). The model is written out in
-- db/migrations/sqlite/00096_vault_locks.sql: one row per tenant and vault
-- id, compare-and-set on `rev`, the token kept only as its SHA-256, every
-- time Unix milliseconds in a BIGINT.
--
-- ⚠ The text columns are VARCHAR: MySQL cannot index a TEXT column without a
-- prefix length (vault_id is in the unique key), and a TEXT column takes no
-- plain DEFAULT. Each table and its indexes are ONE statement, in a block of
-- its own: MySQL DDL is not transactional, and the driver runs one
-- statement per block.
CREATE TABLE IF NOT EXISTS vault_locks (
    id                BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    vault_id          VARCHAR(32) NOT NULL,
    rev               BIGINT NOT NULL DEFAULT 0,
    token_hash        VARCHAR(64) NOT NULL DEFAULT '',
    holder_user_id    BIGINT NOT NULL DEFAULT 0,
    holder_name       VARCHAR(255) NOT NULL DEFAULT '',
    holder_client     VARCHAR(16) NOT NULL DEFAULT '',
    holder_label      VARCHAR(255) NOT NULL DEFAULT '',
    storage_id        BIGINT NOT NULL DEFAULT 0,
    path              VARCHAR(2048) NOT NULL DEFAULT '',
    taken_ms          BIGINT NOT NULL DEFAULT 0,
    lease_ms          BIGINT NOT NULL DEFAULT 0,
    active_ms         BIGINT NOT NULL DEFAULT 0,
    idle_seconds      BIGINT NOT NULL DEFAULT 0,
    index_started_ms  BIGINT NOT NULL DEFAULT 0,
    first_gen         BIGINT NOT NULL DEFAULT 0,
    last_gen          BIGINT NOT NULL DEFAULT 0,
    ended_token_hash  VARCHAR(64) NOT NULL DEFAULT '',
    ended_reason      VARCHAR(16) NOT NULL DEFAULT '',
    ended_by          VARCHAR(255) NOT NULL DEFAULT '',
    ended_ms          BIGINT NOT NULL DEFAULT 0,
    created_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_vault_locks_key (tenant_id, vault_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose StatementBegin
-- Each person's idle time, 1 to 10 minutes, 3 until set.
CREATE TABLE IF NOT EXISTS vault_prefs (
    user_id      BIGINT NOT NULL PRIMARY KEY,
    idle_minutes INT NOT NULL DEFAULT 3,
    updated_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vault_prefs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS vault_locks;
-- +goose StatementEnd
