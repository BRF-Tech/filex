-- +goose Up
-- Who put a thing in the trash. See sqlite/00061_node_deleted_by.sql for the
-- model: NULL is nobody in filex (the scanner, the virus scan, or a row
-- trashed before this column existed), and nothing is backfilled.
--
-- ⚠ One statement, no goose statement block, the index before the foreign
-- key: the three reasons are in mysql/00038_node_actor_and_origin.sql.
ALTER TABLE nodes ADD COLUMN deleted_by BIGINT NULL, ADD INDEX idx_nodes_deleted_by (deleted_by), ADD CONSTRAINT fk_nodes_deleted_by FOREIGN KEY (deleted_by) REFERENCES users(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE nodes DROP FOREIGN KEY fk_nodes_deleted_by;
ALTER TABLE nodes DROP INDEX idx_nodes_deleted_by, DROP COLUMN deleted_by;
