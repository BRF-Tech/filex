-- +goose Up
-- A share's PIN becomes recoverable by its owner and by an administrator —
-- see sqlite/00049_share_pin_enc.sql for the model, for why the value is
-- SEALED (internal/secretbox, FILEX_SECRET_KEY) rather than plain, for why
-- pin_hash stays authoritative for the gate, and for why no backfill exists.
ALTER TABLE shares ADD COLUMN IF NOT EXISTS pin_enc TEXT;

-- +goose Down
ALTER TABLE shares DROP COLUMN IF EXISTS pin_enc;
