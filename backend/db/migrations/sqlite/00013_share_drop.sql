-- +goose Up
-- Public file-drop (upload) links. The same `shares` table now backs both
-- the classic download link (/s/{token}) and its inverse, an upload link
-- (/d/{token}) that lets anonymous visitors write files INTO a folder
-- without ever seeing its contents. `kind` selects the direction; the
-- remaining columns hold drop-only caps/settings (NULL/0 for downloads).
ALTER TABLE shares ADD COLUMN kind TEXT NOT NULL DEFAULT 'download';
ALTER TABLE shares ADD COLUMN max_uploads INTEGER;
ALTER TABLE shares ADD COLUMN upload_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN drop_settings TEXT;

-- +goose Down
ALTER TABLE shares DROP COLUMN drop_settings;
ALTER TABLE shares DROP COLUMN upload_count;
ALTER TABLE shares DROP COLUMN max_uploads;
ALTER TABLE shares DROP COLUMN kind;
