-- +goose Up
-- +goose StatementBegin

-- THE VERSION EACH OFFICE EDITING SESSION OPENED (#184, internal/onlyoffice
-- session_base.go). The model is written out in
-- db/migrations/sqlite/00092_office_sessions.sql: one row per document key,
-- the version the driver reported when filex handed out the editing config,
-- kept in the database so a restart or a second instance still knows it.
--
-- ⚠ doc_key is VARCHAR: MySQL cannot index a TEXT column without a prefix
-- length, and a prefix index does not make a key unique. filex's own keys are
-- 32 hex characters; 190 leaves room and fits utf8mb4's index size. The
-- table and its indexes are ONE statement: MySQL DDL is not transactional.
CREATE TABLE IF NOT EXISTS office_sessions (
    id              BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    doc_key         VARCHAR(190) NOT NULL,
    node_id         BIGINT NOT NULL,
    file_size       BIGINT NOT NULL DEFAULT 0,
    file_mtime_ns   BIGINT NOT NULL DEFAULT 0,
    file_etag       VARCHAR(255) NOT NULL DEFAULT '',
    version_unknown INT NOT NULL DEFAULT 0,
    dropped         INT NOT NULL DEFAULT 0,
    expires_unix    BIGINT NOT NULL DEFAULT 0,
    created_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at      DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_office_sessions_key (doc_key),
    INDEX idx_office_sessions_expires (expires_unix)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS office_sessions;
-- +goose StatementEnd
