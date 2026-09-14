-- +goose Up
-- +goose StatementBegin

-- See sqlite/00039_user_view_prefs.sql for the model and for why this lives in
-- the database rather than in the browser. One JSON document per user, read in
-- full at sign-in and written in full, debounced.
--
-- ⚠ `prefs_json` is TEXT and nullable, matching SQLite rather than reaching
-- for JSONB. Nothing here queries INSIDE the document — the client parses it
-- whole — so a JSONB column would only buy an operator class no query uses,
-- and the schema-parity gate compares names against the SQLite file.
CREATE TABLE IF NOT EXISTS user_view_prefs (
    user_id BIGINT NOT NULL PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    prefs_json TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_view_prefs CASCADE;
-- +goose StatementEnd
