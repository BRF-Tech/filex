-- +goose Up
-- HOW MUCH MEMORY A FULL SCAN TOOK (filex 0.61, #353): the highest figure the
-- process held while the run walked its storage. 0: not measured. See the
-- sqlite migration of the same number.
ALTER TABLE sync_runs ADD COLUMN peak_mem_bytes BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sync_runs DROP COLUMN peak_mem_bytes;
