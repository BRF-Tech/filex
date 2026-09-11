-- +goose Up
-- ⚠ Do not wrap these statements in a goose statement block: goose hands a
-- wrapped block to the server as ONE query, and the MySQL driver refuses a
-- query that carries several statements unless the DSN opts into
-- multiStatements. A wrapper here is what made this migration fail on the
-- FIRST boot of every MySQL install (issue #19). Leave the statements
-- unwrapped, or wrap them one at a time.

-- owner_id on nodes — used for per-user quota accounting.
-- Nullable: nodes that arrive via storage sync (not user upload) may not have an owner.
ALTER TABLE nodes ADD COLUMN owner_id BIGINT NULL;
ALTER TABLE nodes ADD CONSTRAINT fk_nodes_owner FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX idx_nodes_owner ON nodes(owner_id);


-- +goose Down
DROP INDEX idx_nodes_owner ON nodes;
ALTER TABLE nodes DROP FOREIGN KEY fk_nodes_owner;
ALTER TABLE nodes DROP COLUMN owner_id;
