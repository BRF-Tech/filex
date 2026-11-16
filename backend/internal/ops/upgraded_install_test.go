package ops_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/storage"

	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
)

// TestTheOperationsListAnswersOnAnInstallUpgradedFromAnOldRelease is #356
// where it was seen: an install made by 0.10.0 (or anything up to 0.26.1) and
// taken to 0.38 or later in place kept a pending_ops without dest_storage_id,
// so every GET /api/files/ops - this ListFor - failed with "no such column"
// while the server answered /healthz. The schema is the one the release left
// (backend/internal/db/testdata/upgrade), the row is one it queued, and the
// start is the one this tree makes: db.Migrate, then the queue's own Migrate.
func TestTheOperationsListAnswersOnAnInstallUpgradedFromAnOldRelease(t *testing.T) {
	for _, version := range []string{"0.10.0", "0.26.1"} {
		t.Run("v"+version, func(t *testing.T) {
			ctx := context.Background()
			drv, err := db.Get("sqlite")
			require.NoError(t, err)
			sqlDB, err := drv.Open(ctx, filepath.Join(t.TempDir(), "upgraded.sqlite"))
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })

			script, err := os.ReadFile(filepath.Join("..", "db", "testdata", "upgrade", "sqlite-v"+version+".sql"))
			require.NoError(t, err)
			_, err = sqlDB.ExecContext(ctx, string(script))
			require.NoError(t, err)
			_, err = sqlDB.ExecContext(ctx, `INSERT INTO pending_ops
				(kind, storage_id, sources_json, dest, total, done, status)
				VALUES ('copy', 3, '["/report.pdf"]', '/archive', 1, 1, 'done')`)
			require.NoError(t, err)

			require.NoError(t, db.Migrate(ctx, drv, sqlDB))
			svc := ops.New(sqlDB, func(int64) (storage.Driver, error) { return nil, nil })
			require.NoError(t, svc.Migrate(ctx))

			all, err := svc.ListFor(ctx, "", ops.Viewer{All: true})
			require.NoError(t, err, "the operations list on an install upgraded from v%s", version)
			require.Len(t, all, 1)
			require.Equal(t, int64(3), all[0].StorageID)
			// Every op before 0.27 stayed inside its storage: the column the
			// repair adds reads 0 there, which the queue reads as "the same
			// storage".
			require.Equal(t, int64(3), all[0].DestStorageID)

			mine, err := svc.ListFor(ctx, "", ops.Viewer{StorageIDs: []int64{3}})
			require.NoError(t, err, "the list a user with one storage asks for")
			require.Len(t, mine, 1)
		})
	}
}
