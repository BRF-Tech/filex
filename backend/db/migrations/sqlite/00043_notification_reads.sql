-- +goose Up
-- +goose StatementBegin

-- WHO HAS READ A BROADCAST — per reader.
--
-- A notification addressed to a user keeps its read state in its own
-- `read_at`: that user is its only reader. A broadcast (user_id NULL) has many
-- readers and had the same single `read_at`, so one reader's "mark all read"
-- read it for all of them — across tenants, and including rows that reader's
-- bell never showed. From here on a broadcast's own `read_at` is not written
-- again; a reader's state lives in the two tables below. A broadcast stamped
-- through that column before this migration stays read for everyone, which is
-- what it already was; nothing is backfilled.
--
-- ⚠ Numbered 00043 because 00042 (sessions.id_token) belongs to the
-- RP-initiated logout change; the two do not touch each other's tables, but
-- goose runs without allow-missing, so 00042 has to ship first or together.

-- "Mark all read": every broadcast up to `through_id` is read for this reader.
-- One row per reader, moved forward on each press, so a read-all is one write
-- however many broadcasts there are — and an unread read only looks at the
-- rows after it.
CREATE TABLE IF NOT EXISTS notification_read_through (
    user_id    INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    through_id INTEGER NOT NULL,
    read_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- A single broadcast marked read above the reader's `through_id` (a click on
-- one bell row). A read-all deletes the reader's marks it has overtaken.
CREATE TABLE IF NOT EXISTS notification_reads (
    notification_id INTEGER NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (notification_id, user_id)
);

-- Deleting a user cascades here; without it that is a scan of every mark.
CREATE INDEX IF NOT EXISTS idx_notification_reads_user ON notification_reads (user_id);

-- The unread broadcasts after a reader's `through_id` are a range seek on this
-- index, not a walk over every broadcast nobody stamped.
CREATE INDEX IF NOT EXISTS idx_notifications_unread_since ON notifications (user_id, read_at, id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_notifications_unread_since;
DROP INDEX IF EXISTS idx_notification_reads_user;
DROP TABLE IF EXISTS notification_reads;
DROP TABLE IF EXISTS notification_read_through;
-- +goose StatementEnd
