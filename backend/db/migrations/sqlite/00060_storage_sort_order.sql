-- +goose Up
-- THE ADMIN DECIDES THE ORDER THE STORAGES ARE LISTED IN (issue #57).
--
-- Storages were listed by id, which is creation order: the drive an admin
-- added last was always at the bottom of every sidebar, and the only way to
-- move one up was to delete and re-create everything above it. sort_order is
-- the position the admin gave it (1 = first), written only by
-- PUT /api/admin/storages/order; the storage edit form never touches it.
--
-- NULL = not placed. Those storages come after every placed one, in creation
-- order, so an install that never sets an order lists exactly as before.
--
-- 00060: the highest number in every filex worktree on disk was 00059
-- (migrations can sit uncommitted in another worktree, so git alone does not
-- say which numbers are taken).
ALTER TABLE storages ADD COLUMN sort_order INTEGER;

-- +goose Down
ALTER TABLE storages DROP COLUMN sort_order;
