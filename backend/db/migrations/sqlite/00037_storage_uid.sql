-- +goose Up
-- A second address for a storage, and this one never moves.
--
-- The storage NAME is the first path segment on every file protocol —
-- /dav/<name>/ over WebDAV, /<name>/ over SFTP and NFS, the bucket name over
-- the S3-compatible API. So renaming a storage silently re-addresses it, and
-- every mount, bookmark and script written against the old name answers 404
-- (issue #21: "I renamed it to ps-hot, but ... /dav/Garage S3/ 404").
--
-- The name has to stay editable: it is the label people read in the UI. So the
-- fix is not to freeze it but to give every storage an identifier that is
-- assigned once and never changes, and to let the protocols resolve either.
-- A mount written against the uid survives every rename.
--
-- ⚠ Nullable on purpose. A UNIQUE index tolerates NULL on every engine we
-- support, so a row that somehow arrives unfilled cannot collide with another
-- one. Both the backfill below and CreateStorage fill it.
ALTER TABLE storages ADD COLUMN uid TEXT;

-- Existing storages get one now rather than lazily: a "stable address" that
-- only appears after something happens to the row is not stable.
-- randomblob() is evaluated per row, so this is one uuid each, not one shared.
UPDATE storages
SET uid = lower(
    substr(hex(randomblob(4)), 1, 8) || '-' ||
    substr(hex(randomblob(2)), 1, 4) || '-' ||
    '4' || substr(hex(randomblob(2)), 2, 3) || '-' ||
    substr('89AB', 1 + (abs(random()) % 4), 1) || substr(hex(randomblob(2)), 2, 3) || '-' ||
    substr(hex(randomblob(6)), 1, 12)
)
WHERE uid IS NULL OR uid = '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_storages_uid ON storages(uid);

-- +goose Down
DROP INDEX IF EXISTS idx_storages_uid;
