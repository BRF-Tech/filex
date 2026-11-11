-- +goose Up
-- THE TEMPLATES AN ADMINISTRATOR EDITED (filex 0.60; internal/templates):
-- one row per template replaced for the install (tenant_id 0) or one tenant
-- (its provider id). See the sqlite migration of the same number.
CREATE TABLE IF NOT EXISTS templates (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     BIGINT NOT NULL DEFAULT 0,
    tkey          TEXT   NOT NULL,
    subject       TEXT   NOT NULL DEFAULT '',
    body          TEXT   NOT NULL DEFAULT '',
    text_body     TEXT   NOT NULL DEFAULT '',
    revision      BIGINT NOT NULL DEFAULT 1,
    updated_by    BIGINT,
    updated_at    BIGINT NOT NULL DEFAULT 0,
    last_error    TEXT   NOT NULL DEFAULT '',
    last_error_at BIGINT NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_templates_scope_key ON templates (tenant_id, tkey);

-- +goose Down
DROP INDEX IF EXISTS ux_templates_scope_key;
DROP TABLE IF EXISTS templates CASCADE;
