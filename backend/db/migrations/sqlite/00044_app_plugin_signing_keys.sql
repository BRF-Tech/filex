-- +goose Up
-- Signing keys the host holds FOR app plugins (internal/wasmplugin
-- signing.go). A plugin that signs documents never sees a private key: it
-- asks for a certificate (cert_issue), hands the host a digest (host_sign)
-- and destroys the key when the document is done (key_destroy).
--
-- purpose 'ca'   — one per tenant (tenant_id 0 = single-tenant / platform),
--                  created lazily, ECDSA P-256, long-lived; its certificate is
--                  what a reader imports to trust every signature made here.
-- purpose 'leaf' — per signer, per document: issued by the tenant CA for the
--                  plugin that asked (plugin_id), short-lived, destroyed after
--                  use (key_sealed emptied, destroyed_at set; the row stays so
--                  the certificate can still be shown).
--
-- key_sealed is the PKCS#8 private key sealed with FILEX_SECRET_KEY
-- (secretbox); without a secret key the whole feature answers unavailable.
CREATE TABLE IF NOT EXISTS app_plugin_signing_keys (
    id           TEXT PRIMARY KEY,
    tenant_id    INTEGER NOT NULL DEFAULT 0,
    plugin_id    INTEGER NOT NULL DEFAULT 0,
    purpose      TEXT NOT NULL DEFAULT 'leaf',
    subject      TEXT NOT NULL DEFAULT '',
    cert_pem     TEXT NOT NULL DEFAULT '',
    key_sealed   TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at   DATETIME,
    destroyed_at DATETIME,
    retired_at   DATETIME
);
CREATE INDEX IF NOT EXISTS idx_app_plugin_signing_keys_ca ON app_plugin_signing_keys(tenant_id, purpose, retired_at);

-- +goose Down
DROP TABLE IF EXISTS app_plugin_signing_keys;
