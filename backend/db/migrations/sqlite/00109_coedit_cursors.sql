-- +goose Up
-- +goose StatementBegin

-- EDITING TOGETHER: THE MEMBERS' LATEST CURSORS (#189, internal/coedit
-- cursors.go). A cursor frame is sealed by the member's page and not kept in
-- the log. One filex process hands it to the members connected to it at
-- once; this row - the member's LATEST sealed cursor, written at most twice a
-- second - is how a member connected to ANOTHER process on the same database
-- gets it: that process's socket reads the rows of its session that changed
-- since it last looked. One row per member, written over in place, gone with
-- the member and with the session.
--   ctr     the writer's cursor counter (a reader takes only a greater one).
--   ct      the sealed cursor; the relay never opens it.
--   at_ms   when it was written, Unix MILLISECONDS.
CREATE TABLE IF NOT EXISTS coedit_cursors (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sid         TEXT NOT NULL,
    client      TEXT NOT NULL,
    ctr         INTEGER NOT NULL DEFAULT 0,
    ct          BLOB,
    at_ms       INTEGER NOT NULL DEFAULT 0,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_coedit_cursors_client ON coedit_cursors (sid, client);
CREATE INDEX IF NOT EXISTS idx_coedit_cursors_at ON coedit_cursors (sid, at_ms);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS coedit_cursors;
-- +goose StatementEnd
