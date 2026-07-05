-- +goose Up
-- Per-tenant session-cookie Domain (multi-tenant). Optional; NULL keeps the
-- 0.1.63 behaviour (derive from host, else global FILEX_COOKIE_DOMAIN).
-- Purely additive — single-tenant installs never read this column.
ALTER TABLE providers ADD COLUMN cookie_domain VARCHAR(255);

-- +goose Down
ALTER TABLE providers DROP COLUMN cookie_domain;
