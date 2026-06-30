-- +goose Up
-- Human-friendly display name on users. The admin UI (Users / UserEdit /
-- Profile) and TopNav read this field; before this column the backend had
-- no place to persist it so every edit silently dropped the value.
ALTER TABLE users ADD COLUMN display_name VARCHAR(255) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN display_name;
