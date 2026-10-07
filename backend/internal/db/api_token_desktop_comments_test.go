package db_test

// Migration 00091, on every engine (task #157).
//
// Since this version adding and deleting a comment ask a token's `comments`
// permission at `rw`, and a token that does not name it holds `read`. The
// maintainer's decision (2026-10-06): the desktop app's own token comments,
// like its owner's browser - so the pairings made before the upgrade are given
// `comments:rw`, and nothing else is: an API key or an agent's token keeps its
// list exactly, and so reads comments and adds none.
//
// What this proves: the desktop rows gain the one entry, once (down and up
// again runs the forward step a second time on rows it already changed), a
// list that already names a level and an empty list are left alone, and every
// other token's list is byte for byte what it was.

import (
	"context"
	"fmt"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/tokenperm"
)

// versionBeforeDesktopComments is the last migration before 00091. (00087 to
// 00090 belong to branches merged beside this one; UpTo stops at whatever of
// them is present.)
const versionBeforeDesktopComments = 90

func TestDesktopCommentsMigrationOnEveryEngine(t *testing.T) {
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
			require.NoError(t, goose.UpToContext(ctx, sqlDB, ".", versionBeforeDesktopComments))

			_, err = sqlDB.ExecContext(ctx, `INSERT INTO users (email, password_hash) VALUES ('desk@comments.local', 'x')`)
			require.NoError(t, err)
			var uid int64
			require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = 'desk@comments.local'`).Scan(&uid))

			type row struct{ source, scopes, want string }
			seed := map[string]row{
				// A desktop pairing of a person who may change files, and a viewer's.
				"desktop":        {"desktop", "read,write,delete", "read,write,delete,comments:rw"},
				"desktop-viewer": {"desktop", "read,write", "read,write,comments:rw"},
				// Already at rw (a pairing made by the new binary): not twice.
				"desktop-rw": {"desktop", "read,write,delete,comments:rw", "read,write,delete,comments:rw"},
				// A level named on purpose is not overwritten.
				"desktop-named": {"desktop", "read,write,comments:read", "read,write,comments:read"},
				// An empty list grants nothing and must keep granting nothing.
				"desktop-empty": {"desktop", "", ""},
				// API keys and agents' tokens: untouched.
				"api-key":  {"", "read,write,delete,mcp", "read,write,delete,mcp"},
				"agent":    {"", "read,write,delete,mcp,admin", "read,write,delete,mcp,admin"},
				"confined": {"", "read,write,root:main://docs", "read,write,root:main://docs"},
			}
			for label, r := range seed {
				_, err := sqlDB.ExecContext(ctx, fmt.Sprintf(
					`INSERT INTO api_tokens (user_id, label, token_hash, scopes, source) VALUES (%d, '%s', '%s', '%s', '%s')`,
					uid, label, "hash-"+label, r.scopes, r.source))
				require.NoError(t, err, "%s: seeding %s", e.name, label)
			}

			check := func(step string) {
				t.Helper()
				rows, err := sqlDB.QueryContext(ctx, `SELECT label, scopes FROM api_tokens`)
				require.NoError(t, err)
				got := map[string]string{}
				for rows.Next() {
					var label, scopes string
					require.NoError(t, rows.Scan(&label, &scopes))
					got[label] = scopes
				}
				require.NoError(t, rows.Err())
				require.NoError(t, rows.Close())
				for label, r := range seed {
					require.Equal(t, r.want, got[label], "%s: %s: token %q", e.name, step, label)
				}
				// What the rows now mean, read the way the server reads them.
				for _, label := range []string{"desktop", "desktop-viewer", "desktop-rw"} {
					require.Equal(t, tokenperm.ReadWrite, tokenperm.LevelIn(got[label], tokenperm.Comments),
						"%s: %s: the desktop pairing %q comments", e.name, step, label)
				}
				for _, label := range []string{"api-key", "agent", "confined"} {
					require.Equal(t, tokenperm.Read, tokenperm.LevelIn(got[label], tokenperm.Comments),
						"%s: %s: the API key %q reads comments and adds none", e.name, step, label)
				}
				require.Equal(t, tokenperm.None, tokenperm.LevelIn(got["desktop-empty"], tokenperm.Comments), "%s: %s", e.name, step)
			}

			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: the upgrade to the latest", e.name)
			check("after the upgrade")

			// ⚠ db.Migrate clears goose's base FS on its way out.
			goose.SetBaseFS(drv.MigrationsFS())
			require.NoError(t, goose.DownToContext(ctx, sqlDB, ".", versionBeforeDesktopComments), "%s: down", e.name)
			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: up again", e.name)
			check("after down and up again")
		})
	}
}
