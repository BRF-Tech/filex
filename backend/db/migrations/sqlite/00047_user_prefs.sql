-- +goose Up
-- +goose StatementBegin

-- WHAT EACH PERSON CHOSE ABOUT THE INTERFACE — theme, palette, density,
-- language — one JSON document per person PER SURFACE.
--
-- ⚠⚠ WHY THE DATABASE. The owner picked a theme in one browser and the other
-- browser did not have it. `localStorage` is per BROWSER, never per person: a
-- second machine, a private window, a cleared site, the desktop app — each one
-- is a fresh, empty set of choices with nothing to notice and no error. 00039
-- already made the same argument for per-folder view arrangements; this table
-- is that argument applied to the choices that decide what the whole interface
-- LOOKS like, which is the half a person actually notices when it is missing.
--
-- ⚠ ONE ROW PER (person, surface), not one per person. `surface` is `web` or
-- `desktop`, and they are deliberately SEPARATE documents: the desktop app is
-- a window on somebody's own machine — its own density, its own start view,
-- often its own theme against the OS — while `web` is the browser. Merging
-- them would mean one screen's choice silently reaching through to the other,
-- which is the very complaint this table answers, one level up.
--
-- ⚠ NOT a replacement for user_view_prefs (00039). That one is how each
-- FOLDER was left (view mode, sort, column widths) and is read and written on
-- every navigation; this one is the surface's own appearance and is read once
-- at sign-in. They are separate because they change at completely different
-- rates and one of them is per folder.
--
-- ⚠ `doc` is NULLABLE with no default in all three dialects, for the reason
-- 00039 spells out: MySQL cannot give a TEXT column an ordinary DEFAULT, and
-- the schema-parity gate compares nullability across engines. NULL and '' mean
-- the same thing to the reader: nothing chosen yet.
--
-- ⚠ The document is not interpreted here. It is validated as a JSON OBJECT and
-- bounded at 64 KiB by the endpoint (GET/PUT /api/me/prefs); its keys belong to
-- the client, so the server does not have to be edited in lockstep with a
-- theme picker.
CREATE TABLE IF NOT EXISTS user_prefs (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    surface TEXT NOT NULL,
    doc TEXT,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (user_id, surface)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_prefs;
-- +goose StatementEnd
