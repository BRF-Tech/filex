-- +goose Up
-- +goose StatementBegin

-- See sqlite/00047_user_prefs.sql for the model, for why the choices live in
-- the database rather than in the browser, and for why `web` and `desktop` are
-- separate rows.
--
-- ⚠ `doc` is TEXT and nullable, matching SQLite rather than reaching for
-- JSONB: nothing here queries INSIDE the document — the client parses it whole
-- — so JSONB would only buy an operator class no query uses, and the parity
-- gate compares names and nullability against the SQLite file.
CREATE TABLE IF NOT EXISTS user_prefs (
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    surface TEXT NOT NULL,
    doc TEXT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, surface)
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_prefs CASCADE;
-- +goose StatementEnd
