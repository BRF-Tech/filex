-- +goose Up
-- THE TEMPLATES AN ADMINISTRATOR EDITED (filex 0.60; internal/templates).
--
-- filex ships a template for every mail it sends and every page it shows a
-- stranger (internal/templates/defaults). A row here replaces one of them, for
-- the whole install (tenant_id 0, the instance administrator's) or for one
-- tenant (tenant_id = its provider id, that tenant's administrator's). No row
-- means filex's own; deleting the row is "back to the default". tkey is the
-- template's name ("mail.share", "page.pin", "mail.layout"); subject is a
-- mail's subject line, body its HTML (or the page), text_body a mail's own
-- plain-text version ('' = made from the HTML). revision counts the saves, so
-- an editor that saves over a newer version is refused, and keys the rendered
-- output a page caches. last_error is the last time an edited template failed
-- to render (and its default was used instead), for the editor to show.
-- Times are unix seconds.
CREATE TABLE IF NOT EXISTS templates (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id     INTEGER NOT NULL DEFAULT 0,
    tkey          TEXT    NOT NULL,
    subject       TEXT    NOT NULL DEFAULT '',
    body          TEXT    NOT NULL DEFAULT '',
    text_body     TEXT    NOT NULL DEFAULT '',
    revision      INTEGER NOT NULL DEFAULT 1,
    updated_by    INTEGER,
    updated_at    INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT    NOT NULL DEFAULT '',
    last_error_at INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX IF NOT EXISTS ux_templates_scope_key ON templates (tenant_id, tkey);

-- +goose Down
DROP INDEX IF EXISTS ux_templates_scope_key;
DROP TABLE IF EXISTS templates;
