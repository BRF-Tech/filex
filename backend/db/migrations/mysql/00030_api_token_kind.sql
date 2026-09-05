-- +goose Up
-- Token "kind" — what a credential IS, so the explorer can stop drawing a
-- person's surfaces for something that is not a person.
--
-- Two kinds:
--   user — a person's own credential (CLI, WebDAV, SFTP, S3, `filex mount`).
--          Acts as that person and hides nothing. Minted by /api/tokens.
--   app  — an integration: a host app's proxy, a bot, an MCP client. There is
--          no single human behind it, so identity-bearing surfaces (the
--          caller's own API keys, Recent, Starred, Shared with me) are
--          suppressed. Minted by /api/admin/ai-tokens.
--
-- ⚠ Existing rows default to 'app', and so does the column. The embeds we
-- actually run authenticate with ONE shared token injected by the host's
-- proxy, and v0.30.0 put "API keys" in the explorer's navigation panel — under
-- that token an embed user could list and revoke the credential the embed
-- itself runs on. Defaulting to 'user' would ship exactly that. The
-- restricting direction costs a personal token nothing but a one-line admin
-- edit (PATCH /api/admin/ai-tokens/{id} {"kind":"user"}), because these
-- surfaces only matter when a browser UI is drawn and a WebDAV mount or a CLI
-- never draws one.
ALTER TABLE api_tokens ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT 'app';

-- +goose Down
ALTER TABLE api_tokens DROP COLUMN kind;
