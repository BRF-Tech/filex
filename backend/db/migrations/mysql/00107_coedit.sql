-- +goose Up
-- +goose StatementBegin

-- EDITING TOGETHER: THE RELAY'S SESSIONS (#189, internal/coedit). The model
-- is written out in db/migrations/sqlite/00107_coedit.sql.
--
-- ⚠ Indexed columns are VARCHAR: MySQL cannot index a TEXT column without a
-- prefix length, and a prefix index does not make a key unique. sid and
-- doc_key are 32 / 64 hex characters, a client "c<n>", a blob name at most
-- 128 characters. The sealed payload is LONGBLOB (an entry is at most a few
-- MiB). Each table and its indexes are ONE statement: MySQL DDL is not
-- transactional.
CREATE TABLE IF NOT EXISTS coedit_sessions (
    id              BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    sid             VARCHAR(64) NOT NULL,
    doc_key         VARCHAR(64) NOT NULL,
    storage_id      BIGINT NOT NULL DEFAULT 0,
    path            VARCHAR(2048) NOT NULL DEFAULT '',
    app             VARCHAR(128) NOT NULL DEFAULT '',
    build           VARCHAR(190) NOT NULL DEFAULT '',
    base_sig        VARCHAR(255) NOT NULL DEFAULT '',
    saved_sig       VARCHAR(255) NOT NULL DEFAULT '',
    key_mode        VARCHAR(16) NOT NULL DEFAULT '',
    sealed_key      TEXT NOT NULL DEFAULT (''),
    user_salt       VARCHAR(64) NOT NULL DEFAULT '',
    rev             BIGINT NOT NULL DEFAULT 0,
    head            BIGINT NOT NULL DEFAULT 0,
    changes_head    BIGINT NOT NULL DEFAULT 0,
    saved_through   BIGINT NOT NULL DEFAULT 0,
    log_bytes       BIGINT NOT NULL DEFAULT 0,
    blob_bytes      BIGINT NOT NULL DEFAULT 0,
    has_base        INT NOT NULL DEFAULT 0,
    members         INT NOT NULL DEFAULT 0,
    next_client     BIGINT NOT NULL DEFAULT 0,
    next_index      BIGINT NOT NULL DEFAULT 0,
    lease_holder    VARCHAR(64) NOT NULL DEFAULT '',
    lease_until_ms  BIGINT NOT NULL DEFAULT 0,
    created_ms      BIGINT NOT NULL DEFAULT 0,
    active_ms       BIGINT NOT NULL DEFAULT 0,
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_coedit_sessions_sid (sid),
    UNIQUE INDEX idx_coedit_sessions_doc (doc_key),
    INDEX idx_coedit_sessions_active (members, active_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS coedit_members (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    sid         VARCHAR(64) NOT NULL,
    client      VARCHAR(64) NOT NULL,
    token_hash  VARCHAR(64) NOT NULL DEFAULT '',
    user_id     BIGINT NOT NULL DEFAULT 0,
    user_ref    VARCHAR(64) NOT NULL DEFAULT '',
    name        VARCHAR(255) NOT NULL DEFAULT '',
    index_user  BIGINT NOT NULL DEFAULT 0,
    can_edit    INT NOT NULL DEFAULT 0,
    join_seq    BIGINT NOT NULL DEFAULT 0,
    last_ctr    BIGINT NOT NULL DEFAULT 0,
    seen_ms     BIGINT NOT NULL DEFAULT 0,
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_coedit_members_client (sid, client),
    INDEX idx_coedit_members_seen (seen_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS coedit_entries (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    sid         VARCHAR(64) NOT NULL,
    seq         BIGINT NOT NULL,
    kind        VARCHAR(16) NOT NULL DEFAULT '',
    client      VARCHAR(64) NOT NULL DEFAULT '',
    at_ms       BIGINT NOT NULL DEFAULT 0,
    ctr         BIGINT NOT NULL DEFAULT 0,
    ct          LONGBLOB,
    member      TEXT NOT NULL DEFAULT (''),
    through     BIGINT NOT NULL DEFAULT 0,
    sig         VARCHAR(255) NOT NULL DEFAULT '',
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_coedit_entries_seq (sid, seq)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS coedit_blobs (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    sid         VARCHAR(64) NOT NULL,
    name        VARCHAR(128) NOT NULL,
    size        BIGINT NOT NULL DEFAULT 0,
    sha256      VARCHAR(64) NOT NULL DEFAULT '',
    created_ms  BIGINT NOT NULL DEFAULT 0,
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_coedit_blobs_name (sid, name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS coedit_blobs;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS coedit_entries;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS coedit_members;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS coedit_sessions;
-- +goose StatementEnd
