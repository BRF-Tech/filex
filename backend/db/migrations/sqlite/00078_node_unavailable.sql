-- +goose Up
-- AN ENTRY THE STORAGE COULD NOT ANSWER FOR (filex 0.50, issue #104).
--
-- The storage sync drops a row whose object is gone (issue #74) only once the
-- storage itself confirms it: the walk did not list it AND its own Stat answers
-- "not found". Any other answer - a plugin that does not speak Stat for a
-- folder, a permission it lacks, a backend error - used to keep the row
-- exactly as it was, live and clickable, with nothing anywhere saying that
-- nothing about it could be trusted any more.
--
--   unavailable_reason  what the storage answered, NULL for an ordinary row.
--                       A row that carries one is listed with a warning and
--                       every operation on it (and below it, for a folder) is
--                       refused with 409 ENTRY_UNAVAILABLE.
--   unavailable_at      when it was last given that answer.
--
-- Both are cleared when the storage answers for the row again: its Stat
-- succeeds, or the walk lists it. A definite "not found" drops the row, as
-- before.
--
-- 00078: 00076 and 00077 are the tenant self-service and file associations, in
-- the same release.
ALTER TABLE nodes ADD COLUMN unavailable_reason TEXT;
ALTER TABLE nodes ADD COLUMN unavailable_at DATETIME;

-- +goose Down
ALTER TABLE nodes DROP COLUMN unavailable_at;
ALTER TABLE nodes DROP COLUMN unavailable_reason;
