package db_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// versionBeforePathIndex is the last migration before 00083 (the path index).
const versionBeforePathIndex = 82

// TestThePathIndexMigrationGoesUpDownAndUpOnEveryEngine is the upgrade an
// install makes to 00083, the rollback `filex migrate down` makes, and the
// retry after it - on a catalogue that already holds a path longer than a
// PostgreSQL B-tree row can be (2704 bytes). An index on the whole path would
// make the upgrade itself fail there; the index on left(path, 512) must not.
// After each step the subtree questions answer the same, with the index and
// without it.
func TestThePathIndexMigrationGoesUpDownAndUpOnEveryEngine(t *testing.T) {
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
			require.NoError(t, goose.UpToContext(ctx, sqlDB, ".", versionBeforePathIndex))
			require.False(t, hasPathIndex(t, e.name, sqlDB), "%s: no path index before 00083", e.name)

			// The catalogue the upgrade finds: a folder deeper than any index
			// key, what is below it, and a look-alike past the key.
			store := drv.NewStore(sqlDB)
			st := createEngineStorage(t, store)
			deep := subtreeDeepDir
			seedTreeRow(t, store, st.ID, deep, model.NodeTypeDirectory)
			seedTreeRow(t, store, st.ID, deep+"/derin.txt", model.NodeTypeFile)
			seedTreeRow(t, store, st.ID, deep+"/alt/daha-derin.txt", model.NodeTypeFile)
			seedTreeRow(t, store, st.ID, deep+"x/komşu.txt", model.NodeTypeFile)

			answers := func(step string) {
				t.Helper()
				n, err := store.CountLiveNodesUnder(ctx, st.ID, deep)
				require.NoError(t, err, "%s: %s: count", e.name, step)
				require.EqualValues(t, 2, n, "%s: %s: the deep folder holds two rows", e.name, step)
				has, err := store.HasLiveNodesUnder(ctx, st.ID, deep)
				require.NoError(t, err, "%s: %s: has", e.name, step)
				require.True(t, has, "%s: %s", e.name, step)
				got, err := store.ListNodesUnder(ctx, st.ID, deep, false)
				require.NoError(t, err, "%s: %s: list", e.name, step)
				require.Equal(t, []string{deep, deep + "/derin.txt", deep + "/alt/daha-derin.txt"}, pathsInOrder(got),
					"%s: %s: the folder and what is below it, not its look-alike", e.name, step)
			}
			answers("before 00083")

			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: the upgrade to 00083 on a catalogue with a 5 KB path", e.name)
			require.True(t, hasPathIndex(t, e.name, sqlDB), "%s: 00083 made the path index", e.name)
			answers("after 00083")

			// ⚠ db.Migrate clears goose's base FS on its way out.
			goose.SetBaseFS(drv.MigrationsFS())
			require.NoError(t, goose.DownToContext(ctx, sqlDB, ".", versionBeforePathIndex), "%s: down", e.name)
			require.False(t, hasPathIndex(t, e.name, sqlDB), "%s: down removed the path index", e.name)
			answers("after down")

			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: up again", e.name)
			require.True(t, hasPathIndex(t, e.name, sqlDB), "%s: up again made the path index", e.name)
			answers("after up again")
		})
	}
}

// hasPathIndex reports whether nodes carries idx_nodes_storage_path, as the
// engine's own catalogue says.
func hasPathIndex(t *testing.T, engine string, sqlDB *sql.DB) bool {
	t.Helper()
	var q string
	switch engine {
	case "sqlite":
		q = `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND tbl_name='nodes' AND name='idx_nodes_storage_path'`
	case "postgres":
		q = `SELECT COUNT(*) FROM pg_indexes WHERE schemaname=current_schema() AND tablename='nodes' AND indexname='idx_nodes_storage_path'`
	case "mysql":
		q = `SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='nodes' AND index_name='idx_nodes_storage_path'`
	default:
		t.Fatalf("no index reader for engine %q", engine)
	}
	var n int
	require.NoError(t, sqlDB.QueryRow(q).Scan(&n), "%s: read the index list", engine)
	return n > 0
}
