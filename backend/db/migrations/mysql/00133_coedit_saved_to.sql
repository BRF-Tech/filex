-- +goose Up
-- WHERE AN EDITING SESSION'S SAVES GO WHEN IT IS NOT ITS OWN FILE (filex 0.57,
-- #268): beside it in another format, or a conflict copy. '' is the file
-- itself. See the sqlite migration of the same number. VARCHAR(2048) as the
-- session's own `path`.
ALTER TABLE coedit_sessions ADD COLUMN saved_to VARCHAR(2048) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE coedit_sessions DROP COLUMN saved_to;
