package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// The lazy catalogue's per-folder state (migration 00059) on every engine: a
// discovered folder never overwrites one that is catalogued, a watch can only
// be recorded on a catalogued folder, the restart demotes every watch, the
// frontier comes shallowest first, and "anything uncatalogued below this
// folder?" is exactly the folder's prefix — never a sibling that merely starts
// with its name, and a Turkish name as reliably as an ASCII one. SQLite
// compares the timestamps as text, so the "older than" query is asked across a
// second boundary here too.
func TestCatalogueFoldersOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)
			h := func(p string) string { return pathkey.Hash(st.ID, p) }
			folder := func(p string) model.CatalogueFolder {
				return model.CatalogueFolder{PathHash: h(p), Path: p, Depth: db.CatalogueFolderDepth(p)}
			}

			got, err := store.GetCatalogueFolder(ctx, st.ID, h("/"))
			require.NoError(t, err)
			require.Nil(t, got, "no row means not catalogued")

			old := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
			require.NoError(t, store.RecordCatalogueFolder(ctx, &model.CatalogueFolder{
				StorageID: st.ID, PathHash: h("/"), Path: "/", State: model.FolderCatalogued, ReconciledAt: &old, Entries: 3,
			}))
			require.NoError(t, store.DiscoverCatalogueFolders(ctx, st.ID, []model.CatalogueFolder{
				folder("/Müşteri"), folder("/Müşteri/2026"), folder("/Müşteri2"), folder("/Müşteri.eski"), folder("/b"),
			}))
			// Discovering the root again must not demote it.
			require.NoError(t, store.DiscoverCatalogueFolders(ctx, st.ID, []model.CatalogueFolder{folder("/")}))
			root, err := store.GetCatalogueFolder(ctx, st.ID, h("/"))
			require.NoError(t, err)
			require.True(t, root.Catalogued(), "a discovery never overwrites a catalogued row")
			require.Equal(t, 3, root.Entries)
			require.WithinDuration(t, old, *root.ReconciledAt, time.Second)

			front, err := store.ListCatalogueFolders(ctx, st.ID, model.CatalogueFolderFilter{State: model.FolderUncatalogued, Limit: 10})
			require.NoError(t, err)
			require.Equal(t, []string{"/Müşteri", "/Müşteri.eski", "/Müşteri2", "/b", "/Müşteri/2026"}, folderPaths(front),
				"the frontier comes shallowest first")

			under, err := store.ListCatalogueFolders(ctx, st.ID, model.CatalogueFolderFilter{Under: "/Müşteri"})
			require.NoError(t, err)
			require.Equal(t, []string{"/Müşteri", "/Müşteri/2026"}, folderPaths(under), "the folder and what is below it, not its look-alike siblings")

			below, err := store.HasUncataloguedUnder(ctx, st.ID, "/Müşteri")
			require.NoError(t, err)
			require.True(t, below)
			below, err = store.HasUncataloguedUnder(ctx, st.ID, "/Müşteri/2026")
			require.NoError(t, err)
			require.False(t, below)
			below, err = store.HasUncataloguedUnder(ctx, st.ID, "/")
			require.NoError(t, err)
			require.True(t, below)

			// A watch is only ever recorded on a catalogued folder.
			now := time.Now().UTC().Truncate(time.Second)
			require.NoError(t, store.SetCatalogueFolderWatch(ctx, st.ID, h("/b"), &now, false))
			b, err := store.GetCatalogueFolder(ctx, st.ID, h("/b"))
			require.NoError(t, err)
			require.Equal(t, model.FolderUncatalogued, b.State, "an uncatalogued folder cannot be watched")
			require.NoError(t, store.RecordCatalogueFolder(ctx, &model.CatalogueFolder{
				StorageID: st.ID, PathHash: h("/b"), Path: "/b", Depth: 1, State: model.FolderCatalogued, ReconciledAt: &now,
			}))
			require.NoError(t, store.SetCatalogueFolderWatch(ctx, st.ID, h("/b"), &now, false))
			require.NoError(t, store.TouchCatalogueFolderVisit(ctx, st.ID, h("/b"), now))
			b, err = store.GetCatalogueFolder(ctx, st.ID, h("/b"))
			require.NoError(t, err)
			require.Equal(t, model.FolderWatched, b.State)
			require.NotNil(t, b.WatchedAt)
			require.NotNil(t, b.VisitedAt)

			counts, err := store.CountCatalogueFolders(ctx, st.ID)
			require.NoError(t, err)
			require.Equal(t, model.CatalogueCounts{Uncatalogued: 4, Catalogued: 1, Watched: 1, RootCatalogued: true}, counts)

			// Older than an hour ago: the root (two hours), not /b (now).
			cut := time.Now().UTC().Add(-time.Hour)
			due, err := store.ListCatalogueFolders(ctx, st.ID, model.CatalogueFolderFilter{ReconciledBefore: &cut})
			require.NoError(t, err)
			require.Equal(t, []string{"/"}, folderPaths(due))

			n, err := store.ResetCatalogueWatches(ctx, st.ID)
			require.NoError(t, err)
			require.EqualValues(t, 1, n)
			b, err = store.GetCatalogueFolder(ctx, st.ID, h("/b"))
			require.NoError(t, err)
			require.Equal(t, model.FolderCatalogued, b.State)
			require.True(t, b.ReconcileOnOpen, "a watch lost with the process means: reconcile before trusting it")
			require.Nil(t, b.WatchedAt)

			require.NoError(t, store.DeleteCatalogueFoldersUnder(ctx, st.ID, "/Müşteri"))
			rest, err := store.ListCatalogueFolders(ctx, st.ID, model.CatalogueFolderFilter{})
			require.NoError(t, err)
			require.Equal(t, []string{"/", "/Müşteri.eski", "/Müşteri2", "/b"}, folderPaths(rest))
		})
	}
}

func folderPaths(rows []*model.CatalogueFolder) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Path)
	}
	return out
}
