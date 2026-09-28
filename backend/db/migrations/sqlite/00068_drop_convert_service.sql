-- +goose Up
-- THE IFRAME CONVERTER IS GONE (filex 0.48.0). Conversion is the Convert
-- app (docs/APP-PLUGINS.md); the p2r3/convert side-car, its External services
-- row and FILEX_CONVERT_URL were removed. An instance that still has the row
-- would show a service nothing reads.
--
-- 00068: the highest number in every filex worktree on disk was 00067.
DELETE FROM external_services WHERE name = 'convert';

-- +goose Down
-- Nothing to put back: the row is recreated by nothing (the service is gone).
SELECT 1;
