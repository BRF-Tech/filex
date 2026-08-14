-- +goose Up
-- Profile picture. Stored ON THE USER ROW as a small data: URI (or an http(s)
-- / site-relative URL), the same shape branding.logo_url already uses — no new
-- storage path, no orphan files to garbage-collect, and it travels with the
-- account across a DB restore.
--
-- It is what the collaboration presence strip draws instead of initials, so
-- every client of the account (browser session, desktop app, any API key minted
-- under that user) shows the same face. Kept deliberately small: presence
-- frames carry it, so this is capped in the API, not just in the UI.
ALTER TABLE users ADD COLUMN IF NOT EXISTS avatar_url TEXT;

-- +goose Down
ALTER TABLE users DROP COLUMN IF EXISTS avatar_url;
