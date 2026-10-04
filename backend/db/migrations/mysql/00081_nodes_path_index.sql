-- +goose Up
-- WHAT IS BELOW A FOLDER IS FOUND THROUGH AN INDEX.
--
-- Nothing indexed nodes.path, and "the rows below this folder" was asked as
-- SUBSTR(path,1,n)=?, which no index can answer: counting or listing them read
-- every row of the storage. See sqlite/00081_nodes_path_index.sql for who asks
-- it, and how often.
--
-- "Below /a" is the byte range path >= '/a/' AND path < '/a0'. nodes.path has
-- been utf8mb4_0900_bin since 00041: it orders by code point - the byte order
-- of UTF-8 - and does not pad, so the range is exactly the folder's rows. Only
-- a 512-character prefix is indexed (InnoDB's key length limit under
-- utf8mb4); the engine re-checks a longer path against the row.
--
-- ⚠ Plain CREATE INDEX: MySQL has no CREATE INDEX IF NOT EXISTS (see
-- mysql/00036_pending_ops.sql), and goose runs this file once. An index made
-- by hand under this name has to be dropped first.
CREATE INDEX idx_nodes_storage_path ON nodes (storage_id, path(512));

-- +goose Down
DROP INDEX idx_nodes_storage_path ON nodes;
