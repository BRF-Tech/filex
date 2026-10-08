-- +goose Up
-- +goose StatementBegin

-- THE VAULT'S WRITE LOCK (#94, internal/vaultlock, docs/E2E-VAULT-FORMAT.md
-- → The write lock). The model is written out in
-- db/migrations/sqlite/00096_vault_locks.sql: one row per tenant and vault
-- id, compare-and-set on `rev`, the token kept only as its SHA-256, every
-- time Unix milliseconds in a BIGINT. And each person's idle time, 1 to 10
-- minutes, 3 until set.
CREATE TABLE IF NOT EXISTS vault_locks (
    id                BIGSERIAL PRIMARY KEY,
    tenant_id         BIGINT NOT NULL DEFAULT 0,
    vault_id          TEXT NOT NULL,
    rev               BIGINT NOT NULL DEFAULT 0,
    token_hash        TEXT NOT NULL DEFAULT '',
    holder_user_id    BIGINT NOT NULL DEFAULT 0,
    holder_name       TEXT NOT NULL DEFAULT '',
    holder_client     TEXT NOT NULL DEFAULT '',
    holder_label      TEXT NOT NULL DEFAULT '',
    storage_id        BIGINT NOT NULL DEFAULT 0,
    path              TEXT NOT NULL DEFAULT '',
    taken_ms          BIGINT NOT NULL DEFAULT 0,
    lease_ms          BIGINT NOT NULL DEFAULT 0,
    active_ms         BIGINT NOT NULL DEFAULT 0,
    idle_seconds      BIGINT NOT NULL DEFAULT 0,
    index_started_ms  BIGINT NOT NULL DEFAULT 0,
    first_gen         BIGINT NOT NULL DEFAULT 0,
    last_gen          BIGINT NOT NULL DEFAULT 0,
    ended_token_hash  TEXT NOT NULL DEFAULT '',
    ended_reason      TEXT NOT NULL DEFAULT '',
    ended_by          TEXT NOT NULL DEFAULT '',
    ended_ms          BIGINT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_vault_locks_key ON vault_locks (tenant_id, vault_id);

CREATE TABLE IF NOT EXISTS vault_prefs (
    user_id      BIGINT PRIMARY KEY,
    idle_minutes INTEGER NOT NULL DEFAULT 3,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vault_prefs;
DROP TABLE IF EXISTS vault_locks;
-- +goose StatementEnd
