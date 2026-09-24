-- +goose Up
-- +goose StatementBegin

-- See sqlite/00047_user_prefs.sql for the model, for why the choices live in
-- the database rather than in the browser, and for why `web` and `desktop` are
-- separate rows.
--
-- ⚠ A goose statement block is safe HERE and only here: this migration is a
-- single CREATE TABLE, so the block reaches the server as one query. The rule
-- that broke five migrations on the first boot of every MySQL install (issue
-- #19) is a wrapped block of SEVERAL statements.
--
-- ⚠ The foreign key is declared inside the CREATE, not added afterwards:
-- MySQL's DDL is not transactional, so a second statement that fails leaves a
-- table this migration then cannot re-create on the retry.
--
-- ⚠ `surface` is VARCHAR, not TEXT: it is half of the primary key and MySQL
-- cannot index a TEXT column without a prefix length.
--
-- ⚠ `doc` is TEXT and NULLABLE. MySQL cannot give a TEXT column an ordinary
-- DEFAULT, so NOT NULL DEFAULT '' would need an expression default here and a
-- plain one in the other two — and the parity gate exists precisely to catch a
-- column that is nullable on one engine and not on another.
CREATE TABLE IF NOT EXISTS user_prefs (
    user_id BIGINT NOT NULL,
    surface VARCHAR(16) NOT NULL,
    doc MEDIUMTEXT,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (user_id, surface),
    CONSTRAINT fk_uprefs_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_prefs;
-- +goose StatementEnd
