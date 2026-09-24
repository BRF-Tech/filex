-- +goose Up
-- No-op on this engine. See sqlite/00044_nodes_parent_index.sql: nothing
-- indexed nodes.parent_id, so every hard delete of a node read the whole table
-- looking for children to cascade to. InnoDB refuses a foreign key without an
-- index led by its column and created one itself for fk_nodes_parent, so MySQL
-- never had the problem. The file exists so the three dialects keep one
-- numbering.
SELECT 1;

-- +goose Down
SELECT 1;
