-- +goose Up
-- THE IdP's id_token, KEPT BESIDE THE SESSION IT SIGNED IN.
--
-- Sign-out used to delete filex's session and nothing else. With
-- FILEX_OIDC_AUTO_REDIRECT the login page sends the browser straight back to
-- the IdP, whose own session was still open, so the IdP answered with a fresh
-- code and the same account was signed in again without a form — measured
-- 2026-09-23 on a Keycloak 26 tenant: sign-out to signed-in in ~0.5 s, the
-- same session_state every time. Nobody could switch accounts, and on a
-- shared computer the next person got the previous one's files.
--
-- Ending the IdP session too (OpenID Connect RP-Initiated Logout 1.0) needs
-- the id_token as id_token_hint — without it Keycloak stops on a "Do you want
-- to log out?" page and other IdPs refuse outright. The callback used to
-- verify it and throw it away. NULL = no OIDC sign-in behind this session
-- (password, LDAP, desktop exchange) or one minted before this version, and
-- sign-out stays local for those, exactly as before.
--
-- ⚠ Written as 00042 in PR #40 and renumbered to 00057 when it was merged into
-- v0.43.0, whose own migrations took 00042-00055 (and PR #43 took 00056).
-- goose runs without allow-missing: a number is never reused.
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS id_token TEXT;

-- +goose Down
ALTER TABLE sessions DROP COLUMN IF EXISTS id_token;
