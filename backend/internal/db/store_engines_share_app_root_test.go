package db_test

// The root a link was opened under (migration 00082), on every engine.
//
// A link an app job of a `root:` token opens records that root, and the
// visitor's job on it is held to it. A link from before the migration - and
// one opened with no root - reads as "", which holds the visitor's job to no
// root, as before. Down removes the column and up brings it back, empty.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// versionBeforeShareAppRoot is the last migration before 00082.
const versionBeforeShareAppRoot = 81

func TestShareAppRootOnEveryEngine(t *testing.T) {
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

			// A database from before 00082, with a link in it.
			goose.SetBaseFS(drv.MigrationsFS())
			defer goose.SetBaseFS(nil)
			require.NoError(t, goose.SetDialect(drv.Dialect()))
			require.NoError(t, goose.UpToContext(ctx, sqlDB, ".", versionBeforeShareAppRoot))
			store := drv.NewStore(sqlDB)
			st, err := store.CreateStorage(ctx, &model.Storage{Name: "main", Driver: "local", MountPath: "/main", Enabled: true, ConfigJSON: json.RawMessage(`{}`)})
			require.NoError(t, err)
			node, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, Name: "a.txt", Path: "/docs/a.txt", PathHash: pathkey.Hash(st.ID, "/docs/a.txt"), Type: model.NodeTypeFile})
			require.NoError(t, err)
			const oldTok, newTok = "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"
			_, err = sqlDB.ExecContext(ctx, fmt.Sprintf(`INSERT INTO shares (node_id, token, kind) VALUES (%d, '%s', 'download')`, node.ID, oldTok))
			require.NoError(t, err)

			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: up", e.name)
			old, err := store.GetShareByToken(ctx, oldTok)
			require.NoError(t, err)
			require.Equal(t, "", old.AppRoot, "%s: a link from before reads as no root", e.name)

			made, err := store.CreateShare(ctx, &model.Share{NodeID: node.ID, Token: newTok, Kind: model.ShareKindDownload, AppRoot: "main://projeler/acme"})
			require.NoError(t, err)
			got, err := store.GetShareByToken(ctx, newTok)
			require.NoError(t, err)
			require.Equal(t, "main://projeler/acme", got.AppRoot, "%s: by token", e.name)
			got, err = store.GetShareByID(ctx, made.ID)
			require.NoError(t, err)
			require.Equal(t, "main://projeler/acme", got.AppRoot, "%s: by id", e.name)
			byNode, err := store.ListSharesByNode(ctx, node.ID)
			require.NoError(t, err)
			require.Len(t, byNode, 2)

			// Down: the column goes; up again: it comes back, empty.
			// ⚠ db.Migrate clears goose's base FS on its way out.
			goose.SetBaseFS(drv.MigrationsFS())
			require.NoError(t, goose.DownToContext(ctx, sqlDB, ".", versionBeforeShareAppRoot), "%s: down", e.name)
			_, err = sqlDB.ExecContext(ctx, `SELECT app_root FROM shares`)
			require.Error(t, err, "%s: down drops the column", e.name)
			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: up again", e.name)
			got, err = store.GetShareByToken(ctx, newTok)
			require.NoError(t, err)
			require.Equal(t, "", got.AppRoot, "%s: after down and up the link reads as no root", e.name)
		})
	}
}
