-- +goose Up
-- The notification digest. See sqlite/00087_notify_digest.sql.
ALTER TABLE notification_settings ADD COLUMN IF NOT EXISTS urgent_overrides TEXT;

CREATE TABLE IF NOT EXISTS notify_digest_state (
    user_id     BIGINT NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    through_id  BIGINT NOT NULL DEFAULT 0,
    due_at      TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_notify_digest_state_due ON notify_digest_state (due_at);

CREATE TABLE IF NOT EXISTS notify_digest_policy (
    scope_id        BIGINT NOT NULL PRIMARY KEY,
    window_minutes  INTEGER NOT NULL DEFAULT 1,
    urgent_events   TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS notify_digest_policy;
DROP INDEX IF EXISTS idx_notify_digest_state_due;
DROP TABLE IF EXISTS notify_digest_state;
ALTER TABLE notification_settings DROP COLUMN IF EXISTS urgent_overrides;
