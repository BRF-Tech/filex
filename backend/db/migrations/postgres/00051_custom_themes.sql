-- +goose Up
-- +goose StatementBegin

-- See sqlite/00051_custom_themes.sql for the model, for why a theme is a table
-- rather than a settings row, and for why the version number skips a gap.
--
-- ⚠ `tokens_light` / `tokens_dark` are TEXT and not JSONB, matching SQLite.
-- Nothing queries inside the documents — the handler parses them whole and
-- validates every key against an allowlist before storing — so JSONB would buy
-- an operator class no query uses, and the schema-parity gate compares column
-- names against the SQLite file.
CREATE TABLE IF NOT EXISTS custom_themes (
    id           BIGSERIAL PRIMARY KEY,
    theme_key    TEXT NOT NULL UNIQUE,
    name         TEXT NOT NULL,
    tokens_light TEXT NOT NULL,
    tokens_dark  TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS custom_themes CASCADE;
-- +goose StatementEnd
