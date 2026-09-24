-- +goose Up
-- TAGS ARE PERSONAL OR TEAM, AND A TEAM IS A TENANT.
--
-- Until now a tag was a `tag:<name>` row on node_meta — one per file, with no
-- owner and no tenant. So every tag was shared by every user on the instance,
-- across tenants, although routes.go introduced them as "Per-user metadata"
-- (tester, 2026-09-22): a non-admin's tag "müşteri teklifi" appeared in another
-- user's and the admin's sidebar and on the file, the other user could remove
-- it, and in a two-tenant install the other tenant's sidebar listed it too. The
-- name was also lower-cased on the way in ("Müşteri Teklifi" came back as
-- "müşteri teklifi").
--
-- The owner's decision: two kinds — personal (one person's own label, like a
-- star) and team (everyone in the tenant who can see the file), "tenant based".
--
--   tags       the vocabularies. A personal row has owner_id and no tenant_id;
--              a team row has tenant_id (a provider id, or 0 = "the
--              instance": a single-tenant install or a storage linked to no
--              tenant) and no owner_id. `name` is the display form, case
--              preserved; `name_key` is the identity (internal/tagname.Key:
--              case-folded, Turkish i's merged, NFC).
--   node_tags  which files carry which tag.
--
-- ⚠ UNIQUENESS, on all three engines, without a partial index (MySQL has
-- none): UNIQUE(owner_id, name_key) + UNIQUE(tenant_id, name_key). A NULL
-- never collides in a UNIQUE index, so a personal row is unique among ITS
-- OWNER's tags and a team row among ITS TENANT's — which is exactly the rule.
-- owner_id cascades with the user: a deleted account's personal tags go with
-- it, the same way its stars (user_node_meta) do.
--
-- EXISTING TAGS BECOME TEAM TAGS. They were effectively shared, so making them
-- personal would take every tag away from everybody but — whom? They record no
-- author. As team tags nothing disappears for anyone who could see them on a
-- file. The tenant follows the file's storage: one team tag per tenant the
-- storage is linked to (a storage linked to two tenants gives each its own
-- copy — each tenant's team tags are its own from now on), and tenant 0 for a
-- storage linked to none.
--
-- ⚠ THE KEY IN SQL. The Go fold cannot run inside a migration, so `name_key`
-- is approximated here. It only has to be good enough for the UNIQUE index:
-- the old names are already lower case (the old code lower-cased them), and
-- the one fold that differs for ordinary text is the dotless ı, merged here as
-- the Go fold merges it. Matching at run time always recomputes the key from
-- `name` in Go (handlers/tags.go), so a rarer difference (ß, ς, a decomposed
-- accent) can at worst leave two rows that behave as one tag — never a
-- collision that fails this migration.
--
-- ⚠ The old node_meta rows are DELETED once copied: left behind they would be
-- a second, stale copy of every tag, and Down rebuilds them from the team tags
-- (personal tags are dropped on the way down — the old schema has nowhere
-- private to put them, and turning them shared would publish them).
--
-- ⚠⚠ THE NUMBER: 00055, measured across the WORKING TREES on disk (the method
-- 00051_custom_themes.sql describes): 00052 share_app_links, 00053
-- share_revoked_at and 00054 api_token_explicit_scopes are taken in sibling
-- trees. If another branch has taken 00055 by the time these merge, renumber
-- THIS file; goose refuses to boot with two files of one version.
CREATE TABLE IF NOT EXISTS tags (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL,
    tenant_id  BIGINT,
    owner_id   BIGINT REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    name_key   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tags_owner_key ON tags(owner_id, name_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tags_tenant_key ON tags(tenant_id, name_key);

CREATE TABLE IF NOT EXISTS node_tags (
    tag_id     BIGINT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    node_id    BIGINT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tag_id, node_id)
);
CREATE INDEX IF NOT EXISTS idx_node_tags_node ON node_tags(node_id);

INSERT INTO tags (kind, tenant_id, owner_id, name, name_key)
SELECT 'team', x.tenant_id, NULL, MIN(x.name), x.name_key
FROM (
    SELECT COALESCE(ps.provider_id, 0)              AS tenant_id,
           SUBSTR(m.meta_key, 5)                    AS name,
           REPLACE(SUBSTR(m.meta_key, 5), 'ı', 'i') AS name_key
    FROM node_meta m
    JOIN nodes n ON n.id = m.node_id
    LEFT JOIN provider_storages ps ON ps.storage_id = n.storage_id
    WHERE m.meta_key LIKE 'tag:%' AND LENGTH(m.meta_key) > 4
) x
GROUP BY x.tenant_id, x.name_key;

INSERT INTO node_tags (tag_id, node_id)
SELECT DISTINCT t.id, m.node_id
FROM node_meta m
JOIN nodes n ON n.id = m.node_id
LEFT JOIN provider_storages ps ON ps.storage_id = n.storage_id
JOIN tags t ON t.kind = 'team'
           AND t.tenant_id = COALESCE(ps.provider_id, 0)
           AND t.name_key = REPLACE(SUBSTR(m.meta_key, 5), 'ı', 'i')
WHERE m.meta_key LIKE 'tag:%' AND LENGTH(m.meta_key) > 4;

DELETE FROM node_meta WHERE meta_key LIKE 'tag:%';

-- +goose Down
INSERT INTO node_meta (node_id, meta_key, value)
SELECT DISTINCT nt.node_id, 'tag:' || t.name_key, '1'
FROM node_tags nt
JOIN tags t ON t.id = nt.tag_id
WHERE t.kind = 'team'
ON CONFLICT DO NOTHING;
DROP TABLE IF EXISTS node_tags CASCADE;
DROP TABLE IF EXISTS tags CASCADE;
