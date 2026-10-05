-- +goose Up
-- WHAT IS BELOW A FOLDER IS FOUND THROUGH AN INDEX.
--
-- ⚠ Written as 00081 in PR #89 and renumbered to 00083 when it was merged into
-- v0.52.0, whose own migrations took 00081 and 00082.
--
-- Nothing indexed nodes.path, and "the rows below this folder" was asked as
-- SUBSTR(path,1,n)=?, which no index can answer: counting or listing them read
-- every row of the storage. See sqlite/00083_nodes_path_index.sql for who asks
-- it, and how often.
--
-- ⚠ COLLATE "C". "Below /a" is the byte range path >= '/a/' AND path < '/a0',
-- and nodes.path carries the database's own collation, which is not byte
-- order. Under ICU en-US /müşteri/x sorts inside the range of /Müşteri, so the
-- plain comparison answers for a folder of another case; under glibc
-- en_US.utf8 /Rapor/a.txt sorts outside the range of /Rapor, so it does not
-- even answer for the folder itself.
--
-- ⚠ left(path, 512), not path. A B-tree row stops at 2704 bytes, and
-- nodes.path is TEXT: with an index on the whole path the INSERT of a longer
-- path fails unless it happens to compress under the limit - and so does this
-- migration, on a catalogue that holds one. The first 512 characters are at
-- most 2048 bytes. The statements ask this expression for the range, with its
-- two ends cut the same way, and path itself for the range
-- (internal/db nodes_under_sql.go) - what MySQL does with its path(512) by
-- itself.
--
-- The planner chooses this index for a storage its statistics know. For one
-- added since the last (auto)ANALYZE the questions read that storage's rows,
-- as before, until autoanalyze has run.
--
-- The build holds a lock that blocks writes to nodes until it is done; reads
-- go on.
--
-- IF NOT EXISTS so that a second run is a no-op. ⚠ An index made by hand under
-- this name is kept as it is, and unless it is on this very expression no
-- statement can enter it: drop it first.
CREATE INDEX IF NOT EXISTS idx_nodes_storage_path ON nodes (storage_id, (left(path, 512)) COLLATE "C");

-- An index on an expression has no statistics until the table is analyzed.
ANALYZE nodes;

-- +goose Down
DROP INDEX IF EXISTS idx_nodes_storage_path;
