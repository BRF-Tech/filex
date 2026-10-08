-- +goose Up
-- +goose StatementBegin

-- THE VAULT'S WRITE LOCK (#94, internal/vaultlock, docs/E2E-VAULT-FORMAT.md
-- → The write lock).
--
-- A vault (encryption level 3) has one writer at a time. The lock is kept
-- HERE, not in process memory or in a file on the storage: filex can run
-- more than one process on one database, and every storage driver (S3, SFTP,
-- WebDAV, SMB, FTP) is supported without a conditional create.
--
-- One row per tenant and vault id (the key file's vault.id, 32 lower-case hex
-- digits). The row outlives the locks taken on it: it says who held the vault
-- last and how that ended, which is what the holder of a lost lock is told.
-- Taking and renewing are compare-and-set updates on `rev`.
--
-- tenant_id         the vault's tenant (the provider its storage is linked
--                   to; 0 on a single-tenant install).
-- vault_id          the vault's id, hex.
-- rev               the compare-and-set counter.
-- token_hash        hex SHA-256 of the live lock's token; '' when no lock is
--                   recorded as held. The token itself is never stored.
-- holder_*          the person (id, display name), the client (web, desktop,
--                   cli, mount) and the client's own label.
-- storage_id, path  where the lock was taken.
-- *_ms              Unix MILLISECONDS, integers on every engine: taken, lease
--                   end, last activity, the running index write's start
--                   (0 = none), and when the last lock ended.
-- idle_seconds      the holder's idle time, read when the lock was taken.
-- first_gen,
-- last_gen          the generations committed under the lock.
-- ended_*           how the last lock ended (released, expired, idle,
--                   broken, locked_idle), its token's hash, and who
--                   broke it (ended_by, the name the lost holder is told).
CREATE TABLE IF NOT EXISTS vault_locks (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id         INTEGER NOT NULL DEFAULT 0,
    vault_id          TEXT NOT NULL,
    rev               INTEGER NOT NULL DEFAULT 0,
    token_hash        TEXT NOT NULL DEFAULT '',
    holder_user_id    INTEGER NOT NULL DEFAULT 0,
    holder_name       TEXT NOT NULL DEFAULT '',
    holder_client     TEXT NOT NULL DEFAULT '',
    holder_label      TEXT NOT NULL DEFAULT '',
    storage_id        INTEGER NOT NULL DEFAULT 0,
    path              TEXT NOT NULL DEFAULT '',
    taken_ms          INTEGER NOT NULL DEFAULT 0,
    lease_ms          INTEGER NOT NULL DEFAULT 0,
    active_ms         INTEGER NOT NULL DEFAULT 0,
    idle_seconds      INTEGER NOT NULL DEFAULT 0,
    index_started_ms  INTEGER NOT NULL DEFAULT 0,
    first_gen         INTEGER NOT NULL DEFAULT 0,
    last_gen          INTEGER NOT NULL DEFAULT 0,
    ended_token_hash  TEXT NOT NULL DEFAULT '',
    ended_reason      TEXT NOT NULL DEFAULT '',
    ended_by          TEXT NOT NULL DEFAULT '',
    ended_ms          INTEGER NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_vault_locks_key ON vault_locks (tenant_id, vault_id);

-- A PERSON'S IDLE TIME (docs/E2E-VAULT-FORMAT.md → Lock semantics): how long
-- a vault writer may do nothing before its write lock ends, 1 to 10 minutes,
-- 3 until the person sets it. Kept for the person, not per browser or device;
-- the server reads it when a lock is taken, the client never sends it.
CREATE TABLE IF NOT EXISTS vault_prefs (
    user_id      INTEGER PRIMARY KEY,
    idle_minutes INTEGER NOT NULL DEFAULT 3,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS vault_prefs;
DROP TABLE IF EXISTS vault_locks;
-- +goose StatementEnd
