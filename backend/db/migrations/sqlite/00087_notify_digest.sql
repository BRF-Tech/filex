-- +goose Up
-- THE NOTIFICATION DIGEST (docs/NOTIFICATIONS.md → The digest, internal/notify
-- digest.go).
--
-- A person chooses which kinds of notification are URGENT: those reach them
-- the moment they happen, as before. A kind that is not is held for a short
-- window (one minute by default, 1-15 set by an administrator) and the window
-- ends in ONE notification that says, folder by folder, what changed. Out of
-- the box EVERY kind is urgent (urgent_events NULL = the built-in list, which
-- is every kind): nothing is held until an administrator or a person turns a
-- kind off, so this migration changes nobody's notifications. The
-- rows themselves are written as before, one per event: the history, the
-- admin list, the audit log and the webhooks are unchanged. Only the telling
-- (the unread badge, the browser and desktop pop-ups, the e-mail) is held.
--
-- notification_settings.urgent_overrides
--                the person's own choices, a JSON object {"<event>": true|false}.
--                An event it does not name follows the administrator's
--                default. NULL: no choice made.
--
-- notify_digest_state  one row per person who has had a held notification.
--   through_id   every notification at or below it has been told (on its own
--                or in a digest). A held one above it is "quiet": in the list,
--                read, out of the badge, until the digest carries it.
--   due_at       when the open window ends. NULL: no window is open, or the
--                window is only known to the person's next read (a broadcast).
--
-- notify_digest_policy  the administrator's defaults, per scope.
--   scope_id     0 for the instance (a single-tenant install); a tenant's
--                provider id on a multi-tenant one. No foreign key: 0 is not a
--                provider. A tenant that goes leaves a row nobody reads.
--   window_minutes  1-15.
--   urgent_events   JSON array of the urgent kinds. NULL: the built-in list.
--
-- 00087: reserved for this change in the filex Roadmap (task #166).
ALTER TABLE notification_settings ADD COLUMN urgent_overrides TEXT;

CREATE TABLE IF NOT EXISTS notify_digest_state (
    user_id     INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    through_id  INTEGER NOT NULL DEFAULT 0,
    due_at      DATETIME,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_notify_digest_state_due ON notify_digest_state (due_at);

CREATE TABLE IF NOT EXISTS notify_digest_policy (
    scope_id        INTEGER NOT NULL PRIMARY KEY,
    window_minutes  INTEGER NOT NULL DEFAULT 1,
    urgent_events   TEXT,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose Down
DROP TABLE IF EXISTS notify_digest_policy;
DROP INDEX IF EXISTS idx_notify_digest_state_due;
DROP TABLE IF EXISTS notify_digest_state;
ALTER TABLE notification_settings DROP COLUMN urgent_overrides;
