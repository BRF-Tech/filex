package db_test

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
)

// versionBeforeLDAPGroups is the last migration before 00084 (LDAP groups and
// where an account comes from).
const versionBeforeLDAPGroups = 83

// TestLDAPGroupsMigrationLabelsTheAccountsItFinds is the upgrade an install
// from 0.51 makes to 00084, and back down and up again, on every engine: an
// account with a password here is 'local', one with an OIDC subject 'sso',
// and one with neither stays unlabelled (empty) - a directory, a header proxy or
// an administrator made it, and its next sign-in says which. Labelled
// 'local', a second LDAP directory's people (or a tenant's own directory's)
// would no longer be signed in by it after the upgrade.
func TestLDAPGroupsMigrationLabelsTheAccountsItFinds(t *testing.T) {
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
			require.NoError(t, goose.UpToContext(ctx, sqlDB, ".", versionBeforeLDAPGroups))

			insert := `INSERT INTO users (email, password_hash, oidc_subject) VALUES (?, ?, ?)`
			if e.name == "postgres" {
				insert = `INSERT INTO users (email, password_hash, oidc_subject) VALUES ($1, $2, $3)`
			}
			rows := []struct {
				email   string
				hash    any
				subject any
				want    string
			}{
				{"local@example.test", "$2a$10$hash", nil, "local"},
				{"sso@example.test", nil, "sub-1", "sso"},
				{"sso-empty-hash@example.test", "", "sub-2", "sso"},
				{"directory@example.test", nil, nil, ""},
				{"directory-empty@example.test", "", "", ""},
			}
			for _, r := range rows {
				_, err := sqlDB.ExecContext(ctx, insert, r.email, r.hash, r.subject)
				require.NoError(t, err, "%s: seed %s", e.name, r.email)
			}
			check := func(step string) {
				t.Helper()
				q := `SELECT auth_source FROM users WHERE email = ?`
				if e.name == "postgres" {
					q = `SELECT auth_source FROM users WHERE email = $1`
				}
				for _, r := range rows {
					var got string
					require.NoError(t, sqlDB.QueryRowContext(ctx, q, r.email).Scan(&got), "%s: %s: %s", e.name, step, r.email)
					require.Equal(t, r.want, got, "%s: %s: %s", e.name, step, r.email)
				}
			}

			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: the upgrade to the latest", e.name)
			check("after the upgrade")

			goose.SetBaseFS(drv.MigrationsFS())
			require.NoError(t, goose.DownToContext(ctx, sqlDB, ".", versionBeforeLDAPGroups), "%s: down", e.name)
			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: up again", e.name)
			check("after down and up")
		})
	}
}
