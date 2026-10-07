-- +goose Up
-- +goose StatementBegin

-- THE VERSION EACH OFFICE EDITING SESSION OPENED (#184, internal/onlyoffice
-- session_base.go). The model is written out in
-- db/migrations/sqlite/00092_office_sessions.sql: one row per document key,
-- the version the driver reported when filex handed out the editing config,
-- kept in the database so a restart or a second instance still knows it. The
-- modification time is Unix nanoseconds in a BIGINT, so it comes back exactly
-- as it went in (TIMESTAMPTZ keeps microseconds).
CREATE TABLE IF NOT EXISTS office_sessions (
    id              BIGSERIAL PRIMARY KEY,
    doc_key         TEXT NOT NULL,
    node_id         BIGINT NOT NULL,
    file_size       BIGINT NOT NULL DEFAULT 0,
    file_mtime_ns   BIGINT NOT NULL DEFAULT 0,
    file_etag       TEXT NOT NULL DEFAULT '',
    version_unknown INTEGER NOT NULL DEFAULT 0,
    dropped         INTEGER NOT NULL DEFAULT 0,
    expires_unix    BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_office_sessions_key ON office_sessions (doc_key);
CREATE INDEX IF NOT EXISTS idx_office_sessions_expires ON office_sessions (expires_unix);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS office_sessions;
-- +goose StatementEnd
