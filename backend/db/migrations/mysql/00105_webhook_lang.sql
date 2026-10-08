-- +goose Up
-- THE LANGUAGE A WEBHOOK TARGET IS TOLD IN (filex 0.54, #191): '' is the
-- instance's language. See the sqlite migration of the same number.
ALTER TABLE webhook_targets ADD COLUMN lang VARCHAR(35) NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE webhook_targets DROP COLUMN lang;
