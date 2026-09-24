-- +goose Up
-- AN APP PLUGIN'S PUBLIC PAGE IS A SHARE.
--
-- 00043 gave app plugins their own public-link table (app_plugin_pages) with
-- its own token, its own PIN, its own expiry, its own visit ceiling and its
-- own revoke. Three months of that is three copies of the one thing a share
-- link already is — and the administrator could not see or revoke a signature
-- request in **Shares**, because it was not one.
--
-- From v3 an app page is a row in THIS table. Everything a public link needs
-- keeps coming from the share machine:
--
--   token          shares.token          (the /s/<token> link)
--   PIN            shares.pin_hash       + pin_fails / locked_until below
--   expiry         shares.expires_at     (revoke = expires_at := now)
--   visit ceiling  shares.max_downloads  (a page open spends one)
--   visits         shares.download_count
--   creator        shares.created_by     (the job a visitor asks for runs as them)
--
-- and only what is genuinely the plugin's is new:
--
--   plugin_id   which installed app answers this link (0 = an ordinary share)
--   page_id     which manifest page it renders
--   state_json  the plugin's own durable record for this link (≤ 64 KiB,
--               read/written by the share_state host function)
--   files_json  the copies the plugin exposed to the visitor:
--               [{ref:"pub:N", name, file, size, mime}] where `file` is the
--               BASENAME inside <data-dir>/app-plugins/public/<share id>/.
--               ⚠ A basename, not an absolute path: 00043 stored the full
--               path, so moving or restoring the data directory silently
--               broke every exposed copy while the row still looked healthy.
--   subject     the line the public shell puts in its header ("Please sign
--               contract.pdf"). Host-owned, unlike state_json.
--
-- pin_fails / locked_until move the five-strikes-then-ten-minutes lock onto
-- the share, where it now covers EVERY public link — a download share's PIN
-- was previously guessable at the speed of HTTP, because the lock lived only
-- on the app-plugin table.
--
-- ⚠ NO DATA IS CARRIED OVER, and none can be. app_plugin_pages stored only
-- sha256(token); a share stores the token itself, so an existing /p/<token>
-- link cannot be turned into a /s/<token> one without the token nobody kept.
-- Those pages are therefore gone (their exposed copies are swept with the
-- directories). The table shipped in no release — it was added on this same
-- feature branch — so this affects development instances only.
--
-- ⚠ state_json / files_json are NULLABLE with no default, in all three
-- dialects, for the reason 00039 spells out: MySQL cannot give a TEXT column
-- an ordinary DEFAULT, and the schema-parity gate compares nullability across
-- engines. NULL and '' mean the same to every reader here.
ALTER TABLE shares ADD COLUMN plugin_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN page_id TEXT NOT NULL DEFAULT '';
ALTER TABLE shares ADD COLUMN subject TEXT NOT NULL DEFAULT '';
ALTER TABLE shares ADD COLUMN state_json TEXT;
ALTER TABLE shares ADD COLUMN files_json TEXT;
ALTER TABLE shares ADD COLUMN pin_fails INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shares ADD COLUMN locked_until DATETIME;

-- Partial: an ordinary share carries plugin_id 0 and the admin app-plugin
-- list only ever asks for the rows that belong to an app.
CREATE INDEX IF NOT EXISTS idx_shares_plugin ON shares(plugin_id, created_at) WHERE plugin_id > 0;

DROP TABLE IF EXISTS app_plugin_pages;

-- +goose Down
-- ⚠ app_plugin_pages is NOT recreated. Its rows were unrecoverable at Up
-- (only sha256 of each token was ever stored), so an empty table would be a
-- shape without content; 00043's own Down drops it again in any case.
DROP INDEX IF EXISTS idx_shares_plugin;
ALTER TABLE shares DROP COLUMN locked_until;
ALTER TABLE shares DROP COLUMN pin_fails;
ALTER TABLE shares DROP COLUMN files_json;
ALTER TABLE shares DROP COLUMN state_json;
ALTER TABLE shares DROP COLUMN subject;
ALTER TABLE shares DROP COLUMN page_id;
ALTER TABLE shares DROP COLUMN plugin_id;
