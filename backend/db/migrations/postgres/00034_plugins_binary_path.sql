-- +goose Up
-- Nothing to do on PostgreSQL: 00029 creates plugins.binary_path under its
-- final name.
--
-- The SQLite variant renames plugins.binary, which is what 00029 shipped and
-- what only SQLite ever accepted (issue #19). A Postgres instance either never
-- got past 00029 or, since the fix, created the column already named
-- binary_path — so there is nothing here to rename. The file exists so the
-- three dialects keep the same migration numbers.
SELECT 1;

-- +goose Down
SELECT 1;
