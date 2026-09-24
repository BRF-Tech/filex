package db_test

// Personal and team tags (00055) on every engine.
//
// Two things only a real server can prove:
//
//   - The uniqueness rule. It is expressed as UNIQUE(owner_id, name_key) +
//     UNIQUE(tenant_id, name_key) and relies on a NULL never colliding, and on
//     MySQL it additionally relies on the key columns being utf8mb4_0900_bin —
//     under the table default (ai_ci) "müşteri" and "musteri" are ONE key and
//     the second insert fails. SQLite passes both by accident.
//   - The UPGRADE. Existing tags are node_meta `tag:<name>` rows; the migration
//     turns them into team tags whose tenant follows the file's storage. Like
//     the uid backfill (storage_uid_backfill_test.go), that is only exercised
//     by migrating to the version before, writing old-shape rows and then
//     applying — which the from-nothing migration test never does.

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/tagname"
)

func i64(v int64) *int64 { return &v }

func TestTagStoreOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st, err := store.CreateStorage(ctx, &model.Storage{
				Name: "tags", Driver: "local", MountPath: "/t", ConfigJSON: []byte(`{}`),
				SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
			})
			require.NoError(t, err)
			ali, err := store.CreateUser(ctx, "ali@example.com", "h", "user", "tr", "UTC")
			require.NoError(t, err)
			ayse, err := store.CreateUser(ctx, "ayse@example.com", "h", "user", "tr", "UTC")
			require.NoError(t, err)
			mk := func(p string) *model.Node {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, Name: p, Path: "/" + p, PathHash: pathkey.Hash(st.ID, "/"+p),
					Type: model.NodeTypeFile, Size: 1, Mime: "text/plain",
				})
				require.NoError(t, err)
				return n
			}
			teklif, rapor := mk("teklif.pdf"), mk("rapor.pdf")

			create := func(tag *model.Tag) *model.Tag {
				tag.Key = tagname.Key(tag.Name)
				out, err := store.CreateTag(ctx, tag)
				require.NoError(t, err, "%s: create %+v", e.name, tag)
				require.NotZero(t, out.ID)
				return out
			}
			aliMine := create(&model.Tag{Kind: model.TagPersonal, OwnerID: i64(ali.ID), Name: "Müşteri Teklifi"})
			// Same name, another person: their own tag, not a collision.
			ayseMine := create(&model.Tag{Kind: model.TagPersonal, OwnerID: i64(ayse.ID), Name: "müşteri teklifi"})
			team1 := create(&model.Tag{Kind: model.TagTeam, TenantID: i64(1), Name: "Müşteri Teklifi"})
			team0 := create(&model.Tag{Kind: model.TagTeam, TenantID: i64(0), Name: "Müşteri Teklifi"})
			// ⚠ MySQL: under the table's ai_ci collation this is the SAME key
			// as "müşteri" and the insert fails.
			create(&model.Tag{Kind: model.TagPersonal, OwnerID: i64(ali.ID), Name: "musteri"})
			create(&model.Tag{Kind: model.TagPersonal, OwnerID: i64(ali.ID), Name: "müşteri"})

			// The same key twice in ONE vocabulary is refused by the database.
			_, err = store.CreateTag(ctx, &model.Tag{Kind: model.TagPersonal, OwnerID: i64(ali.ID),
				Name: "MÜŞTERİ TEKLİFİ", Key: tagname.Key("MÜŞTERİ TEKLİFİ")})
			require.Error(t, err, "%s: a second personal tag with one key was accepted", e.name)
			_, err = store.CreateTag(ctx, &model.Tag{Kind: model.TagTeam, TenantID: i64(1),
				Name: "müşteri teklifi", Key: tagname.Key("müşteri teklifi")})
			require.Error(t, err, "%s: a second team tag with one key in one tenant was accepted", e.name)

			// Case survives the round trip.
			got, err := store.ListTags(ctx, model.TagQuery{OwnerID: ali.ID})
			require.NoError(t, err)
			require.Equal(t, "Müşteri Teklifi", got[0].Name, "%s: display name changed on the way through", e.name)
			require.Equal(t, model.TagPersonal, got[0].Kind)
			require.NotNil(t, got[0].OwnerID)
			require.Nil(t, got[0].TenantID)

			// Vocabulary queries: owner only; team of tenants {1}; team of every tenant.
			ids := func(q model.TagQuery) []int64 {
				rows, err := store.ListTags(ctx, q)
				require.NoError(t, err)
				var out []int64
				for _, r := range rows {
					out = append(out, r.ID)
				}
				return out
			}
			require.NotContains(t, ids(model.TagQuery{OwnerID: ali.ID}), ayseMine.ID)
			require.Equal(t, []int64{team1.ID}, ids(model.TagQuery{Team: true, TeamTenants: []int64{1}}))
			require.ElementsMatch(t, []int64{team1.ID, team0.ID}, ids(model.TagQuery{Team: true}))
			require.Empty(t, ids(model.TagQuery{}), "an empty query must select nothing, not everything")
			require.Contains(t, ids(model.TagQuery{OwnerID: ayse.ID, Team: true, TeamTenants: []int64{2, 0}}), team0.ID)

			// Links: adding twice is not an error; removal garbage-collects an
			// unused tag and keeps a used one.
			require.NoError(t, store.LinkNodeTags(ctx, teklif.ID, []int64{aliMine.ID, team1.ID}, nil))
			require.NoError(t, store.LinkNodeTags(ctx, teklif.ID, []int64{aliMine.ID}, nil), "%s: re-adding a link", e.name)
			require.NoError(t, store.LinkNodeTags(ctx, rapor.ID, []int64{team1.ID}, nil))
			on, err := store.ListNodeTags(ctx, teklif.ID)
			require.NoError(t, err)
			require.Len(t, on, 2)

			nodes, err := store.ListNodesByTagIDs(ctx, []int64{team1.ID, aliMine.ID}, 10)
			require.NoError(t, err)
			require.Len(t, nodes, 2, "%s: a file carrying two of the tags must be listed once", e.name)

			places, err := store.ListTagPlacements(ctx, model.TagQuery{Team: true, TeamTenants: []int64{1}})
			require.NoError(t, err)
			require.Len(t, places, 2)
			for _, p := range places {
				require.Equal(t, team1.ID, p.TagID)
				require.Equal(t, st.ID, p.StorageID)
			}

			require.NoError(t, store.LinkNodeTags(ctx, teklif.ID, nil, []int64{aliMine.ID, team1.ID}))
			require.NotContains(t, ids(model.TagQuery{OwnerID: ali.ID}), aliMine.ID,
				"%s: a personal tag on no file any more must be gone", e.name)
			require.Contains(t, ids(model.TagQuery{Team: true}), team1.ID,
				"%s: a team tag still on rapor.pdf must stay", e.name)

			// A deleted account takes its personal tags with it (FK cascade).
			require.NoError(t, store.LinkNodeTags(ctx, rapor.ID, []int64{ayseMine.ID}, nil))
			require.NoError(t, store.DeleteUser(ctx, ayse.ID))
			require.Empty(t, ids(model.TagQuery{OwnerID: ayse.ID}), "%s: personal tags outlived their owner", e.name)
			on, err = store.ListNodeTags(ctx, rapor.ID)
			require.NoError(t, err)
			require.Len(t, on, 1, "%s: the owner's link must go with the tag", e.name)
		})
	}
}

