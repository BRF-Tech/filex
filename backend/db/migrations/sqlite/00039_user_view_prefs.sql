-- +goose Up
-- +goose StatementBegin

-- HOW EACH PERSON LEFT EACH FOLDER — one JSON document per user.
--
-- The owner's decision, 2026-09-13: "tarayıcıya değil db'ye kaydedeceğiz,
-- basit bir json olarak. Oradan çekersek ayarları, tarayıcıda 36 user ile
-- girsin yine fark etmez."
--
-- ⚠⚠ WHY THE DATABASE AND NOT localStorage, which is where every other view
-- preference in this product lives. Browser storage is per BROWSER, not per
-- person: on a shared machine, or any browser two accounts sign into one after
-- the other, the second person silently inherits the first person's folder
-- arrangements. There is no error and nothing to notice — just somebody else's
-- layout — and it is the exact thing the owner ruled out in the same breath
-- ("her user kendi görünümünü görür"). Against the user row it cannot happen,
-- and the arrangements follow the person to the desktop app and to their other
-- machines, which is what that sentence actually asks for.
--
-- ⚠ ONE DOCUMENT, not a row per folder. This is read in full, once, at sign-in
-- and written in full, debounced — it is never queried BY folder, so a table
-- keyed on (user, folder) would buy an index nothing reads and cost a round
-- trip per navigation. The client caps it at 300 folders and evicts the
-- least-recently-used, so the document is bounded at roughly 33 KB; see
-- `packages/core/src/lib/viewPrefs.ts` for how that number was chosen.
--
-- ⚠ `prefs_json` is NULLABLE with no default, in all three dialects on
-- purpose. MySQL cannot give a TEXT column an ordinary DEFAULT, so a
-- NOT NULL + DEFAULT '' here would have to be expressed differently there and
-- the schema-parity gate compares the dialects against this file. NULL and ''
-- mean the same thing to the reader: nothing remembered yet.
--
-- ⚠ The column is NOT called `key`, `value` or `doc`-adjacent-to-anything
-- reserved: 00035 exists because `key` is reserved in PostgreSQL and MySQL and
-- not in SQLite, and the rename had to reach three dialects and the shared Go
-- statements. `prefs_json` is a keyword in none of them.
CREATE TABLE IF NOT EXISTS user_view_prefs (
    user_id INTEGER NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    prefs_json TEXT,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_view_prefs;
-- +goose StatementEnd
