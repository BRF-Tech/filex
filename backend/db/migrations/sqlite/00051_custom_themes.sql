-- +goose Up
-- +goose StatementBegin

-- OPERATOR-DEFINED THEMES — "make filex look like our product", as data.
--
-- A theme is a NAME plus two maps of `--fe-*` custom properties, one for the
-- light variant and one for the dark one, because every built-in palette in
-- packages/core/src/lib/themes.ts has both and a custom one that had only a
-- light variant would go unreadable the moment somebody flipped their mode.
-- The rows are read, turned into the same `ThemeDef` shape the built-ins use,
-- and handed to the browser beside them.
--
-- ⚠ WHY A TABLE AND NOT A SETTINGS ROW, which is where branding.* and
-- ui.custom_css live and which would have needed no migration at all.
-- `BrandingSource.For` calls `ListSettings` — the WHOLE table — on every
-- public share/PIN page render whose 15s cache has expired. A theme document
-- is ~3 KB of JSON; three of them in the settings table would be 9 KB read,
-- parsed and thrown away on a hot path that wants five short strings. A
-- theme is also a listable, deletable, importable ENTITY with a name and a
-- stable id, which is a table's job and not a key/value pair's.
--
-- ⚠⚠ THE NUMBER IS 00051, NOT 00042 (what `ls` on this directory says is free)
-- AND NOT 00049 (what the other branch's committed history says is free).
-- goose refuses to run at all when two files in one directory declare the same
-- version, so a clash does not merge badly — it bricks the first boot of every
-- installation that merged both. A gap costs nothing: goose orders by version
-- and never requires them to be contiguous.
--
-- ⚠⚠ HOW TO MEASURE IT, because the obvious two ways are both wrong. This
-- number was first set to 00049 after checking `git ls-tree` against every
-- branch, which reported `feat/app-plugins` topping out at 00048. That was
-- stale by two: 00049_share_pin_enc and 00050_app_plugin_schedule existed only
-- as UNCOMMITTED files in that branch's worktree, where three agents were
-- writing at the time. Committed history is the wrong instrument for a number
-- that has to be unique across work still in flight. Measure the WORKING TREES
-- on disk:
--
--   for d in /g/filex*; do find "$d/backend/db/migrations" -name '000*.sql'; done \
--     | sed 's#.*/##' | grep -oE '^[0-9]{5}' | sort -n | tail -1
--
-- That reported 00050 at the time of writing, so this is 00051.
--
-- ⚠ `theme_key`, not `key`: `key` is reserved in PostgreSQL and MySQL and not
-- in SQLite, which is why migration 00035 exists (filex lesson #99). It holds
-- the slug an operator typed; the palette id the browser persists is that slug
-- behind a `custom:` prefix, so a custom theme can never collide with a
-- built-in id (`night`, `forest`, … , `default`) no matter what was typed.
--
-- ⚠ `tokens_light` / `tokens_dark` are TEXT holding a JSON object and are NOT
-- NULL. Nothing queries INSIDE them — they are parsed whole by the handler and
-- validated key by key against an allowlist before they are ever stored — so a
-- JSON column type would buy an operator class no query uses, and the
-- schema-parity gate compares names against this file.
CREATE TABLE IF NOT EXISTS custom_themes (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    theme_key    TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    tokens_light TEXT NOT NULL,
    tokens_dark  TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS custom_themes;
-- +goose StatementEnd
