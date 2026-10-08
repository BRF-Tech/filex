package db_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// Why a link will not open, kept in the catalogue (migration 00098), on every
// engine: the sync records the driver's reason for a link row, a listing reads
// it back for its link rows, and the reason the row already carries is not
// written again on every pass.
func TestLinkStateOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)
			mk := func(p string, typ model.NodeType) *model.Node {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, Name: lastSegment(p), Path: p,
					PathHash: pathkey.Hash(st.ID, p), StorageKey: p, Type: typ,
				})
				require.NoError(t, err, "%s: create %s", e.name, p)
				return n
			}
			archive := mk("/archive", model.NodeTypeSymlink)
			gone := mk("/gone", model.NodeTypeSymlink)
			file := mk("/README.md", model.NodeTypeFile)
			ids := []int64{archive.ID, gone.ID, file.ID}

			got, err := store.NodeLinkStates(ctx, ids)
			require.NoError(t, err, e.name)
			require.Empty(t, got, "%s: a new row carries no reason", e.name)

			changed, err := store.SetNodeLinkState(ctx, archive.ID, "outside_root")
			require.NoError(t, err, e.name)
			require.True(t, changed, "%s: the first reason changes the row", e.name)
			changed, err = store.SetNodeLinkState(ctx, archive.ID, "outside_root")
			require.NoError(t, err, e.name)
			require.False(t, changed, "%s: the same reason again writes nothing", e.name)
			changed, err = store.SetNodeLinkState(ctx, gone.ID, "broken")
			require.NoError(t, err, e.name)
			require.True(t, changed, e.name)

			got, err = store.NodeLinkStates(ctx, ids)
			require.NoError(t, err, e.name)
			require.Equal(t, map[int64]string{archive.ID: "outside_root", gone.ID: "broken"}, got, e.name)

			// A reason that changed on the storage replaces the recorded one.
			changed, err = store.SetNodeLinkState(ctx, gone.ID, "outside_root")
			require.NoError(t, err, e.name)
			require.True(t, changed, e.name)

			// An empty reason clears it: the driver gave none.
			changed, err = store.SetNodeLinkState(ctx, archive.ID, "")
			require.NoError(t, err, e.name)
			require.True(t, changed, "%s: clearing a recorded reason changes the row", e.name)
			changed, err = store.SetNodeLinkState(ctx, archive.ID, "")
			require.NoError(t, err, e.name)
			require.False(t, changed, "%s: clearing nothing writes nothing", e.name)

			got, err = store.NodeLinkStates(ctx, ids)
			require.NoError(t, err, e.name)
			require.Equal(t, map[int64]string{gone.ID: "outside_root"}, got, e.name)

			// What a storage plugin says is kept within bounds.
			_, err = store.SetNodeLinkState(ctx, archive.ID, strings.Repeat("ş", db.LinkStateMax+40))
			require.NoError(t, err, e.name)
			got, err = store.NodeLinkStates(ctx, []int64{archive.ID})
			require.NoError(t, err, e.name)
			require.Equal(t, strings.Repeat("ş", db.LinkStateMax), got[archive.ID], "%s: clipped on a character boundary", e.name)

			// The common node read is untouched by the column.
			n, err := store.GetNode(ctx, gone.ID)
			require.NoError(t, err, e.name)
			require.Equal(t, model.NodeTypeSymlink, n.Type, e.name)

			// No ids, no query.
			got, err = store.NodeLinkStates(ctx, nil)
			require.NoError(t, err, e.name)
			require.Empty(t, got, e.name)
		})
	}
}

// versionBeforeLinkState is the last migration before 00098.
const versionBeforeLinkState = 97

// The upgrade to 00098 on a catalogue that already holds a link row: the row
// reads back with no reason (NULL - the next sync of its folder fills it in),
// a reason can be recorded on it, `filex migrate down` takes the column away
// and the retry brings it back empty.
func TestLinkStateMigrationGoesUpDownAndUpOnEveryEngine(t *testing.T) {
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
			require.NoError(t, goose.UpToContext(ctx, sqlDB, ".", versionBeforeLinkState))

			// The catalogue the upgrade finds, written as 0.53 wrote it.
			store := drv.NewStore(sqlDB)
			st := createEngineStorage(t, store)
			_, err = sqlDB.ExecContext(ctx, fmt.Sprintf(
				`INSERT INTO nodes (storage_id, name, path, path_hash, type, size, sync_state)
				 VALUES (%d, 'archive', '/archive', '%s', 'symlink', 0, 'synced')`,
				st.ID, pathkey.Hash(st.ID, "/archive")))
			require.NoError(t, err, "%s: a link row from before 00098", e.name)
			link, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/archive"))
			require.NoError(t, err, e.name)

			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: the upgrade to 00098", e.name)
			got, err := store.NodeLinkStates(ctx, []int64{link.ID})
			require.NoError(t, err, e.name)
			require.Empty(t, got, "%s: a link row from before the upgrade has no reason yet", e.name)
			changed, err := store.SetNodeLinkState(ctx, link.ID, "outside_root")
			require.NoError(t, err, e.name)
			require.True(t, changed, e.name)
			got, err = store.NodeLinkStates(ctx, []int64{link.ID})
			require.NoError(t, err, e.name)
			require.Equal(t, "outside_root", got[link.ID], e.name)

			// ⚠ db.Migrate clears goose's base FS on its way out.
			goose.SetBaseFS(drv.MigrationsFS())
			require.NoError(t, goose.DownToContext(ctx, sqlDB, ".", versionBeforeLinkState), "%s: down", e.name)
			_, err = sqlDB.ExecContext(ctx, `SELECT link_state FROM nodes`)
			require.Error(t, err, "%s: down drops the column", e.name)
			n, err := store.GetNode(ctx, link.ID)
			require.NoError(t, err, "%s: the row reads without the column", e.name)
			require.Equal(t, model.NodeTypeSymlink, n.Type, e.name)

			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: up again", e.name)
			got, err = store.NodeLinkStates(ctx, []int64{link.ID})
			require.NoError(t, err, e.name)
			require.Empty(t, got, "%s: after down and up the reason is gone until the next sync", e.name)
		})
	}
}
