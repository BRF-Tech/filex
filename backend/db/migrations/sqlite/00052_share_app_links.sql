-- +goose Up
-- AN APP'S LINKS, COUNTED AND NAMED FOR WHAT THEY ARE.
--
-- 1. AN APP PAGE'S VISITS ARE NOT DOWNLOADS.
--
-- An app plugin's public page is a share (00046), and opening it was counted
-- on the share's `download_count` — deliberately, so the page's `max_visits`
-- ceiling was the share's own `max_downloads` and there was one counter to
-- enforce. On the owner's screen that read as a lie: a signing link somebody
-- had only LOOKED at twice said "İndirme 2" in My shares, while a file-drop
-- link said "İndirme 0" after an upload (2026-09-21, a tester). The owner's
-- ruling: page views must not count as downloads.
--
-- So an app link has two counters, each meaning one thing:
--
--   visit_count     times the page was opened — what `max_visits` caps (kept
--                   in `max_downloads`, the one ceiling column) and what the
--                   app is told as `page.visits`;
--   download_count  times a file the page exposed was actually fetched.
--
-- An ordinary share keeps visit_count at 0 and nothing about it changes.
--
-- ⚠ The backfill moves the counts app links already have from the column
-- that was misnamed for them to the right one. Without it every link opened
-- before this migration would claim its visits as downloads for the rest of
-- its life, and a capped page would get its whole allowance back.
--
-- ⚠⚠ THE NUMBER: 00052 was the next free one across every working tree on
-- disk when this was written (the measurement 00051_custom_themes.sql
-- describes). If another branch has taken 00052 by the time these merge, this
-- file is renumbered, not that one — goose refuses to boot with two files of
-- one version.
--
-- 2. WHAT A LINK IS. `purpose_json` holds what the app said a link is when
-- it opened it (wire.PagePurpose: a label, what revoking it does, the
-- section of the app's home page that shows it), so My shares and Shares can
-- name it — "Signed copy" — instead of listing it as a share somebody made by
-- hand. A manifest PAGE can declare a purpose, but a page-less link (an app's
-- plain share of a finished document) has no page, and read as exactly that
-- (2026-09-21). NULL: the page's purpose, or none. TEXT and nullable for the
-- reason 00046 gives for state_json (MySQL cannot default a TEXT column).
ALTER TABLE shares ADD COLUMN visit_count INTEGER NOT NULL DEFAULT 0;
UPDATE shares SET visit_count = download_count, download_count = 0
 WHERE plugin_id > 0 AND page_id <> '';
ALTER TABLE shares ADD COLUMN purpose_json TEXT;

-- +goose Down
ALTER TABLE shares DROP COLUMN purpose_json;
UPDATE shares SET download_count = download_count + visit_count
 WHERE plugin_id > 0 AND page_id <> '';
ALTER TABLE shares DROP COLUMN visit_count;
