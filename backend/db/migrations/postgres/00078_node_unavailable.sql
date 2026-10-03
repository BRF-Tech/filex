-- +goose Up
-- An entry the storage could not answer for. See
-- sqlite/00078_node_unavailable.sql for the model: NULL is an ordinary row,
-- a reason makes the row listed with a warning and refused with
-- 409 ENTRY_UNAVAILABLE until the storage answers for it again.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS unavailable_reason TEXT;
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS unavailable_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE nodes DROP COLUMN IF EXISTS unavailable_at;
ALTER TABLE nodes DROP COLUMN IF EXISTS unavailable_reason;
