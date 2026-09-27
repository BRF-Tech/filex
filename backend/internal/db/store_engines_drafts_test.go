package db_test

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// TestDraftsOnEveryEngine walks the drafts table (migration 00064, issue #71)
// on every engine: a draft is recorded, read back by key and by node joined to
// its file's row, counted and listed only while that row is live, kept while
// it is in the trash, dropped with it when the trash purges it (ON DELETE
// CASCADE), and left out of the storage's counts throughout. Written ONCE for
// every engine (db.DraftSQL), so it is measured once for every engine.
func TestDraftsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st, err := store.CreateStorage(ctx, &model.Storage{
				Name: "main", Driver: "local", MountPath: "/data", ConfigJSON: []byte(`{}`),
				SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
			})
			require.NoError(t, err)
			user, err := store.CreateUser(ctx, "owner@example.com", "hash", "user", "tr", "UTC")
			require.NoError(t, err)
			other, err := store.CreateUser(ctx, "other@example.com", "hash", "user", "tr", "UTC")
			require.NoError(t, err)

			mk := func(p string, size int64) *model.Node {
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, Name: path.Base(p), Path: p, PathHash: pathkey.Hash(st.ID, p),
					Type: model.NodeTypeFile, Size: size, Mime: "text/plain",
				})
				require.NoError(t, err)
				return n
			}
			plain := mk("/Documents/report", 100)
			file := mk("/.filex-drafts/1/0123456789abcdef/notes.txt", 7)

			d, err := store.CreateDraft(ctx, &model.Draft{
				Key: "0123456789abcdef", UserID: user.ID, StorageID: st.ID, NodeID: file.ID,
				TargetDir: "Documents", TargetName: "notes.txt", DocType: "txt",
			})
			require.NoError(t, err)
			require.Equal(t, "notes.txt", d.TargetName)
			require.Equal(t, "Documents", d.TargetDir)
			require.Equal(t, file.Path, d.Path, "the draft is read with its file's path")
			require.EqualValues(t, 7, d.Size)
			require.True(t, d.NodeLive)
			require.False(t, d.CreatedAt.IsZero())

			byNode, err := store.GetDraftByNode(ctx, file.ID)
			require.NoError(t, err)
			require.Equal(t, d.ID, byNode.ID)

			list, err := store.ListLiveDrafts(ctx, user.ID)
			require.NoError(t, err)
			require.Len(t, list, 1)
			n, err := store.CountLiveDrafts(ctx, user.ID)
			require.NoError(t, err)
			require.Equal(t, 1, n)
			n, err = store.CountLiveDrafts(ctx, other.ID)
			require.NoError(t, err)
			require.Equal(t, 0, n, "another person's count included somebody's draft")

			// The storage's counts leave the draft out.
			count, size, err := store.StorageStats(ctx, st.ID)
			require.NoError(t, err)
			require.EqualValues(t, 1, count, "a draft was counted as a file of the storage")
			require.EqualValues(t, plain.Size, size)

			// In the trash: not live, not listed, not counted — but kept.
			require.NoError(t, store.SoftDeleteAndRetag(ctx, file.ID,
				"/.filex-trash/1-ab__notes.txt", pathkey.Hash(st.ID, "/.filex-trash/1-ab__notes.txt"), file.Path))
			list, err = store.ListLiveDrafts(ctx, user.ID)
			require.NoError(t, err)
			require.Empty(t, list)
			n, err = store.CountLiveDrafts(ctx, user.ID)
			require.NoError(t, err)
			require.Equal(t, 0, n)
			kept, err := store.GetDraftByKey(ctx, "0123456789abcdef")
			require.NoError(t, err, "a discarded draft's row must survive until the trash purges it")
			require.False(t, kept.NodeLive)

			// The trash purges it: the draft goes with its row.
			require.NoError(t, store.HardDeleteNode(ctx, file.ID))
			_, err = store.GetDraftByKey(ctx, "0123456789abcdef")
			require.True(t, errors.Is(err, sql.ErrNoRows), "the draft outlived its purged file: %v", err)

			// DeleteDraft drops a row by id.
			file2 := mk("/.filex-drafts/1/fedcba9876543210/plan.txt", 1)
			d2, err := store.CreateDraft(ctx, &model.Draft{
				Key: "fedcba9876543210", UserID: user.ID, StorageID: st.ID, NodeID: file2.ID,
				TargetName: "plan.txt",
			})
			require.NoError(t, err)
			require.Equal(t, "", d2.TargetDir, "the storage root is ''")
			require.NoError(t, store.DeleteDraft(ctx, d2.ID))
			_, err = store.GetDraftByKey(ctx, "fedcba9876543210")
			require.True(t, errors.Is(err, sql.ErrNoRows))
		})
	}
}
