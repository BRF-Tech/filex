package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Migration 00094 (#186) on every engine: replica failures are keyed by
// (storage, path, op) - the same path failing on two storages is two rows,
// each resolved on its own - and replica_initial_copies holds one initial copy
// per storage: started once per target (a restart begins it anew), claimed by
// one worker at a time, saved only by the worker that holds it, removed with
// the link.
func TestReplicaFailuresAndInitialCopiesOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			// Failures, per storage.
			require.NoError(t, store.UpsertReplicaFailure(ctx, 1, "/a.txt", "write", "E1", "one"))
			require.NoError(t, store.UpsertReplicaFailure(ctx, 2, "/a.txt", "write", "E2", "two"))
			require.NoError(t, store.UpsertReplicaFailure(ctx, 1, "/a.txt", "write", "E3", "again"))
			rows, total, err := store.ListReplicaFailures(ctx, true, 10, 0)
			require.NoError(t, err)
			require.EqualValues(t, 2, total, "%s: one storage's failure overwrote another's", e.name)
			byStorage := map[int64]*model.ReplicaFailure{}
			for _, f := range rows {
				byStorage[f.StorageID] = f
			}
			require.Equal(t, 2, byStorage[1].Attempts, "%s: a repeated failure is one row with attempts", e.name)
			require.Equal(t, "E3", byStorage[1].ErrorCode)
			require.Equal(t, "E2", byStorage[2].ErrorCode)

			require.NoError(t, store.ResolveReplicaFailure(ctx, 2, "/a.txt", "write"))
			n, err := store.CountUnresolvedReplicaFailures(ctx)
			require.NoError(t, err)
			require.EqualValues(t, 1, n, "%s: resolving storage 2's failure touched storage 1's", e.name)

			// Initial copies.
			none, err := store.GetReplicaInitialCopy(ctx, 7)
			require.NoError(t, err)
			require.Nil(t, none, "no row: (nil, nil)")

			c, started, err := store.StartReplicaInitialCopy(ctx, 7, 3, 1000, false)
			require.NoError(t, err)
			require.True(t, started)
			require.Equal(t, model.ReplicaCopyPending, c.Phase)
			_, started, err = store.StartReplicaInitialCopy(ctx, 7, 3, 1001, false)
			require.NoError(t, err)
			require.False(t, started, "%s: the same link started its copy twice", e.name)

			ok, err := store.ClaimReplicaInitialCopy(ctx, 7, 3, "w1", 1000, 1100)
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = store.ClaimReplicaInitialCopy(ctx, 7, 3, "w2", 1050, 1150)
			require.NoError(t, err)
			require.False(t, ok, "%s: two workers held one copy", e.name)
			ok, err = store.ClaimReplicaInitialCopy(ctx, 7, 4, "w2", 2000, 2100)
			require.NoError(t, err)
			require.False(t, ok, "%s: a claim for another target took the copy", e.name)

			got, err := store.GetReplicaInitialCopy(ctx, 7)
			require.NoError(t, err)
			got.Phase, got.Cursor, got.Counted, got.Total = model.ReplicaCopyCopying, "/docs/ç ğ ş.txt", true, 5
			got.Copied, got.Present, got.Excluded, got.Failed, got.CopiedBytes = 2, 1, 1, 0, 12345
			got.UpdatedUnix, got.LeaseOwner, got.LeaseUntil = 1060, "w1", 1200
			ok, err = store.SaveReplicaInitialCopy(ctx, got, "w1")
			require.NoError(t, err)
			require.True(t, ok)
			ok, err = store.SaveReplicaInitialCopy(ctx, got, "w1")
			require.NoError(t, err)
			require.True(t, ok, "%s: an unchanged save read as a lost lease (MySQL counts changed rows)", e.name)
			ok, err = store.SaveReplicaInitialCopy(ctx, got, "w2")
			require.NoError(t, err)
			require.False(t, ok, "%s: a worker without the lease saved", e.name)

			again, err := store.GetReplicaInitialCopy(ctx, 7)
			require.NoError(t, err)
			require.Equal(t, "/docs/ç ğ ş.txt", again.Cursor)
			require.True(t, again.Counted)
			require.Equal(t, int64(4), again.Done())
			require.Equal(t, int64(12345), again.CopiedBytes)

			// The lease lapses: another worker takes it.
			ok, err = store.ClaimReplicaInitialCopy(ctx, 7, 3, "w2", 1300, 1400)
			require.NoError(t, err)
			require.True(t, ok, "%s: a lapsed lease kept the copy held", e.name)

			// A restart begins it anew and takes the lease from whoever held it.
			restarted, started, err := store.StartReplicaInitialCopy(ctx, 7, 3, 1500, true)
			require.NoError(t, err)
			require.True(t, started)
			require.Equal(t, model.ReplicaCopyPending, restarted.Phase)
			fresh, err := store.GetReplicaInitialCopy(ctx, 7)
			require.NoError(t, err)
			require.Empty(t, fresh.Cursor)
			require.Zero(t, fresh.Done())
			require.Empty(t, fresh.LeaseOwner)
			ok, err = store.SaveReplicaInitialCopy(ctx, got, "w2")
			require.NoError(t, err)
			require.False(t, ok, "%s: the worker of the old run went on writing after the restart", e.name)

			// Another target is another copy.
			_, started, err = store.StartReplicaInitialCopy(ctx, 7, 9, 1600, false)
			require.NoError(t, err)
			require.True(t, started)
			list, err := store.ListReplicaInitialCopies(ctx)
			require.NoError(t, err)
			require.Len(t, list, 1)
			require.Equal(t, int64(9), list[0].TargetID)

			require.NoError(t, store.DeleteReplicaInitialCopy(ctx, 7))
			gone, err := store.GetReplicaInitialCopy(ctx, 7)
			require.NoError(t, err)
			require.Nil(t, gone)
		})
	}
}

