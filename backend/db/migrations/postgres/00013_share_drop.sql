-- +goose Up
-- Public file-drop (upload) links. The same `shares` table now backs both
-- the classic download link (/s/{token}) and its inverse, an upload link
-- (/d/{token}) that lets anonymous visitors write files INTO a folder
-- without ever seeing its contents. `kind` selects the direction; the
-- remaining columns hold drop-only caps/settings (NULL/0 for downloads).
ALTER TABLE shares ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'download';
ALTER TABLE shares ADD COLUMN IF NOT EXISTS max_uploads INTEGER;
ALTER TABLE shares ADD COLUMN IF NOT EXISTS upload_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN IF NOT EXISTS drop_settings TEXT;

-- +goose Down
ALTER TABLE shares DROP COLUMN IF EXISTS drop_settings;
ALTER TABLE shares DROP COLUMN IF EXISTS upload_count;
ALTER TABLE shares DROP COLUMN IF EXISTS max_uploads;
ALTER TABLE shares DROP COLUMN IF EXISTS kind;
