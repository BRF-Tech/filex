package db_test

// The uid backfill, on every engine.
//
// Migration 00037 gives every existing storage the address that does not move,
// and it does it in SQL — three hand-written expressions, one per dialect,
// because none of the three engines spells "make me a uuid" the same way. A
// backfill that produced one shared value, or an empty string, would be worse
// than no column at all: the UNIQUE index would reject the second row on one
// engine and on another every storage would answer to the same address.
//
// So this migrates up to the version BEFORE the column exists, puts real rows
// in, and then applies it — which is what an upgrade does and what the
// from-nothing migration test can never exercise.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
)

// versionBeforeUID is the last migration that predates the uid column.
const versionBeforeUID = 36

var uuidShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestStorageUIDBackfillOnEveryEngine(t *testing.T) {
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

			// Up to the state an install running the previous release is in.
			goose.SetBaseFS(drv.MigrationsFS())
			defer goose.SetBaseFS(nil)
			require.NoError(t, goose.SetDialect(drv.Dialect()))
			require.NoError(t, goose.UpToContext(context.Background(), sqlDB, ".", versionBeforeUID))

			for _, name := range []string{"Garage S3", "ps-hot", "archive"} {
				_, err := sqlDB.ExecContext(context.Background(), fmt.Sprintf(
					`INSERT INTO storages (name, driver, mount_path, config_json, sync_mode, sync_interval_s, enabled, read_only)
					 VALUES ('%s','local','/x','{}','ondemand',900,%s,%s)`,
					name, boolLit(e.driver, true), boolLit(e.driver, false)))
				require.NoError(t, err, "%s: seeding a pre-upgrade storage", e.name)
			}

			// The upgrade itself.
			require.NoError(t, db.Migrate(context.Background(), drv, sqlDB))

			rows, err := sqlDB.QueryContext(context.Background(),
				`SELECT name, COALESCE(uid,'') FROM storages ORDER BY id`)
			require.NoError(t, err)
			defer rows.Close()

			seen := map[string]string{}
			for rows.Next() {
				var name, uid string
				require.NoError(t, rows.Scan(&name, &uid))
				require.NotEmpty(t, uid, "%s: %q came out of the upgrade with no uid", e.name, name)
				require.Regexp(t, uuidShape, uid,
					"%s: %q got %q, which is not the shape the protocols recognise", e.name, name, uid)
				require.NotContains(t, seen, uid,
					"%s: %q and %q share a uid — the backfill evaluated once for the whole table",
					e.name, seen[uid], name)
				seen[uid] = name
			}
			require.NoError(t, rows.Err())
			require.Len(t, seen, 3, "%s: expected three storages", e.name)

			// And the index really is unique, so a future bug cannot quietly
			// hand two storages the same address.
			var uid string
			require.NoError(t, sqlDB.QueryRowContext(context.Background(),
				`SELECT uid FROM storages ORDER BY id`).Scan(&uid))
			_, err = sqlDB.ExecContext(context.Background(), fmt.Sprintf(
				`INSERT INTO storages (name, driver, mount_path, config_json, sync_mode, sync_interval_s, enabled, read_only, uid)
				 VALUES ('collides','local','/x','{}','ondemand',900,%s,%s,'%s')`,
				boolLit(e.driver, true), boolLit(e.driver, false), uid))
			require.Error(t, err, "%s: a duplicate uid was accepted", e.name)
		})
	}
}

// envOrSkip mirrors openMigrated's skip so this test needs no server it was
// not given one for.
func envOrSkip(t *testing.T, e engine) string {
	t.Helper()
	dsn := os.Getenv(e.dsnEnv)
	if dsn == "" {
		t.Skipf("%s not exercised: set %s to a throwaway server", e.name, e.dsnEnv)
	}
	return dsn
}

// boolLit spells a boolean the way the engine wants it in a literal INSERT.
// MySQL takes 1/0 and PostgreSQL takes true/false; SQLite takes either.
func boolLit(driver string, v bool) string {
	if driver == "mysql" {
		if v {
			return "1"
		}
		return "0"
	}
	if v {
		return "true"
	}
	return "false"
}
