-- +goose Up
-- Per-tenant session-cookie Domain (multi-tenant). Optional; NULL keeps the
-- 0.1.63 behaviour (derive from host, else global FILEX_COOKIE_DOMAIN).
-- Purely additive — single-tenant installs never read this column.
ALTER TABLE providers ADD COLUMN IF NOT EXISTS cookie_domain TEXT;

-- +goose Down
ALTER TABLE providers DROP COLUMN IF EXISTS cookie_domain;
