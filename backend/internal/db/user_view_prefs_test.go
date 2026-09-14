package db_test

// Per-user view preferences, on every engine — the upgrade path and the
// round trip.
//
// Migration 00039 adds `user_view_prefs`: one JSON document per user saying
// how that person left each folder. There is no backfill to prove (a new table
// starts empty and that is the correct answer for every existing account), so
// what this measures instead is the two things that HAVE gone wrong in this
// repo before, both of them silently:
//
//   1. A migration that exists in two dialects out of three. `TestMigrations…`
//      and `TestSchemaParity…` already catch the table's absence, but neither
//      of them WRITES a row — and MySQL was missing storages.replica_target_id
//      for months with green migrations, because the first statement to fail
//      was the first INSERT an operator ran (issue #19).
//   2. A statement that only works on SQLite. The upsert here goes through
//      `Store.upsert()`, which rewrites `ON CONFLICT … excluded.x` into
//      `ON DUPLICATE KEY UPDATE … VALUES(x)` at runtime for MySQL — a
//      translation nothing tests unless something round-trips through it on a
//      real MySQL server. Writing the same user TWICE is the point: the second
//      write is the one that takes the conflict branch.
//
// And it is done as an UPGRADE — migrate to the version before, insert a real
// user, then migrate up — because that is what an existing install does, and
// the from-nothing migration test can never exercise it.

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
)

// versionBeforeViewPrefs is the last migration that predates the table.
const versionBeforeViewPrefs = 38

func TestUserViewPrefsOnEveryEngine(t *testing.T) {
	ctx := context.Background()
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
			sqlDB, err := drv.Open(ctx, e.fresh(t, admin))
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })

			// The state an install running the previous release is in.
			goose.SetBaseFS(drv.MigrationsFS())
			defer goose.SetBaseFS(nil)
			require.NoError(t, goose.SetDialect(drv.Dialect()))
			require.NoError(t, goose.UpToContext(ctx, sqlDB, ".", versionBeforeViewPrefs))

			// A real account, created before the table existed.
			_, err = sqlDB.ExecContext(ctx,
				`INSERT INTO users (email, password_hash, role, enabled) VALUES ('viewprefs@example.test','x','user',`+
					boolLit(e.driver, true)+`)`)
			require.NoError(t, err, "%s: seeding a pre-upgrade user", e.name)

			// The upgrade itself.
			require.NoError(t, db.Migrate(ctx, drv, sqlDB))

			store := drv.NewStore(sqlDB)

			var uid int64
			require.NoError(t, sqlDB.QueryRowContext(ctx,
				`SELECT id FROM users WHERE email='viewprefs@example.test'`).Scan(&uid))

			// Nothing stored is "" and NOT an error: it is the ordinary state
			// of every account on its first day, and a caller made to tell
			// sql.ErrNoRows apart from an empty column gets it wrong once.
			got, err := store.GetUserViewPrefs(ctx, uid)
			require.NoError(t, err, "%s: reading before anything was written", e.name)
			require.Equal(t, "", got, "%s: an account with no document must read as empty", e.name)

			first := `{"f":{"thumbfix/Photos":{"v":"gallery","k":"name","d":"asc","t":1}}}`
			require.NoError(t, store.SetUserViewPrefs(ctx, uid, first), "%s: first write", e.name)
			got, err = store.GetUserViewPrefs(ctx, uid)
			require.NoError(t, err)
			require.Equal(t, first, got, "%s: the document did not survive the round trip", e.name)

			// ⚠ THE SECOND WRITE IS THE TEST. The first is an INSERT on every
			// engine; only this one takes the conflict branch, which is the
			// clause `Store.upsert()` rewrites for MySQL and the one place a
			// SQLite-only statement would still be hiding.
			second := `{"f":{"thumbfix/Photos":{"v":"list","k":"modified","d":"desc","t":2}},"c":{"hidden":["owner"]}}`
			require.NoError(t, store.SetUserViewPrefs(ctx, uid, second), "%s: overwriting the document", e.name)
			got, err = store.GetUserViewPrefs(ctx, uid)
			require.NoError(t, err)
			require.Equal(t, second, got,
				"%s: the second write did not replace the first — the upsert took the wrong branch", e.name)

			// One row per user, not one per write. A conflict clause that
			// silently inserted instead would leave the read above passing
			// (it takes whichever row it finds) and the table growing forever.
			var n int
			require.NoError(t, sqlDB.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM user_view_prefs`).Scan(&n))
			require.Equal(t, 1, n, "%s: expected exactly one document row", e.name)

			// Deleting the account takes its arrangements with it. Without the
			// cascade these rows would outlive every user and nothing would
			// ever collect them.
			_, err = sqlDB.ExecContext(ctx, `DELETE FROM users WHERE email='viewprefs@example.test'`)
			require.NoError(t, err, "%s: deleting the account", e.name)
			require.NoError(t, sqlDB.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM user_view_prefs`).Scan(&n))
			require.Equal(t, 0, n, "%s: the document outlived the user it belongs to", e.name)
		})
	}
}
