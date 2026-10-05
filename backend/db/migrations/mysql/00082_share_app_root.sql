-- +goose Up
-- THE ROOT A LINK WAS OPENED UNDER (filex 0.52.0).
--
-- An app job a `root:` token queued is held to that token's folder: what the
-- app is told about the files it keeps state on, and the files it may name by
-- path. A link such a job opens (share_create) is answered later by a
-- visitor, and the job the visitor's submit queues runs as the link's
-- creator, with no token behind it. app_root records the root of the job that
-- opened the link (`<adapter>://<rel>`), and the visitor's job is held to it.
--
-- NULLABLE, no default, in all three dialects (the schema-parity gate
-- compares nullability). NO BACKFILL: a link opened before this migration,
-- or with no root, reads as "" and its visitor's job is held to no root, as
-- it always was.
--
-- THE NUMBER: 00082. 00081 is the app store install (feat/052-store-install),
-- which ships in the same release.
ALTER TABLE shares ADD COLUMN app_root VARCHAR(2048) NULL;

-- +goose Down
ALTER TABLE shares DROP COLUMN app_root;
