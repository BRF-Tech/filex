-- +goose Up
-- +goose StatementBegin

-- Per-user storage quota.
-- quota_bytes = 0 means unlimited.
-- usage_bytes is recomputed periodically by quota.Service.Recompute().
ALTER TABLE users ADD COLUMN quota_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN usage_bytes BIGINT NOT NULL DEFAULT 0;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN usage_bytes;
ALTER TABLE users DROP COLUMN quota_bytes;
-- +goose StatementEnd
