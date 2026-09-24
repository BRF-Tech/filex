-- +goose Up
-- A token's scope list is EXPLICIT from now on — no door issues a token
-- without one, and the auth driver reads an empty list as "nothing", not as
-- "everything" (owner's decision, v0.43.0).
--
-- ⚠⚠ Until this version an empty `scopes` meant EVERY scope, admin included:
-- the admin screen said so ("If none are selected, all scopes are granted")
-- and a token minted there with nothing ticked read /api/ai/admin/users and
-- /api/ai/admin/storages (release-candidate sweep, 2026-09-21). Such a token
-- was created there precisely to mean "everything", so this rewrites each one
-- to the explicit full list — admin INCLUDED. Its effective access is exactly
-- what it was; it is only written down now, so the token's row shows it as an
-- admin token and the driver no longer has to guess. The CHANGELOG's upgrade
-- note asks operators to review those tokens and narrow the ones that do not
-- need admin.
--
-- ⚠ Only an EXACTLY empty list (after trimming). A list with only a `root:`
-- confinement and no verb granted no verb before and grants none now.
--
-- ⚠ The number is 00054: 00052 and 00053 are taken by branches still open in
-- other worktrees (measured by scanning every worktree on disk, not git
-- history — see the note in 00051).
UPDATE api_tokens SET scopes = 'read,write,delete,mcp,admin' WHERE TRIM(COALESCE(scopes, '')) = '';

-- +goose Down
-- Deliberately a no-op: which rows were empty before cannot be told apart
-- from tokens that were minted with the full list on purpose, and turning an
-- explicit list back into "empty" would, on a newer binary, grant nothing.
SELECT 1;
