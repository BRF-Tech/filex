// Package conformance_test runs the same catalogue query against every
// database filex supports: SQLite always, PostgreSQL and MySQL/MariaDB when
// FILEX_TEST_PG_DSN / FILEX_TEST_MYSQL_DSN name an EMPTY database to migrate
// (the queue's contract tests read the same two variables).
package conformance_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/mysql"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/postgres"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// each runs fn on SQLite, and on PostgreSQL and MySQL when their DSN is set.
// set is how that database writes created_at for one row.
func each(t *testing.T, fn func(t *testing.T, sqlDB *sql.DB, store db.Store, set string)) {
	t.Run("sqlite", func(t *testing.T) {
		sqlDB, store := dbtest.NewTestDB(t)
		fn(t, sqlDB, store, `UPDATE nodes SET created_at = ? WHERE id = ?`)
	})
	for _, c := range []struct{ name, env, set string }{
		{"postgres", "FILEX_TEST_PG_DSN", `UPDATE nodes SET created_at = $1 WHERE id = $2`},
		{"mysql", "FILEX_TEST_MYSQL_DSN", `UPDATE nodes SET created_at = ? WHERE id = ?`},
	} {
		c := c
		t.Run(c.name, func(t *testing.T) {
			dsn := os.Getenv(c.env)
			if dsn == "" {
				t.Skipf("%s is not set", c.env)
			}
			drv := db.MustGet(c.name)
			conn, err := drv.Open(context.Background(), dsn)
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			require.NoError(t, db.Migrate(context.Background(), drv, conn))
			fn(t, conn, drv.NewStore(conn), c.set)
		})
	}
}

// What a folder shows of itself (docs/thumbnails.md, Folder previews): the
// files directly in it that came in last, newest first by the later of the
// row's creation and the file's own modification time. Subfolders are never
// looked into.
//
// ⚠ The times are written the ways the catalogue writes them: created_at by
// CURRENT_TIMESTAMP (UTC text), backend_mtime as a time.Time the driver
// stores with its zone ("... +0300 +03"), in three zones. As text these do
// not sort together; a query that compares them as text, or asks julianday()
// to read the second, orders by created_at alone.
func TestListPreviewCandidates_NewestFirst(t *testing.T) {
	each(t, newestFirst)
}

func newestFirst(t *testing.T, sqlDB *sql.DB, store db.Store, setCreated string) {
	ctx := context.Background()
	// ⚠ A name of its own on every run, as the package's other tests do: the
	// PostgreSQL and MySQL databases outlive a run, and a fixed name ("s") made
	// the second run against the same database fail on storages.name before
	// it reached the query (the 0.50 final run, both servers).
	name := fmt.Sprintf("preview-%d", time.Now().UnixNano())
	st, err := store.CreateStorage(ctx, &model.Storage{Name: name, Driver: "local", MountPath: "/" + name, Enabled: true, ConfigJSON: []byte(`{}`)})
	require.NoError(t, err)
	mk := func(parent *model.Node, name string, typ model.NodeType, mtime *time.Time, created string) *model.Node {
		p := "/" + name
		var pid *int64
		if parent != nil {
			p = parent.Path + "/" + name
			pid = &parent.ID
		}
		n, err := store.CreateNode(ctx, &model.Node{StorageID: st.ID, ParentID: pid, Name: name, Path: p,
			PathHash: pathkey.Hash(st.ID, p), Type: typ, BackendMtime: mtime})
		require.NoError(t, err)
		if created != "" {
			ts, err := time.Parse("2006-01-02 15:04:05", created)
			require.NoError(t, err)
			_, err = sqlDB.ExecContext(ctx, setCreated, ts.UTC().Format("2006-01-02 15:04:05"), n.ID)
			require.NoError(t, err)
		}
		return n
	}
	istanbul := time.FixedZone("+03", 3*3600)
	newYork := time.FixedZone("-04", -4*3600)
	at := func(s string, loc *time.Location) *time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		require.NoError(t, err)
		return &v
	}
	dir := mk(nil, "Tatil", model.NodeTypeDirectory, nil, "")
	// Catalogued at 10:00 UTC, each; their own times decide.
	mk(dir, "eski.jpg", model.NodeTypeFile, at("2026-09-01 09:00", time.UTC), "2026-09-30 10:00:00")
	// 12:30 in Istanbul is 09:30 UTC: older than created_at, which wins.
	mk(dir, "istanbul.jpg", model.NodeTypeFile, at("2026-09-30 12:30", istanbul), "2026-09-30 10:00:00")
	// 08:00 in New York is 12:00 UTC: the newest. As text it would sort last.
	mk(dir, "newyork.jpg", model.NodeTypeFile, at("2026-09-30 08:00", newYork), "2026-09-30 10:00:00")
	// No mtime at all: its catalogue time, 11:00 UTC.
	mk(dir, "yuklenen.txt", model.NodeTypeFile, nil, "2026-09-30 11:00:00")
	// A file in a subfolder is never shown, however new.
	sub := mk(dir, "Alt", model.NodeTypeDirectory, nil, "")
	mk(sub, "derin.jpg", model.NodeTypeFile, at("2026-12-01 00:00", time.UTC), "2026-12-01 00:00:00")
	// A folder that holds only folders has nothing to show.
	only := mk(nil, "Projeler", model.NodeTypeDirectory, nil, "")
	mk(only, "Alfa", model.NodeTypeDirectory, nil, "")
	// A trashed file is not shown either.
	gone := mk(dir, "silinen.jpg", model.NodeTypeFile, at("2026-12-01 00:00", time.UTC), "")
	require.NoError(t, store.SoftDeleteNode(ctx, gone.ID))

	got, err := store.ListPreviewCandidates(ctx, st.ID, []int64{dir.ID, only.ID, sub.ID}, 3)
	require.NoError(t, err)
	names := func(ns []*model.Node) []string {
		var out []string
		for _, n := range ns {
			out = append(out, n.Name)
		}
		return out
	}
	// newyork 12:00 UTC (its mtime), yuklenen 11:00 (catalogued), then the
	// two at 10:00 by name: eski before istanbul, whose 12:30 is local time.
	require.Equal(t, []string{"newyork.jpg", "yuklenen.txt", "eski.jpg"}, names(got[dir.ID]))
	require.NotContains(t, got, only.ID, "a folder of folders shows nothing")
	require.Equal(t, []string{"derin.jpg"}, names(got[sub.ID]), "the subfolder's own file is its own, not its parent's")
}
