package db_test

// Migration 00040, on every engine.
//
// Every account used to be created with timezone 'UTC', and the web app reads
// the account's zone — so an account nobody had ever configured drew its dates
// in UTC while the same explorer embedded elsewhere drew the browser's clock.
// The migration clears the rows that default wrote and leaves a real choice
// alone. Like the uid backfill test beside it, this migrates up to the version
// BEFORE the change, puts rows in, and then upgrades — which is the path an
// existing install takes and the from-nothing migration test cannot see.

import (
	"context"
	"fmt"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
)

// versionBeforeTimezoneUnset is the last migration that predates 00040.
const versionBeforeTimezoneUnset = 39

func TestUserTimezoneUnsetOnEveryEngine(t *testing.T) {
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

			goose.SetBaseFS(drv.MigrationsFS())
			defer goose.SetBaseFS(nil)
			require.NoError(t, goose.SetDialect(drv.Dialect()))
			require.NoError(t, goose.UpToContext(context.Background(), sqlDB, ".", versionBeforeTimezoneUnset))

			before := map[string]string{
				"never-chose@tz.local": "UTC",             // the old default
				"istanbul@tz.local":    "Europe/Istanbul", // a real choice
				"device@tz.local":      "",                // already "this device"
			}
			for email, tz := range before {
				_, err := sqlDB.ExecContext(context.Background(), fmt.Sprintf(
					`INSERT INTO users (email, password_hash, role, locale, timezone) VALUES ('%s','x','user','en','%s')`,
					email, tz))
				require.NoError(t, err, "%s: seeding a pre-upgrade account", e.name)
			}

			require.NoError(t, db.Migrate(context.Background(), drv, sqlDB))

			rows, err := sqlDB.QueryContext(context.Background(), `SELECT email, timezone FROM users`)
			require.NoError(t, err)
			defer rows.Close()
			after := map[string]string{}
			for rows.Next() {
				var email, tz string
				require.NoError(t, rows.Scan(&email, &tz))
				after[email] = tz
			}
			require.NoError(t, rows.Err())

			require.Equal(t, "", after["never-chose@tz.local"],
				"%s: an account carrying the old 'UTC' default still has it — every date it reads is drawn in UTC", e.name)
			require.Equal(t, "Europe/Istanbul", after["istanbul@tz.local"],
				"%s: the upgrade overwrote a zone somebody chose", e.name)
			require.Equal(t, "", after["device@tz.local"], "%s", e.name)
		})
	}
}
