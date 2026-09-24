package db_test

// Migration 00054, on every engine.
//
// Until v0.43.0 an EMPTY token scope list meant every scope, admin included —
// the admin screen said so, and a token minted there with nothing ticked read
// /api/ai/admin/users. From this version no door issues an empty list and the
// driver reads one as NOTHING, so the upgrade has to write the old empty
// lists out. The owner's decision: as the explicit full list, admin INCLUDED
// — a token left blank on that screen was made to mean "everything", and
// narrowing it silently would break the integration it was made for.
//
// What this proves: every token's EFFECTIVE access is identical before and
// after the upgrade — measured by the rule each side had (empty = all before,
// empty = nothing after), scope by scope, admin included.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	apitoken "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// versionBeforeExplicitScopes is the last migration that predates 00054.
// (00052 and 00053 belong to branches merged beside this one; UpTo stops at
// whatever of them is present.)
const versionBeforeExplicitScopes = 53

// hadBefore is the rule a pre-0.43 token was judged by: an empty list
// granted every scope.
func hadBefore(scopes, want string) bool {
	if strings.TrimSpace(scopes) == "" {
		return true
	}
	return (&model.APIToken{Scopes: scopes}).HasScope(want)
}

func TestAPITokenExplicitScopesOnEveryEngine(t *testing.T) {
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
			require.NoError(t, goose.UpToContext(context.Background(), sqlDB, ".", versionBeforeExplicitScopes))

			_, err = sqlDB.ExecContext(context.Background(),
				`INSERT INTO users (email, password_hash, role, locale, timezone) VALUES ('tok@scopes.local','x','admin','en','')`)
			require.NoError(t, err)
			var uid int64
			require.NoError(t, sqlDB.QueryRowContext(context.Background(),
				`SELECT id FROM users WHERE email = 'tok@scopes.local'`).Scan(&uid))

			before := map[string]string{
				"blank":        "",                  // ticked nothing on the admin screen
				"spaces":       "  ",                // the same, as a form might send it
				"read":         "read",              // an explicit list: untouched
				"root-only":    "root:main://x",     // a root and no verb granted no verb
				"admin-listed": "read,admin",        // admin named on purpose: untouched
				"full":         "read,write,delete", // untouched
			}
			for label, scopes := range before {
				_, err := sqlDB.ExecContext(context.Background(), fmt.Sprintf(
					`INSERT INTO api_tokens (user_id, label, token_hash, scopes) VALUES (%d, '%s', '%s', '%s')`,
					uid, label, "hash-"+label, scopes))
				require.NoError(t, err, "%s: seeding a pre-upgrade token", e.name)
			}

			require.NoError(t, db.Migrate(context.Background(), drv, sqlDB))

			rows, err := sqlDB.QueryContext(context.Background(), `SELECT label, scopes FROM api_tokens`)
			require.NoError(t, err)
			defer rows.Close()
			after := map[string]string{}
			for rows.Next() {
				var label, scopes string
				require.NoError(t, rows.Scan(&label, &scopes))
				after[label] = scopes
			}
			require.NoError(t, rows.Err())

			require.Equal(t, "read,write,delete,mcp,admin", after["blank"],
				"%s: a blank list must be written out as the full list, admin included", e.name)
			require.Equal(t, "read,write,delete,mcp,admin", after["spaces"], "%s", e.name)
			for _, kept := range []string{"read", "root-only", "admin-listed", "full"} {
				require.Equal(t, before[kept], after[kept], "%s: the upgrade rewrote an explicit list (%s)", e.name, kept)
			}

			// The point of the whole thing: nobody gained or lost anything.
			for label, was := range before {
				now := (&model.APIToken{Scopes: after[label]})
				for _, s := range apitoken.ValidScopes {
					require.Equal(t, hadBefore(was, s), now.HasScope(s),
						"%s: token %q (%q → %q) changed its %s access across the upgrade", e.name, label, was, after[label], s)
				}
			}
		})
	}
}
