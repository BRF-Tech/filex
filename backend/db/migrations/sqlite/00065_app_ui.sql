-- +goose Up
-- AN APP'S OWN INTERFACE (filex 0.48.0, docs/APP-PLUGINS-API.md → An app's own
-- interface).
--
-- An app may bring its own HTML/JS/CSS — a zip, ui.zip in its directory —
-- which filex serves in a sandboxed frame at
-- /_appui/<name>/<first 16 hex digits of this hash>/<path>.
--
-- ui_sha256  the interface bundle's sha256 as installed; checked again at
--            every load, like the module's sha256. '' = no interface.
--
-- 00065: the highest number in every filex worktree on disk was 00064.
ALTER TABLE app_plugins ADD COLUMN ui_sha256 TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE app_plugins DROP COLUMN ui_sha256;
