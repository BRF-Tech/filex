-- +goose Up
-- FILE TYPE ASSOCIATIONS AND APP THUMBNAILS (filex 0.50, docs/APP-PLUGINS.md ->
-- Default apps, docs/thumbnails.md -> Thumbnails drawn by apps).
--
-- file_associations     the administrator's rule for one kind of file (its
--                       extension) and one capability (open | thumbnail):
--                       the handlers in the order they are asked
--                       (handlers_json) and the ones switched off (off_json).
--                       A kind with no row keeps the default order.
-- app_thumb_limits      the administrator's limits for one app's thumbnail
--                       calls: the largest file sent, the time per file, the
--                       memory, how many at once. 0 = the default.
-- thumbnails.generator  who drew the picture: builtin, or app:<name>@<version>.
-- thumbnails.attempts   who was asked, in order, and what each answered (JSON);
--                       '' on rows drawn before 0.50.
CREATE TABLE IF NOT EXISTS file_associations (
    capability    TEXT NOT NULL,
    ext           TEXT NOT NULL,
    handlers_json TEXT NOT NULL DEFAULT '[]',
    off_json      TEXT NOT NULL DEFAULT '[]',
    updated_by    BIGINT,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (capability, ext)
);

CREATE TABLE IF NOT EXISTS app_thumb_limits (
    plugin_id    BIGINT PRIMARY KEY,
    max_input_mb BIGINT NOT NULL DEFAULT 0,
    timeout_s    BIGINT NOT NULL DEFAULT 0,
    memory_mb    BIGINT NOT NULL DEFAULT 0,
    concurrency  BIGINT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE thumbnails ADD COLUMN IF NOT EXISTS generator TEXT NOT NULL DEFAULT '';
ALTER TABLE thumbnails ADD COLUMN IF NOT EXISTS attempts TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE thumbnails DROP COLUMN IF EXISTS attempts;
ALTER TABLE thumbnails DROP COLUMN IF EXISTS generator;
DROP TABLE IF EXISTS app_thumb_limits;
DROP TABLE IF EXISTS file_associations;
