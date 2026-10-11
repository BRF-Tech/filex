-- +goose Up
-- +goose StatementBegin

-- EDITING TOGETHER: THE MEMBERS' LATEST CURSORS (#189, internal/coedit). The
-- model is written out in db/migrations/sqlite/00109_coedit_cursors.sql: one
-- row per member, its latest sealed cursor, read by the sockets of the other
-- processes on the same database.
CREATE TABLE IF NOT EXISTS coedit_cursors (
    id          BIGSERIAL PRIMARY KEY,
    sid         TEXT NOT NULL,
    client      TEXT NOT NULL,
    ctr         BIGINT NOT NULL DEFAULT 0,
    ct          BYTEA,
    at_ms       BIGINT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_cursors_client ON coedit_cursors (sid, client);
CREATE INDEX IF NOT EXISTS idx_coedit_cursors_at ON coedit_cursors (sid, at_ms);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS coedit_cursors;
-- +goose StatementEnd
