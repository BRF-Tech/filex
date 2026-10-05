package db_test

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// TestSubtreeQuestionsHoldAtTheEdgeOfTheIndexKeyOnEveryEngine asks about
// folders whose path ends just before, at and just after the 512 characters
// the PostgreSQL and MySQL indexes hold (left(path, 512), path(512)). There
// the cut lands on the range's own bounds: for a folder of 511 characters the
// key of "dir/" is the whole bound, for one of 512 the two bounds cut to the
// same key, and a row past the key differs from its folder only in what the
// index does not hold. The names are two-byte letters, so a cut counted in
// bytes instead of characters lands somewhere else, and each folder is the
// one before it plus a letter: a folder that only starts with another's name
// sits right beside that one's range. (A name holds 255 characters at most -
// nodes.name on MySQL - so each folder is three names deep.)
func TestSubtreeQuestionsHoldAtTheEdgeOfTheIndexKeyOnEveryEngine(t *testing.T) {
	for _, e := range subtreeEngines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			base := "/" + strings.Repeat("ş", 200) + "/" + strings.Repeat("ğ", 200) + "/"
			var dirs, live []string
			for _, chars := range []int{510, 511, 512, 513} {
				dir := base + strings.Repeat("ü", chars-utf8.RuneCountInString(base))
				require.Equal(t, chars, utf8.RuneCountInString(dir))
				dirs = append(dirs, dir)
				for _, row := range []struct {
					path string
					typ  model.NodeType
				}{
					{dir, model.NodeTypeDirectory},
					{dir + "/a.txt", model.NodeTypeFile},
					{dir + "/" + strings.Repeat("ç", 200) + "/" + strings.Repeat("ö", 200), model.NodeTypeFile},
					{dir + "0", model.NodeTypeDirectory},
					{dir + "0/x.txt", model.NodeTypeFile},
					{dir + ".txt", model.NodeTypeFile},
					{dir[1:] + "/çıplak.txt", model.NodeTypeFile},
				} {
					seedTreeRow(t, store, st.ID, row.path, row.typ)
					live = append(live, row.path)
				}
			}

			asked := []string{}
			for _, dir := range dirs {
				asked = append(asked, dir, dir+"/", dir[1:], dir+"0")
			}
			for _, dir := range asked {
				name := subtreeLabel(dir)
				self, below := subtreeOracle(dir)
				var wantBelow, wantAll []string
				for _, p := range live {
					if below(p) {
						wantBelow = append(wantBelow, p)
					}
					if self(p) || below(p) {
						wantAll = append(wantAll, p)
					}
				}

				got, err := store.ListNodesUnder(ctx, st.ID, dir, false)
				require.NoError(t, err, "ListNodesUnder(%s)", name)
				require.Equal(t, wantAll, pathsOrNil(got), "ListNodesUnder(%s)", name)

				n, err := store.CountLiveNodesUnder(ctx, st.ID, dir)
				require.NoError(t, err, "CountLiveNodesUnder(%s)", name)
				require.EqualValues(t, len(wantBelow), n, "CountLiveNodesUnder(%s)", name)

				has, err := store.HasLiveNodesUnder(ctx, st.ID, dir)
				require.NoError(t, err, "HasLiveNodesUnder(%s)", name)
				require.Equal(t, len(wantBelow) > 0, has, "HasLiveNodesUnder(%s)", name)

				stale, err := store.ListStaleNodesUnder(ctx, st.ID, dir, time.Now().Add(time.Hour))
				require.NoError(t, err, "ListStaleNodesUnder(%s)", name)
				require.ElementsMatch(t, wantBelow, pathsOrNil(stale), "ListStaleNodesUnder(%s)", name)
			}
		})
	}
}

// pathsOrNil is pathsInOrder, nil for no rows (what an empty want slice is).
func pathsOrNil(nodes []*model.Node) []string {
	if len(nodes) == 0 {
		return nil
	}
	return pathsInOrder(nodes)
}

// subtreeEngines is engines() and, on the PostgreSQL server, a database whose
// collation is not byte order (ICU en-US): the subtree questions run there too.
//
// ⚠ The PostgreSQL that CI and the release gate run (postgres:17-alpine,
// postgres:16-alpine) is built on musl, where en_US.utf8 orders like "C": a
// range asked in the database's own collation instead of
// "C" gives the right rows there and the wrong ones on a Debian or RHEL server
// (glibc) or an ICU database - "/Rapor/a.txt" outside the range of /Rapor
// under glibc, "/müşteri/x" inside the range of /Müşteri under ICU. Measured
// 2026-10-05 (PR #89 review): with `path COLLATE "C"` taken out of the
// PostgreSQL driver, TestSubtreeQuestionsMatchTheFolderExactlyOnEveryEngine
// passed on postgres:17-alpine and failed on postgres:17 (glibc) and in an
// ICU database. This entry makes CI's server catch it.
func subtreeEngines() []engine {
	return append(engines(), engine{name: "postgres-icu", driver: "postgres", dsnEnv: "FILEX_TEST_PG_DSN", fresh: freshPostgresICU})
}

// freshPostgresICU is freshPostgres for a database created with the ICU
// collation en-US. A server that cannot (PostgreSQL before 15, or built
// without ICU) skips the test rather than passing it.
func freshPostgresICU(t *testing.T, admin string) string {
	t.Helper()
	name := scratchDBName()
	adminDB, err := sql.Open("pgx", admin)
	require.NoError(t, err, "postgres: open admin connection")
	defer adminDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := adminDB.ExecContext(ctx, `CREATE DATABASE "`+name+`" TEMPLATE template0 ENCODING 'UTF8' LOCALE_PROVIDER icu ICU_LOCALE 'en-US'`); err != nil {
		t.Skipf("postgres-icu not exercised: this server cannot create an ICU database: %v", err)
	}
	t.Cleanup(func() {
		drop, err := sql.Open("pgx", admin)
		if err != nil {
			return
		}
		defer drop.Close()
		_, _ = drop.Exec(`DROP DATABASE IF EXISTS "` + name + `" WITH (FORCE)`)
	})
	u, err := url.Parse(admin)
	require.NoError(t, err, "postgres: DSN must be a URL")
	u.Path = "/" + name
	return u.String()
}
