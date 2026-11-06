-- +goose Up
-- THE TENANT A LINK WAS MADE IN (filex 0.59).
--
-- A public link (a share /s/, a file request /d/, an app's page) names a node,
-- and the node lives on a storage the platform operator may move to another
-- tenant. tenant_id is the tenant that storage was in when the link was made:
-- its first provider_storages row by provider id (what GetProviderIDForStorage
-- reads), 0 when it was in none. A link whose customer tenant no longer has the
-- storage is not served (db.LinkTenantStands); the row stays, and the link
-- answers again once the storage is back. A link made where the storage was in
-- no tenant, or in the platform's own, is not held to it. CreateShare stamps
-- every new link; existing links get the tenant their storage is in now.
ALTER TABLE shares ADD COLUMN tenant_id INTEGER;
UPDATE shares SET tenant_id = COALESCE((
    SELECT MIN(ps.provider_id) FROM nodes n
    JOIN provider_storages ps ON ps.storage_id = n.storage_id
    WHERE n.id = shares.node_id), 0);

-- +goose Down
ALTER TABLE shares DROP COLUMN tenant_id;
