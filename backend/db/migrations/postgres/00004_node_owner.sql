-- +goose Up
-- +goose StatementBegin

-- owner_id on nodes — used for per-user quota accounting.
-- Nullable: nodes that arrive via storage sync (not user upload) may not have an owner.
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS owner_id BIGINT REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS idx_nodes_owner ON nodes(owner_id) WHERE owner_id IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_nodes_owner;
ALTER TABLE nodes DROP COLUMN IF EXISTS owner_id;
-- +goose StatementEnd
