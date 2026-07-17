-- +goose Up
-- Cloud preparation (v0.7 "Kimlik" E3, docs/CLOUD.md). Plan metadata on the
-- providers (tenant) table for the FILEX_CLOUD self-signup skeleton:
--   plan        — plan id from FILEX_CLOUD_PLANS (e.g. "free", "pro")
--   limits_json — resolved plan limits snapshot (JSON), stamped at signup
--   billing_ref — billing correlation id (Stripe customer/subscription)
-- All three are NULLABLE and PASSIVE: nothing reads or writes them unless
-- FILEX_CLOUD=1, so existing installs see zero behavior change.
ALTER TABLE providers ADD COLUMN plan TEXT;
ALTER TABLE providers ADD COLUMN limits_json TEXT;
ALTER TABLE providers ADD COLUMN billing_ref TEXT;

-- +goose Down
ALTER TABLE providers DROP COLUMN billing_ref;
ALTER TABLE providers DROP COLUMN limits_json;
ALTER TABLE providers DROP COLUMN plan;
