-- +goose Up
-- An entry the storage could not answer for. See
-- sqlite/00078_node_unavailable.sql for the model: NULL is an ordinary row,
-- a reason makes the row listed with a warning and refused with
-- 409 ENTRY_UNAVAILABLE until the storage answers for it again.
ALTER TABLE nodes ADD COLUMN unavailable_reason TEXT NULL, ADD COLUMN unavailable_at DATETIME(6) NULL;

-- +goose Down
ALTER TABLE nodes DROP COLUMN unavailable_at, DROP COLUMN unavailable_reason;
