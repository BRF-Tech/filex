-- +goose Up
-- +goose StatementBegin

-- EDITING TOGETHER: THE RELAY'S SESSIONS (#189, internal/coedit). The model
-- is written out in db/migrations/sqlite/00107_coedit.sql: sessions moved by
-- compare-and-set on `rev`, their members, the sealed log ((sid, seq)
-- unique) and the blobs' rows (the bytes are files under the data
-- directory). Times are Unix milliseconds in BIGINT; the sealed payload is
-- BYTEA.
CREATE TABLE IF NOT EXISTS coedit_sessions (
    id              BIGSERIAL PRIMARY KEY,
    sid             TEXT NOT NULL,
    doc_key         TEXT NOT NULL,
    storage_id      BIGINT NOT NULL DEFAULT 0,
    path            TEXT NOT NULL DEFAULT '',
    app             TEXT NOT NULL DEFAULT '',
    build           TEXT NOT NULL DEFAULT '',
    base_sig        TEXT NOT NULL DEFAULT '',
    saved_sig       TEXT NOT NULL DEFAULT '',
    key_mode        TEXT NOT NULL DEFAULT '',
    sealed_key      TEXT NOT NULL DEFAULT '',
    user_salt       TEXT NOT NULL DEFAULT '',
    rev             BIGINT NOT NULL DEFAULT 0,
    head            BIGINT NOT NULL DEFAULT 0,
    changes_head    BIGINT NOT NULL DEFAULT 0,
    saved_through   BIGINT NOT NULL DEFAULT 0,
    log_bytes       BIGINT NOT NULL DEFAULT 0,
    blob_bytes      BIGINT NOT NULL DEFAULT 0,
    has_base        INTEGER NOT NULL DEFAULT 0,
    members         INTEGER NOT NULL DEFAULT 0,
    next_client     BIGINT NOT NULL DEFAULT 0,
    next_index      BIGINT NOT NULL DEFAULT 0,
    lease_holder    TEXT NOT NULL DEFAULT '',
    lease_until_ms  BIGINT NOT NULL DEFAULT 0,
    created_ms      BIGINT NOT NULL DEFAULT 0,
    active_ms       BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_sessions_sid ON coedit_sessions (sid);
CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_sessions_doc ON coedit_sessions (doc_key);
CREATE INDEX IF NOT EXISTS idx_coedit_sessions_active ON coedit_sessions (members, active_ms);

CREATE TABLE IF NOT EXISTS coedit_members (
    id          BIGSERIAL PRIMARY KEY,
    sid         TEXT NOT NULL,
    client      TEXT NOT NULL,
    token_hash  TEXT NOT NULL DEFAULT '',
    user_id     BIGINT NOT NULL DEFAULT 0,
    user_ref    TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL DEFAULT '',
    index_user  BIGINT NOT NULL DEFAULT 0,
    can_edit    INTEGER NOT NULL DEFAULT 0,
    join_seq    BIGINT NOT NULL DEFAULT 0,
    last_ctr    BIGINT NOT NULL DEFAULT 0,
    seen_ms     BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_members_client ON coedit_members (sid, client);
CREATE INDEX IF NOT EXISTS idx_coedit_members_seen ON coedit_members (seen_ms);

CREATE TABLE IF NOT EXISTS coedit_entries (
    id          BIGSERIAL PRIMARY KEY,
    sid         TEXT NOT NULL,
    seq         BIGINT NOT NULL,
    kind        TEXT NOT NULL DEFAULT '',
    client      TEXT NOT NULL DEFAULT '',
    at_ms       BIGINT NOT NULL DEFAULT 0,
    ctr         BIGINT NOT NULL DEFAULT 0,
    ct          BYTEA,
    member      TEXT NOT NULL DEFAULT '',
    through     BIGINT NOT NULL DEFAULT 0,
    sig         TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_entries_seq ON coedit_entries (sid, seq);

CREATE TABLE IF NOT EXISTS coedit_blobs (
    id          BIGSERIAL PRIMARY KEY,
    sid         TEXT NOT NULL,
    name        TEXT NOT NULL,
    size        BIGINT NOT NULL DEFAULT 0,
    sha256      TEXT NOT NULL DEFAULT '',
    created_ms  BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_blobs_name ON coedit_blobs (sid, name);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS coedit_blobs;
DROP TABLE IF EXISTS coedit_entries;
DROP TABLE IF EXISTS coedit_members;
DROP TABLE IF EXISTS coedit_sessions;
-- +goose StatementEnd
