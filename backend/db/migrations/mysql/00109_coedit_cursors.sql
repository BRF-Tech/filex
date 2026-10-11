-- +goose Up
-- +goose StatementBegin

-- EDITING TOGETHER: THE MEMBERS' LATEST CURSORS (#189, internal/coedit). The
-- model is written out in db/migrations/sqlite/00109_coedit_cursors.sql.
-- Indexed columns are VARCHAR (a TEXT column takes no unique index); the
-- table and its indexes are ONE statement (MySQL DDL is not transactional).
CREATE TABLE IF NOT EXISTS coedit_cursors (
    id          BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    sid         VARCHAR(64) NOT NULL,
    client      VARCHAR(64) NOT NULL,
    ctr         BIGINT NOT NULL DEFAULT 0,
    ct          LONGBLOB,
    at_ms       BIGINT NOT NULL DEFAULT 0,
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE INDEX idx_coedit_cursors_client (sid, client),
    INDEX idx_coedit_cursors_at (sid, at_ms)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS coedit_cursors;
-- +goose StatementEnd
