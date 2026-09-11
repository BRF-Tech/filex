package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// TestStoreWritePathsOnEveryEngine walks the writes an install performs in its
// first five minutes — register a storage, create the admin, record a file,
// save a setting, configure an external service, tag something, thumbnail it,
// share it — on every engine we tell operators they can install on.
//
// It exists because "the migrations run" is a much weaker claim than it looks:
// MySQL borrows the SQLite Store wholesale, so SQLite-flavoured SQL (upserts,
// most of all) reaches a MySQL server unchanged and fails at the moment an
// operator saves something, not at boot.
func TestStoreWritePathsOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st, err := store.CreateStorage(ctx, &model.Storage{
				Name:          "local",
				Driver:        "local",
				MountPath:     "/data/files",
				ConfigJSON:    json.RawMessage(`{"root":"/data/files"}`),
				SyncMode:      model.SyncModePoll,
				SyncIntervalS: 900,
				Enabled:       true,
			})
			require.NoError(t, err, "create storage")

			user, err := store.CreateUser(ctx, "admin@example.com", "hash", "admin", "en", "UTC")
			require.NoError(t, err, "create user")

			node, err := store.CreateNode(ctx, &model.Node{
				StorageID: st.ID,
				Name:      "quarterly.ods",
				Path:      "/quarterly.ods",
				PathHash:  pathkey.Hash(st.ID, "/quarterly.ods"),
				Type:      model.NodeTypeFile,
				Size:      4096,
				Mime:      "application/vnd.oasis.opendocument.spreadsheet",
			})
			require.NoError(t, err, "create node")

			// Settings: the upsert every admin save goes through, run twice so
			// the conflict branch is the one under test.
			require.NoError(t, store.UpsertSetting(ctx, "site.name", "filex"), "insert setting")
			require.NoError(t, store.UpsertSetting(ctx, "site.name", "filex renamed"), "update setting")
			got, err := store.GetSetting(ctx, "site.name")
			require.NoError(t, err)
			require.Equal(t, "filex renamed", got)

			// External services: what issue #17's admin page writes.
			now := time.Now().UTC().Truncate(time.Second)
			require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true,
				"https://office.example.com", "sealed", `{}`, now, "ok"), "insert external service")
			require.NoError(t, store.UpsertExternalService(ctx, "onlyoffice", true,
				"https://office.example.com", "sealed-2", `{}`, now, "ok"), "update external service")
			svc, err := store.GetExternalService(ctx, "onlyoffice")
			require.NoError(t, err)
			require.Equal(t, "sealed-2", svc.SecretEnc)

			// Thumbnails and tags: upserts on the file-listing hot path.
			require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{
				NodeID: node.ID, State: "pending",
			}), "insert thumbnail")
			require.NoError(t, store.UpsertThumbnail(ctx, &model.Thumbnail{
				NodeID: node.ID, State: "ready", Width: 320, Height: 200, GeneratedAt: &now,
			}), "update thumbnail")

			require.NoError(t, store.SetNodeTags(ctx, node.ID, []string{"reports", "q3"}), "set tags")
			require.NoError(t, store.SetNodeTags(ctx, node.ID, []string{"reports"}), "replace tags")

			require.NoError(t, store.SetUserNodeMeta(ctx, user.ID, node.ID, "star", "1"), "insert user meta")
			require.NoError(t, store.SetUserNodeMeta(ctx, user.ID, node.ID, "star", "0"), "update user meta")

			share, err := store.CreateShare(ctx, &model.Share{
				NodeID:    node.ID,
				Token:     "0123456789abcdef0123456789abcdef",
				CreatedBy: &user.ID,
			})
			require.NoError(t, err, "create share")
			require.NotZero(t, share.ID)

			// And read the file back the way a listing does.
			reread, err := store.GetNodeByPath(ctx, st.ID, pathkey.Hash(st.ID, "/quarterly.ods"))
			require.NoError(t, err)
			require.Equal(t, node.ID, reread.ID)
		})
	}
}
