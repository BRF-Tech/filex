-- +goose Up
-- WHERE AN EDITING SESSION'S SAVES GO WHEN IT IS NOT ITS OWN FILE (filex 0.57,
-- #268): beside it in another format, or a conflict copy. '' is the file
-- itself. See the sqlite migration of the same number.
ALTER TABLE coedit_sessions ADD COLUMN saved_to TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE coedit_sessions DROP COLUMN saved_to;
