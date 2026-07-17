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
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    name        VARCHAR(255) NOT NULL,
    url         TEXT NOT NULL,
    secret      VARCHAR(255) NOT NULL DEFAULT '',
    events      TEXT NOT NULL,
    enabled     TINYINT(1) NOT NULL DEFAULT 1,
    created_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_targets;
-- +goose StatementEnd
