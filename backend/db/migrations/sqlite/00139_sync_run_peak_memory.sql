-- +goose Up
-- HOW MUCH MEMORY A FULL SCAN TOOK (filex 0.61, #353; internal/sync memPeak,
-- internal/db sync_run_memory_sql.go, internal/syncadvice).
--
-- A full scan samples the filex process's memory while it walks the storage
-- and records the highest figure on its run. The storage form compares it with
-- what the process was given (the container's memory limit, or the machine's
-- RAM) and warns before a scan runs filex out of memory. The figure is the
-- whole process's, not this scan's alone. 0 (every run before this migration,
-- and a run still going): not measured.
ALTER TABLE sync_runs ADD COLUMN peak_mem_bytes INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sync_runs DROP COLUMN peak_mem_bytes;
