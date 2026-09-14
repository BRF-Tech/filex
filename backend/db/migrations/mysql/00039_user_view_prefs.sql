-- +goose Up
-- +goose StatementBegin

-- See sqlite/00039_user_view_prefs.sql for the model and for why this lives in
-- the database rather than in the browser.
--
-- ⚠ A goose statement block is safe HERE and only here: this migration is a
-- single CREATE TABLE. The rule that broke five migrations on the first boot
-- of every MySQL install (issue #19) is that a wrapped block of SEVERAL
-- statements reaches the server as one multi-statement query, which the driver
-- refuses unless the DSN opts into multiStatements. One statement in a block
-- is one query, which is why 00005 wraps its CREATE TABLE the same way.
--
-- ⚠ The foreign key is declared inside the CREATE, not added afterwards:
-- MySQL's DDL is not transactional, so a second statement that fails leaves a
-- table this migration then cannot re-create on the retry.
--
-- ⚠ `prefs_json` is TEXT and NULLABLE. MySQL cannot give a TEXT column an
-- ordinary DEFAULT, so NOT NULL DEFAULT '' would need an expression default
-- here and a plain one in the other two — and the parity gate exists precisely
-- to catch a column that is nullable on one engine and not on another.
CREATE TABLE IF NOT EXISTS user_view_prefs (
    user_id BIGINT NOT NULL,
    prefs_json TEXT,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (user_id),
    CONSTRAINT fk_uvp_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_view_prefs;
-- +goose StatementEnd