// A storage created linked keeps its link on every engine. CreateStorage
// wrote every column but replica_target_id (only UpdateStorage wrote it), so
// a storage made already linked was read back unlinked and nothing written
// to it reached the target (#186).
func TestCreateStorage_KeepsTheReplicationTargetOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			rt, err := store.CreateReplicationTarget(ctx, &model.ReplicationTarget{
				Name: "storage-box", Driver: "local", ConfigJSON: []byte(`{"path":"/srv/box"}`), Mode: "async", Enabled: true,
			})
			require.NoError(t, err)
			tid := rt.ID
			linked, err := store.CreateStorage(ctx, &model.Storage{
				Name: "arsiv", Driver: "local", MountPath: "/arsiv", ConfigJSON: []byte(`{"path":"/srv/arsiv"}`),
				SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true, ReplicaTargetID: &tid,
			})
			require.NoError(t, err)
			require.NotNil(t, linked.ReplicaTargetID, "%s: the created row came back unlinked", e.name)
			require.Equal(t, rt.ID, *linked.ReplicaTargetID)
			again, err := store.GetStorage(ctx, linked.ID)
			require.NoError(t, err)
			require.NotNil(t, again.ReplicaTargetID, "%s: the link was not saved", e.name)
			require.Equal(t, rt.ID, *again.ReplicaTargetID)

			bare, err := store.CreateStorage(ctx, &model.Storage{
				Name: "bare", Driver: "local", MountPath: "/bare", ConfigJSON: []byte(`{"path":"/srv/bare"}`),
				SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true,
			})
			require.NoError(t, err)
			require.Nil(t, bare.ReplicaTargetID, "%s: a storage created without a target got one", e.name)
		})
	}
}

// versionBeforeReplicaWiring is the last migration before 00094. (00093
// belongs to a branch merged beside this one; UpTo stops at whatever of it is
// present.)
const versionBeforeReplicaWiring = 93

