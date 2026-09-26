-- +goose Up
-- Who put a thing in the trash. See sqlite/00061_node_deleted_by.sql for the
-- model: NULL is nobody in filex (the scanner, the virus scan, or a row
-- trashed before this column existed), and nothing is backfilled.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS deleted_by BIGINT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_nodes_deleted_by ON nodes(deleted_by) WHERE deleted_by IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_nodes_deleted_by;
ALTER TABLE nodes DROP COLUMN IF EXISTS deleted_by;
