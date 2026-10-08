-- +goose Up
-- WEB PUSH (task #191; docs/NOTIFICATIONS.md → Web Push, internal/notify
-- push.go, internal/webpush).
--
-- A person turns push notifications on for a device (user settings →
-- Notifications); filex then pushes to it what their bell tells them, also
-- while filex is closed. Nothing about which rows are pushed is stored here:
-- a push reads the person's bell (internal/notify push.go).
--
-- push_subscriptions  one row per device.
--   endpoint       the push service address the browser gave. Never answered.
--   endpoint_hash  hex SHA-256 of endpoint. Unique: one browser profile is one
--                  device, whoever signs in on it (a second account that
--                  subscribes the same browser takes the row over). The client
--                  finds itself in the list by it.
--   p256dh, auth_secret  the browser's keys for the payload (RFC 8291).
--   label          what the device was called when it subscribed.
--   through_id     the device's mark: every notification at or below it was
--                  pushed to it or passed over. Moved by compare-and-set.
--   failures       the push service's refusals since the last push it took.
--   last_ok_at     when it last took one.
--   A deleted account takes its devices with it (ON DELETE CASCADE).
--
-- push_vapid_keys  the instance's VAPID key (RFC 8292): one row, id 1, made
--   at the first start and replaced only by a rotation (which also empties
--   push_subscriptions - a subscription is bound to the key it was made
--   with). private_key is sealed with FILEX_SECRET_KEY (internal/secretbox).
--
-- 00097: Web Push (filex Roadmap #191).
CREATE TABLE IF NOT EXISTS push_subscriptions (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint      TEXT NOT NULL,
    endpoint_hash TEXT NOT NULL,
    p256dh        TEXT NOT NULL,
    auth_secret   TEXT NOT NULL,
    label         TEXT NOT NULL DEFAULT '',
    through_id    BIGINT NOT NULL DEFAULT 0,
    failures      INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_ok_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_push_subscriptions_endpoint ON push_subscriptions (endpoint_hash);
CREATE INDEX IF NOT EXISTS idx_push_subscriptions_user ON push_subscriptions (user_id);

CREATE TABLE IF NOT EXISTS push_vapid_keys (
    id          BIGINT NOT NULL PRIMARY KEY,
    public_key  TEXT NOT NULL,
    private_key TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS push_vapid_keys;
DROP INDEX IF EXISTS idx_push_subscriptions_user;
DROP INDEX IF EXISTS idx_push_subscriptions_endpoint;
DROP TABLE IF EXISTS push_subscriptions;