// A failure recorded before 00094 survives the upgrade, with storage 0: no
// storage claims it, and the repair resolves it as having nothing to repair.
func TestReplicaFailuresUpgradeOnEveryEngine(t *testing.T) {
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
			ctx := context.Background()
			sqlDB, err := drv.Open(ctx, e.fresh(t, admin))
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })

			goose.SetBaseFS(drv.MigrationsFS())
			defer goose.SetBaseFS(nil)
			require.NoError(t, goose.SetDialect(drv.Dialect()))
			require.NoError(t, goose.UpToContext(ctx, sqlDB, ".", versionBeforeReplicaWiring))
			_, err = sqlDB.ExecContext(ctx,
				`INSERT INTO replica_failures (path, op, error_code, error_msg, attempts) VALUES ('/old.txt', 'write', 'E', 'before', 2)`)
			require.NoError(t, err)

			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: the upgrade", e.name)
			store := drv.NewStore(sqlDB)
			rows, _, err := store.ListReplicaFailures(ctx, true, 10, 0)
			require.NoError(t, err)
			require.Len(t, rows, 1, "%s: the upgrade lost a failure", e.name)
			require.Equal(t, int64(0), rows[0].StorageID)
			require.Equal(t, "/old.txt", rows[0].Path)
			require.Equal(t, 2, rows[0].Attempts)

			// The same path can now fail on a storage too.
			require.NoError(t, store.UpsertReplicaFailure(ctx, 5, "/old.txt", "write", "E", "now"))
			n, err := store.CountUnresolvedReplicaFailures(ctx)
			require.NoError(t, err)
			require.EqualValues(t, 2, n)
			require.NoError(t, store.ResolveReplicaFailure(ctx, 0, "/old.txt", "write"))
			n, err = store.CountUnresolvedReplicaFailures(ctx)
			require.NoError(t, err)
			require.EqualValues(t, 1, n)
		})
	}
}

// Migration 00095 on every engine: one folder per storage, no two storages on
// one target in the same folder (by its lowercased key, accents kept apart on
// MySQL too), the same folder on another target is fine, and a storage's own
// row is replaced, not doubled.
func TestReplicaLinksOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			none, err := store.GetReplicaLink(ctx, 1)
			require.NoError(t, err)
			require.Nil(t, none)

			put := func(sid, tid int64, folder string) error {
				return store.PutReplicaLink(ctx, &model.ReplicaLink{StorageID: sid, TargetID: tid, Folder: folder, FolderKey: strings.ToLower(folder), CreatedUnix: 1000})
			}
			require.NoError(t, put(1, 9, "Arşiv"))
			require.NoError(t, put(2, 9, "Arsiv"), "%s: an accent made two folders one", e.name)
			require.Error(t, put(3, 9, "arşiv"), "%s: two storages on one target got one folder", e.name)
			require.NoError(t, put(3, 10, "arşiv"), "%s: the same folder on another target is another folder", e.name)

			// The storage's own row is replaced.
			require.NoError(t, put(1, 9, "Yeni"))
			got, err := store.GetReplicaLink(ctx, 1)
			require.NoError(t, err)
			require.Equal(t, "Yeni", got.Folder)
			require.Equal(t, "yeni", got.FolderKey)
			require.Equal(t, int64(9), got.TargetID)

			// A refused change leaves the old row.
			require.Error(t, put(1, 9, "ARSIV"))
			kept, err := store.GetReplicaLink(ctx, 1)
			require.NoError(t, err)
			require.NotNil(t, kept, "%s: a refused change took the storage's folder away", e.name)
			require.Equal(t, "Yeni", kept.Folder)

			list, err := store.ListReplicaLinks(ctx)
			require.NoError(t, err)
			require.Len(t, list, 3)

			require.NoError(t, store.DeleteReplicaLink(ctx, 1))
			gone, err := store.GetReplicaLink(ctx, 1)
			require.NoError(t, err)
			require.Nil(t, gone)
		})
	}
}
