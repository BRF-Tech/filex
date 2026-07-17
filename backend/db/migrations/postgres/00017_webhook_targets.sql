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
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    url         TEXT NOT NULL,
    secret      TEXT NOT NULL DEFAULT '',
    events      TEXT NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_targets;
-- +goose StatementEnd
