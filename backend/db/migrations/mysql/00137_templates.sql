-- +goose Up
-- THE TEMPLATES AN ADMINISTRATOR EDITED (filex 0.60; internal/templates):
-- one row per template replaced for the install (tenant_id 0) or one tenant
-- (its provider id). See the sqlite migration of the same number. tkey is
-- ASCII (dotted names), compared byte for byte; the template text itself is
-- MEDIUMTEXT, as a template may be up to 128 KiB. The text columns default to
-- '' as SQLite's do (an expression default, MySQL 8.0.13+, like 00008).
CREATE TABLE IF NOT EXISTS templates (
    id            BIGINT NOT NULL AUTO_INCREMENT,
    tenant_id     BIGINT NOT NULL DEFAULT 0,
    tkey          VARCHAR(120) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    subject       TEXT NOT NULL DEFAULT (''),
    body          MEDIUMTEXT NOT NULL DEFAULT (''),
    text_body     MEDIUMTEXT NOT NULL DEFAULT (''),
    revision      BIGINT NOT NULL DEFAULT 1,
    updated_by    BIGINT NULL,
    updated_at    BIGINT NOT NULL DEFAULT 0,
    last_error    TEXT NOT NULL DEFAULT (''),
    last_error_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    UNIQUE KEY ux_templates_scope_key (tenant_id, tkey)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS templates;
