-- +goose Up
-- THUMBNAIL FRESHNESS AND THE REPAIR TOOL (filex 0.50, docs/thumbnails.md ->
-- Design notes).
--
-- thumbnails.source_sig    the content fingerprint (model.Node.ContentFingerprint:
--                          the backend etag, else size + modification time) of
--                          the bytes the thumbnail was drawn from. '' on rows
--                          drawn before 0.50.
-- thumbnails.attempted_at  when the latest render started. The loop guard and
--                          the "pending left by a crash" rule read it.
-- pending_ops.skipped      items an operation deliberately left alone. The
--                          thumbnail repair counts files no engine draws (or
--                          that are end-to-end encrypted) here, next to done
--                          and failed.
--
-- 00075: 00073 and 00074 are the tenant realm and groups, in the same release.
ALTER TABLE thumbnails ADD COLUMN IF NOT EXISTS source_sig TEXT NOT NULL DEFAULT '';
ALTER TABLE thumbnails ADD COLUMN IF NOT EXISTS attempted_at TIMESTAMPTZ;
ALTER TABLE pending_ops ADD COLUMN IF NOT EXISTS skipped BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE pending_ops DROP COLUMN IF EXISTS skipped;
ALTER TABLE thumbnails DROP COLUMN IF EXISTS attempted_at;
ALTER TABLE thumbnails DROP COLUMN IF EXISTS source_sig;
