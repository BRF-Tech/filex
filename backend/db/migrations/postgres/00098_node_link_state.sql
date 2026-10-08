-- +goose Up
-- Why a link will not open, kept in the catalogue. See
-- sqlite/00098_node_link_state.sql for the model: the driver's reason
-- (outside_root, broken, unresolved) the last time the sync listed the link,
-- NULL when none is known (a row from before this migration until the next
-- sync of its folder, a driver that gives none, any row that is not a link).
ALTER TABLE nodes ADD COLUMN IF NOT EXISTS link_state TEXT;

-- +goose Down
ALTER TABLE nodes DROP COLUMN IF EXISTS link_state;
