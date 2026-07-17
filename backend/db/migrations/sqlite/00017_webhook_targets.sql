-- +goose Up
-- +goose StatementBegin

-- Webhook v2 targets ("Bağlan" wave). The legacy single global webhook
-- (FILEX_WEBHOOK_URL / admin webhook-config) keeps working; these rows
-- are ADDITIONAL destinations. Each delivery carries X-Filex-Event +
-- X-Filex-Delivery headers and, when `secret` is set, an
-- X-Filex-Signature: sha256=<hex hmac-sha256(body)> header.
--
-- `events` is a comma-separated allow-list of event names the target
-- wants (e.g. "file.uploaded,share.created"); empty == every event.
CREATE TABLE IF NOT EXISTS webhook_targets (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    url         TEXT NOT NULL,
    secret      TEXT NOT NULL DEFAULT '',
    events      TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_targets;
-- +goose StatementEnd
