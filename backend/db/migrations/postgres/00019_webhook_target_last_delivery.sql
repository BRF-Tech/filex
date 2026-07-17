-- +goose Up
-- Webhook target last-delivery persistence ("Temizlik" wave). The admin
-- list used to surface only an in-memory, process-lifetime status; these
-- columns make the most recent delivery outcome survive restarts.
--
-- `last_status` is the HTTP status code of the final attempt (0 = the
-- request never got a response — DNS/connect/timeout). `last_error` is
-- NULL after a successful delivery, otherwise the aggregated error
-- message. `last_delivery_at` is the attempt timestamp (UTC).
ALTER TABLE webhook_targets ADD COLUMN last_status INTEGER;
ALTER TABLE webhook_targets ADD COLUMN last_error TEXT;
ALTER TABLE webhook_targets ADD COLUMN last_delivery_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE webhook_targets DROP COLUMN last_status;
ALTER TABLE webhook_targets DROP COLUMN last_error;
ALTER TABLE webhook_targets DROP COLUMN last_delivery_at;
