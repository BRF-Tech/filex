-- +goose Up
-- A share's PIN becomes recoverable by its owner and by an administrator —
-- see sqlite/00049_share_pin_enc.sql for the model, for why the value is
-- SEALED (internal/secretbox, FILEX_SECRET_KEY) rather than plain, for why
-- pin_hash stays authoritative for the gate, and for why no backfill exists.
--
-- ⚠ Not wrapped in a goose statement block: a wrapped block of several
-- statements reaches the server as ONE multi-statement query, which the driver
-- refuses unless the DSN opts into multiStatements (issue #19).
--
-- ⚠ TEXT and nullable with no default. MySQL cannot give a TEXT column an
-- ordinary DEFAULT and the parity gate compares nullability, so this matches
-- the other two dialects exactly — the same choice 00046 made for
-- state_json / files_json. A VARCHAR with a length would be worse than it
-- looks: the sealed value grows with the PIN, and a silently truncated
-- ciphertext is one that can never be opened again.
ALTER TABLE shares ADD COLUMN pin_enc TEXT NULL;

-- +goose Down
ALTER TABLE shares DROP COLUMN pin_enc;
