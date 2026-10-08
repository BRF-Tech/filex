-- +goose Up
-- THE LANGUAGE A WEBHOOK TARGET IS TOLD IN (filex 0.54, #191).
--
-- A notification is said by the server in its reader's language (internal/
-- notify say.go). A person reads in their account's; a webhook target is a
-- receiver no person stands behind, so it gets the language chosen for it
-- here. '' (every row before this migration): the instance's language
-- (FILEX_DEFAULT_LOCALE, else English).
ALTER TABLE webhook_targets ADD COLUMN lang TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE webhook_targets DROP COLUMN lang;
