-- +goose Up
-- WHY A LINK WILL NOT OPEN, KEPT IN THE CATALOGUE (filex 0.54, issue #34).
--
-- A symlink row (type 'symlink') is a link the storage's driver will not
-- follow, and the driver says why when it lists it (storage.MetaLinkState):
--
--   outside_root  the target is outside the storage's folder and
--                 follow_symlinks is off
--   broken        the target does not exist
--   unresolved    a remote link the driver does not resolve (ftp, sftp)
--
-- Until now the catalogue kept THAT a row is such a link, not WHY, so a
-- listing answered by the catalogue - every listing after the storage's first
-- sync - said only "Link" and the general sentence, while the same folder
-- listed from the storage a moment earlier said "Outside storage".
--
--   link_state  what the driver said the last time the sync listed the link.
--               NULL: no reason known - a row catalogued before this
--               migration (the next sync of its folder fills it), a driver
--               that gives none, or any row that is not a link.
--
-- Read only for the rows a listing returns (Store.NodeLinkStates), never in
-- the common node column list.
ALTER TABLE nodes ADD COLUMN link_state TEXT;

-- +goose Down
ALTER TABLE nodes DROP COLUMN link_state;
