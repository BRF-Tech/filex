-- +goose Up
-- +goose StatementBegin

-- owner_id on nodes — used for per-user quota accounting.
-- Nullable: nodes that arrive via storage sync (not user upload) may not have an owner.
ALTER TABLE nodes ADD COLUMN owner_id BIGINT NULL;
ALTER TABLE nodes ADD CONSTRAINT fk_nodes_owner FOREIGN KEY (owner_id) REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX idx_nodes_owner ON nodes(owner_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX idx_nodes_owner ON nodes;
ALTER TABLE nodes DROP FOREIGN KEY fk_nodes_owner;
ALTER TABLE nodes DROP COLUMN owner_id;
-- +goose StatementEnd
