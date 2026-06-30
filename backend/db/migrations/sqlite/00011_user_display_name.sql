-- +goose Up
-- +goose StatementBegin

-- Human-friendly display name on users. The admin UI (Users / UserEdit /
-- Profile) and TopNav read this field; before this column the backend had
-- no place to persist it so every edit silently dropped the value.
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN display_name;
-- +goose StatementEnd
