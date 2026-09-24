-- +goose Up
-- A NODE'S CHILDREN ARE FOUND THROUGH AN INDEX.
--
-- nodes.parent_id references nodes(id) ON DELETE CASCADE, and nothing indexed
-- it: the only index holding the column, (storage_id, parent_id, name), cannot
-- answer "parent_id = ?". So every hard delete of a node -- every row a trash
-- purge removes -- read the whole nodes table looking for children to cascade
-- to. PostgreSQL, like SQLite, does not index a referencing column by itself;
-- see sqlite/00058_nodes_parent_index.sql for what that cost a purge.
--
-- Written as 00044 in PR #47; numbered 00058 in the release, after the
-- migrations that landed first (00042-00057, #40 and #43 among them).
-- IF NOT EXISTS because an operator may have created it by hand under this name.
CREATE INDEX IF NOT EXISTS idx_nodes_parent_id ON nodes(parent_id);

-- +goose Down
DROP INDEX IF EXISTS idx_nodes_parent_id;