// versionBeforeTags is the last migration before 00055. On a tree without
// 00054 goose simply stops at the highest version below it.
const versionBeforeTags = 54

// TestTagMigrationOnEveryEngine upgrades a database holding pre-v0.43 tags.
func TestTagMigrationOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			admin := ""
			if e.dsnEnv != "" {
				if admin = envOrSkip(t, e); admin == "" {
					return
				}
			}
			drv, err := db.Get(e.driver)
			require.NoError(t, err)
			sqlDB, err := drv.Open(context.Background(), e.fresh(t, admin))
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			ctx := context.Background()

			goose.SetBaseFS(drv.MigrationsFS())
			defer goose.SetBaseFS(nil)
			require.NoError(t, goose.SetDialect(drv.Dialect()))
			require.NoError(t, goose.UpToContext(ctx, sqlDB, ".", versionBeforeTags))

			exec := func(q string) {
				_, err := sqlDB.ExecContext(ctx, q)
				require.NoError(t, err, "%s: %s", e.name, q)
			}
			// Three storages: linked to no tenant, to alpha, to alpha AND bravo.
			for i, name := range []string{"plain", "alpha-only", "shared"} {
				exec(fmt.Sprintf(`INSERT INTO storages (id, name, driver, mount_path, config_json, sync_mode, sync_interval_s, enabled, read_only)
					VALUES (%d,'%s','local','/x','{}','ondemand',900,%s,%s)`, i+1, name, boolLit(e.driver, true), boolLit(e.driver, false)))
			}
			exec(`INSERT INTO providers (id, slug, name) VALUES (7,'alpha','Alpha')`)
			exec(`INSERT INTO providers (id, slug, name) VALUES (8,'bravo','Bravo')`)
			exec(`INSERT INTO provider_storages (provider_id, storage_id) VALUES (7,2)`)
			exec(`INSERT INTO provider_storages (provider_id, storage_id) VALUES (7,3)`)
			exec(`INSERT INTO provider_storages (provider_id, storage_id) VALUES (8,3)`)
			node := func(id, storage int, name string) {
				p := "/" + name
				exec(fmt.Sprintf(`INSERT INTO nodes (id, storage_id, name, path, path_hash, type, size, sync_state)
					VALUES (%d,%d,'%s','%s','%s','file',1,'synced')`, id, storage, name, p, pathkey.Hash(int64(storage), p)))
			}
			node(1, 1, "a.pdf")
			node(2, 1, "b.pdf")
			node(3, 2, "c.pdf")
			node(4, 3, "d.pdf")
			tag := func(nodeID int, name string) {
				exec(fmt.Sprintf(`INSERT INTO node_meta (node_id, meta_key, value) VALUES (%d,'tag:%s','1')`, nodeID, name))
			}
			// What v0.42 wrote: lower-cased. "ışık" and "işik" were two rows
			// then; they are one tag by the new rule and must not make the
			// migration's UNIQUE insert fail.
			tag(1, "müşteri teklifi")
			tag(2, "müşteri teklifi")
			tag(1, "ışık")
			tag(2, "işik")
			tag(3, "müşteri teklifi")
			tag(4, "ortak")
			exec(`INSERT INTO node_meta (node_id, meta_key, value) VALUES (1,'note','keep me')`)

			// The upgrade.
			require.NoError(t, db.Migrate(ctx, drv, sqlDB))

			store := drv.NewStore(sqlDB)
			all, err := store.ListTags(ctx, model.TagQuery{Team: true})
			require.NoError(t, err)
			type row struct {
				tenant int64
				name   string
			}
			var got []row
			for _, tg := range all {
				require.Equal(t, model.TagTeam, tg.Kind, "%s: an existing tag came out %q — existing tags must become TEAM tags", e.name, tg.Kind)
				require.Nil(t, tg.OwnerID)
				require.NotNil(t, tg.TenantID)
				got = append(got, row{*tg.TenantID, tg.Name})
			}
			sort.Slice(got, func(i, j int) bool {
				if got[i].tenant != got[j].tenant {
					return got[i].tenant < got[j].tenant
				}
				return got[i].name < got[j].name
			})
			// Storage 1 (no tenant): "ışık" and "işik" merged into ONE tag. Which
			// spelling survives is MIN() under the engine's collation, so the
			// assertion is on the identity, not on the letters.
			require.Len(t, got, 5, "%s: %+v", e.name, got)
			require.Equal(t, int64(0), got[0].tenant)
			require.Equal(t, int64(0), got[1].tenant)
			light := got[0]
			if light.name == "müşteri teklifi" {
				light = got[1]
			}
			require.Equal(t, tagname.Key("ışık"), tagname.Key(light.name), "%s: %+v", e.name, got)
			require.Equal(t, []row{
				{7, "müşteri teklifi"}, // storage 2 → alpha
				{7, "ortak"},           // storage 3 → alpha's copy
				{8, "ortak"},           // storage 3 → bravo's copy
			}, got[2:], "%s: tenants must follow the file's storage", e.name)
			require.Contains(t, []string{got[0].name, got[1].name}, "müşteri teklifi")

			// Every file still carries its tags.
			for nodeID, want := range map[int64]int{1: 2, 2: 2, 3: 1, 4: 2} {
				on, err := store.ListNodeTags(ctx, nodeID)
				require.NoError(t, err)
				require.Len(t, on, want, "%s: node %d lost or gained tags in the upgrade", e.name, nodeID)
			}
			// The old rows are gone; other node_meta is untouched.
			var n int
			require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_meta WHERE meta_key LIKE 'tag:%'`).Scan(&n))
			require.Zero(t, n, "%s: the old tag rows were left behind", e.name)
			require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_meta WHERE meta_key = 'note'`).Scan(&n))
			require.Equal(t, 1, n)

			// And back down: the old shared rows are rebuilt from the TEAM
			// tags, and a personal tag is NOT published on the way (the old
			// schema has nowhere private to put it).
			owner, err := store.CreateUser(ctx, "down@example.com", "x", "user", "tr", "UTC")
			require.NoError(t, err)
			uid := owner.ID
			secret, err := store.CreateTag(ctx, &model.Tag{Kind: model.TagPersonal, OwnerID: &uid, Name: "Gizli", Key: tagname.Key("Gizli")})
			require.NoError(t, err)
			require.NoError(t, store.LinkNodeTags(ctx, 3, []int64{secret.ID}, nil))
			// ⚠ db.Migrate clears goose's base FS on its way out.
			goose.SetBaseFS(drv.MigrationsFS())
			require.NoError(t, goose.DownToContext(ctx, sqlDB, ".", versionBeforeTags))
			rows, err := sqlDB.QueryContext(ctx, `SELECT node_id, meta_key FROM node_meta WHERE meta_key LIKE 'tag:%' ORDER BY node_id, meta_key`)
			require.NoError(t, err)
			var back []string
			for rows.Next() {
				var id int64
				var k string
				require.NoError(t, rows.Scan(&id, &k))
				back = append(back, fmt.Sprintf("%d %s", id, k))
			}
			require.NoError(t, rows.Close())
			require.Len(t, back, 6, "%s: down must rebuild one row per (file, team tag): %v", e.name, back)
			for _, b := range back {
				require.NotContains(t, b, "gizli", "%s: a personal tag was published by the downgrade", e.name)
			}
			require.Contains(t, back, "3 tag:müşteri teklifi")
			require.Contains(t, back, "4 tag:ortak")
		})
	}
}
