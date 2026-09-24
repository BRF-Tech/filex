-- +goose Up
-- A NODE'S CHILDREN ARE FOUND THROUGH AN INDEX.
--
-- nodes.parent_id references nodes(id) ON DELETE CASCADE, and nothing indexed
-- it: the only index holding the column, (storage_id, parent_id, name), cannot
-- answer "parent_id = ?". So every hard delete of a node -- every row a trash
-- purge removes -- read the whole nodes table looking for children to cascade
-- to. On an install with 231,074 nodes that was 300 ms a row (85 ms with this
-- index), and the store runs one SQLite connection, so a large purge held it
-- for hours while every other request queued behind it.
--
-- Numbered 00044 because 00042 and 00043 are taken by open changes (#40, #43);
-- IF NOT EXISTS because an operator may have created it by hand under this name.
CREATE INDEX IF NOT EXISTS idx_nodes_parent_id ON nodes(parent_id);

-- +goose Down
DROP INDEX IF EXISTS idx_nodes_parent_id;
