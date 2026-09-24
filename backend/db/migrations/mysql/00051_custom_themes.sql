-- +goose Up
-- +goose StatementBegin

-- See sqlite/00051_custom_themes.sql for the model and for why the version
-- number skips a gap.
--
-- ⚠ A goose statement block is safe HERE because this migration is a single
-- CREATE TABLE. The rule that broke five migrations on the first boot of every
-- MySQL install (issue #19) is that a wrapped block of SEVERAL statements
-- reaches the server as one multi-statement query, which the driver refuses
-- unless the DSN opts into multiStatements.
--
-- ⚠ `theme_key` is VARCHAR(190) and not TEXT because it carries a UNIQUE key:
-- utf8mb4 costs 4 bytes per character and the InnoDB index prefix limit is 3072
-- bytes, so a TEXT column would need an explicit prefix length here and none in
-- the other two dialects. 190 is far past the 40 characters the handler's slug
-- pattern allows.
--
-- ⚠ `created_at` / `updated_at` are DATETIME(6) with a plain CURRENT_TIMESTAMP
-- default, the same shape 00039 uses, so the parity gate sees "has a default"
-- on all three engines.
CREATE TABLE IF NOT EXISTS custom_themes (
    id           BIGINT NOT NULL AUTO_INCREMENT,
    theme_key    VARCHAR(190) NOT NULL,
    name         TEXT NOT NULL,
    tokens_light TEXT NOT NULL,
    tokens_dark  TEXT NOT NULL,
    created_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    UNIQUE KEY uq_custom_themes_key (theme_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS custom_themes;
-- +goose StatementEnd
