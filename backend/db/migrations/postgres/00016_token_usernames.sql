-- +goose Up
-- Token "usernames" — the identity an API-key connection acts under.
--
-- One durable token often serves several consumers (work panel, fishapp PWA,
-- a PC MCP client). `usernames` is a comma-separated allow-list of slugs the
-- caller may pick from per request via the X-Filex-Token-User header; the
-- FIRST entry is the default. Empty list == only the token's label is usable
-- (legacy behavior). A requested name outside the list is rejected (403).
ALTER TABLE api_tokens ADD COLUMN IF NOT EXISTS usernames TEXT NOT NULL DEFAULT '';

-- Which token username created a share — surfaced in the admin Shares list
-- ("admin (work)"). Empty for session-created shares.
ALTER TABLE shares ADD COLUMN IF NOT EXISTS created_via TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN IF EXISTS usernames;
ALTER TABLE shares DROP COLUMN IF EXISTS created_via;
