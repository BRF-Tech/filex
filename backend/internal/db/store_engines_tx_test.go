package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
)

// Store.WithTx on every engine (issue #70): the catalogue writes a folder's
// listing in one transaction, through the store the sync worker is really
// handed (the quota wrapper, whose quota service is built on the raw store),
// and relies on four things this pins:
//
//   - every statement of every method run with fn's context is part of the
//     transaction — the wrapper's own reads and the quota service's UPDATE
//     too. On SQLite the pool has ONE connection and the transaction holds it,
//     so a statement that went to the pool instead would wait for it until the
//     deadline below;
//   - a write is read back inside it, the folder's state row included, with
//     the timestamp the delete pass compares exactly;
//   - fn's error rolls everything back, also after a refused INSERT (which on
//     PostgreSQL has already ended the transaction);
//   - a method that commits on its own refuses to run inside (ErrNestedTx)
//     rather than wait for the connection.
func TestCatalogueTransactionOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			raw := drv.NewStore(sqlDB)
			store := quotastore.New(raw)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			st := createEngineStorage(t, raw)
			h := func(p string) string { return pathkey.Hash(st.ID, p) }
			user, err := raw.CreateUser(ctx, "sahip@example.com", "hash", "user", "tr", "Europe/Istanbul")
			require.NoError(t, err)

			node := func(ctx context.Context, parent *int64, p string, typ model.NodeType, size int64) (*model.Node, error) {
				return store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, ParentID: parent, Name: path.Base(p), Path: p, PathHash: h(p), StorageKey: p,
					Type: typ, Size: size, SyncState: model.SyncStateSynced,
				})
			}
			dir, err := node(ctx, nil, "/klasör", model.NodeTypeDirectory, 0)
			require.NoError(t, err)
			owned, err := node(quotastore.WithOwner(ctx, user.ID), &dir.ID, "/klasör/eski.txt", model.NodeTypeFile, 10)
			require.NoError(t, err)
			before, err := raw.GetUser(ctx, user.ID)
			require.NoError(t, err)

			// The scanner's identity: nobody.
			sys := quotastore.WithActor(quotastore.WithOwner(ctx, 0), 0)
			stamp := time.Now().UTC().Truncate(time.Second)
			require.NoError(t, store.WithTx(sys, func(ctx context.Context) error {
				require.True(t, db.InTx(ctx, sqlDB), "fn runs with the transaction in its context")
				for _, p := range []string{"/klasör/yeni-1.txt", "/klasör/yeni-2.txt", "/klasör/alt"} {
					typ := model.NodeTypeFile
					if p == "/klasör/alt" {
						typ = model.NodeTypeDirectory
					}
					if _, err := node(ctx, &dir.ID, p, typ, 5); err != nil {
						return err
					}
				}
				got, err := store.GetNodeByPath(ctx, st.ID, h("/klasör/yeni-1.txt"))
				require.NoError(t, err)
				require.NotNil(t, got, "a row written in the transaction is read back in it")

				// Drift found by the scanner: the wrapper reads the row, writes
				// it, and bills its owner the difference through the quota
				// service built on the RAW store.
				if err := store.UpdateNodeMeta(ctx, owned.ID, 25, "text/plain", "e2", time.Now().UTC()); err != nil {
					return err
				}
				if err := store.TouchNodeSeen(ctx, dir.ID); err != nil {
					return err
				}
				if err := store.DiscoverCatalogueFolders(ctx, st.ID, []model.CatalogueFolder{
					{PathHash: h("/klasör/alt"), Path: "/klasör/alt", Depth: 2},
				}); err != nil {
					return err
				}
				if err := store.RecordCatalogueFolder(ctx, &model.CatalogueFolder{
					StorageID: st.ID, PathHash: h("/klasör"), Path: "/klasör", Depth: 1,
					State: model.FolderCatalogued, ReconciledAt: &stamp, Entries: 4,
				}); err != nil {
					return err
				}
				row, err := store.GetCatalogueFolder(ctx, st.ID, h("/klasör"))
				require.NoError(t, err)
				require.NotNil(t, row)
				require.True(t, row.ReconciledAt.Equal(stamp), "the delete pass's gate reads its own listing's stamp back: %v", row.ReconciledAt)
				kids, err := store.ListNodesByParent(ctx, st.ID, &dir.ID)
				require.NoError(t, err)
				require.Len(t, kids, 4)

				// The rest of what a folder's transaction may run: the lookups
				// and writes of a first catalogue, a settled upload, a backfilled
				// mtime, and the delete pass removing a folder and its subtree.
				yeni2, err := store.GetNodeByPathIncludingDeleted(ctx, st.ID, h("/klasör/yeni-2.txt"))
				require.NoError(t, err)
				require.NotNil(t, yeni2)
				mtime := time.Now().UTC().Truncate(time.Second)
				if err := store.SetNodeMtime(ctx, yeni2.ID, &mtime); err != nil {
					return err
				}
				if _, err := store.GetStagedUploadByNode(ctx, yeni2.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				if err := store.SetNodeTransferState(ctx, yeni2.ID, model.TransferStateStored); err != nil {
					return err
				}
				if got, err := store.GetNode(ctx, yeni2.ID); err != nil || got.BackendMtime == nil {
					return fmt.Errorf("the mtime written in the transaction reads back: %v %v", got, err)
				}
				altRow, err := store.GetNodeByPath(ctx, st.ID, h("/klasör/alt"))
				require.NoError(t, err)
				if _, err := node(ctx, &altRow.ID, "/klasör/alt/iç.txt", model.NodeTypeFile, 1); err != nil {
					return err
				}
				below, err := store.ListNodesUnder(ctx, st.ID, "/klasör/alt", false)
				require.NoError(t, err)
				require.Len(t, below, 2, "the folder and its file")
				for _, n := range below {
					if err := store.SoftDeleteNode(ctx, n.ID); err != nil {
						return err
					}
				}
				return store.DeleteCatalogueFoldersUnder(ctx, st.ID, "/klasör/alt")
			}), "the folder's writes commit")

			for _, p := range []string{"/klasör/yeni-1.txt", "/klasör/yeni-2.txt"} {
				got, err := raw.GetNodeByPath(ctx, st.ID, h(p))
				require.NoError(t, err)
				require.NotNil(t, got, "%s is committed", p)
			}
			for _, p := range []string{"/klasör/alt", "/klasör/alt/iç.txt"} {
				_, err := raw.GetNodeByPath(ctx, st.ID, h(p))
				require.ErrorIs(t, err, sql.ErrNoRows, "%s was removed in the same transaction", p)
			}
			alt, err := raw.GetCatalogueFolder(ctx, st.ID, h("/klasör/alt"))
			require.NoError(t, err)
			require.Nil(t, alt, "and its folder state with it")
			after, err := raw.GetUser(ctx, user.ID)
			require.NoError(t, err)
			require.Equal(t, before.UsageBytes+15, after.UsageBytes, "the owner's usage moved with the drift, in the same transaction")

			// A refused INSERT, and fn's error: nothing of it stays.
			err = store.WithTx(sys, func(ctx context.Context) error {
				if _, err := node(ctx, &dir.ID, "/klasör/geri.txt", model.NodeTypeFile, 1); err != nil {
					return err
				}
				_, err := node(ctx, &dir.ID, "/klasör/yeni-1.txt", model.NodeTypeFile, 1)
				require.Error(t, err, "the live row's unique index refuses a second one")
				return err
			})
			require.Error(t, err)
			_, err = raw.GetNodeByPath(ctx, st.ID, h("/klasör/geri.txt"))
			require.ErrorIs(t, err, sql.ErrNoRows, "rolled back")

			// Nested: the inner call joins the outer transaction.
			require.NoError(t, store.WithTx(sys, func(outer context.Context) error {
				return store.WithTx(outer, func(inner context.Context) error {
					_, err := node(inner, &dir.ID, "/klasör/iç.txt", model.NodeTypeFile, 1)
					return err
				})
			}))
			in, err := raw.GetNodeByPath(ctx, st.ID, h("/klasör/iç.txt"))
			require.NoError(t, err)
			require.NotNil(t, in)

			// A method with a transaction of its own refuses to nest.
			err = store.WithTx(sys, func(ctx context.Context) error {
				return store.LinkNodeTags(ctx, in.ID, nil, nil)
			})
			require.ErrorIs(t, err, db.ErrNestedTx)
		})
	}
}
