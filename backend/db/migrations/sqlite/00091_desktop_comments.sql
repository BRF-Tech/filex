-- +goose Up
-- The desktop app's own token comments, like its owner's browser (the
-- maintainer's decision, 2026-10-06, task #157).
--
-- Since this version adding and deleting a comment ask a token's `comments`
-- permission at `rw` (`comments:rw` in its scope list, package tokenperm), and
-- a token that does not name it holds `read` - every API key and agent token
-- among them, with no rewrite of their rows. The ONE exception is the desktop
-- pairing (source = 'desktop', migration 00069): it is the person's own app,
-- the browser's twin, so handlers.desktopScopes mints it with `comments:rw`,
-- and this gives the pairings made before the same level - otherwise every
-- desktop already installed would stop commenting at the upgrade.
--
-- Forward-idempotent: a list that already names a comments level is left as
-- it is (no second entry, no lowering), and so is an empty list, which grants
-- nothing (model.APIToken.HasScope) and must keep granting nothing.
-- The list is wrapped in commas so only a whole entry matches, never a
-- substring of another one.
UPDATE api_tokens SET scopes = scopes || ',comments:rw'
WHERE source = 'desktop'
  AND TRIM(COALESCE(scopes, '')) <> ''
  AND (',' || scopes || ',') NOT LIKE '%,comments:%';

-- +goose Down
-- Deliberately a no-op, as 00054: a desktop pairing minted after the upgrade
-- carries `comments:rw` too, and the rows this changed cannot be told apart
-- from those. An older binary does not know the entry and ignores it.
SELECT 1;
