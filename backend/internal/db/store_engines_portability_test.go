package db_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// The store methods below had SQLite-only SQL that the first-five-minutes test
// never reached, so they shipped broken on MySQL through v0.40.0 while every
// gate stayed green. Each was measured failing on a real MySQL 8.4 and 8.0
// server on 2026-09-14 (issue #23) before it was fixed:
//
//   - IncrementUserUsage used MAX(0, x) — a scalar in SQLite, an aggregate
//     only in MySQL. Syntax error; quota usage never moved.
//   - DeleteOldNodeVersions used LIMIT -1. Syntax error; version history grew
//     without bound, silently, because Snapshot ignores a cleanup error.
//   - ListSyncRunsAcrossAll used datetime('now', '-5 days'). The admin Sync
//     history page and every storage's run list answered 500.
//   - nodes.name was case- and accent-insensitive, so README.md and Readme.md
//     could not both be catalogued; the second uploaded, never listed, and
//     every sync logged a duplicate key for it.
//
// They run on every engine, not only MySQL, so a later "portable" rewrite
// cannot trade one engine's bug for another's.

func TestQuotaUsageMovesOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			user, err := store.CreateUser(ctx, "quota@example.com", "hash", "user", "en", "")
			require.NoError(t, err)

			usage := func() int64 {
				t.Helper()
				u, err := store.GetUser(ctx, user.ID)
				require.NoError(t, err)
				return u.UsageBytes
			}

			require.NoError(t, store.IncrementUserUsage(ctx, user.ID, 1000), "grow usage")
			require.EqualValues(t, 1000, usage())

			require.NoError(t, store.IncrementUserUsage(ctx, user.ID, -300), "shrink usage")
			require.EqualValues(t, 700, usage())

			require.NoError(t, store.IncrementUserUsage(ctx, user.ID, -5000), "shrink past zero")
			require.EqualValues(t, 0, usage(), "usage is clamped at zero, never negative")
		})
	}
}

func TestVersionPruneOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st := createEngineStorage(t, store)
			node, err := store.CreateNode(ctx, &model.Node{
				StorageID: st.ID, Name: "draft.md", Path: "/draft.md",
				PathHash: pathkey.Hash(st.ID, "/draft.md"), Type: model.NodeTypeFile,
			})
			require.NoError(t, err)

			for n := 1; n <= 5; n++ {
				_, err := store.CreateNodeVersion(ctx, &model.NodeVersion{NodeID: node.ID, VersionN: n, Size: int64(n)})
				require.NoError(t, err, "version %d", n)
			}

			doomed, err := store.DeleteOldNodeVersions(ctx, node.ID, 2)
			require.NoError(t, err, "prune to the newest two")
			require.Len(t, doomed, 3)

			left, err := store.ListNodeVersions(ctx, node.ID)
			require.NoError(t, err)
			kept := make([]int, 0, len(left))
			for _, v := range left {
				kept = append(kept, v.VersionN)
			}
			sort.Ints(kept)
			require.Equal(t, []int{4, 5}, kept, "the newest versions survive the prune")
		})
	}
}

func TestSyncHistoryOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st := createEngineStorage(t, store)

			recent, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)
			require.NoError(t, store.FinishSyncRun(ctx, recent.ID, "", 3, 1, 0, 0, "ok", ""))

			old, err := store.CreateSyncRun(ctx, st.ID, "")
			require.NoError(t, err)
			require.NoError(t, store.FinishSyncRun(ctx, old.ID, "", 3, 0, 0, 0, "ok", ""))
			age := `UPDATE sync_runs SET started_at=? WHERE id=?`
			if e.name == "postgres" {
				age = `UPDATE sync_runs SET started_at=$1 WHERE id=$2`
			}
			_, err = sqlDB.ExecContext(ctx, age, time.Now().UTC().Add(-6*24*time.Hour), old.ID)
			require.NoError(t, err, "age one run past the five-day window")

			runs, total, err := store.ListSyncRunsAcrossAll(ctx, 0, "", 50, 0)
			require.NoError(t, err, "the admin Sync history query")
			require.EqualValues(t, 1, total)
			require.Len(t, runs, 1)
			require.Equal(t, recent.ID, runs[0].ID, "a six-day-old run is outside the window")

			_, total, err = store.ListSyncRunsAcrossAll(ctx, st.ID, "ok", 50, 0)
			require.NoError(t, err, "filtered by storage and status")
			require.EqualValues(t, 1, total)
		})
	}
}

func TestNamesDifferingOnlyByCaseOrAccentOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()

			st := createEngineStorage(t, store)
			dir, err := store.CreateNode(ctx, &model.Node{
				StorageID: st.ID, Name: "docs", Path: "/docs",
				PathHash: pathkey.Hash(st.ID, "/docs"), Type: model.NodeTypeDirectory,
			})
			require.NoError(t, err)

			// Every pair is two different objects on S3, SFTP, WebDAV and a
			// Linux disk. A parent is set on purpose: a NULL parent_id exempts
			// a row from the unique key on every engine, which is how the
			// MySQL bug hid from a check made at the storage root.
			names := []string{"README.md", "Readme.md", "resume.txt", "résumé.txt", "notes", "notes "}
			for _, name := range names {
				p := "/docs/" + name
				_, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, ParentID: &dir.ID, Name: name, Path: p,
					PathHash: pathkey.Hash(st.ID, p), Type: model.NodeTypeFile,
				})
				require.NoError(t, err, "catalogue %q next to its look-alike", name)
			}

			children, err := store.ListNodesByParent(ctx, st.ID, &dir.ID)
			require.NoError(t, err)
			got := make([]string, 0, len(children))
			for _, c := range children {
				got = append(got, c.Name)
			}
			want := append([]string(nil), names...)
			sort.Strings(got)
			sort.Strings(want)
			require.Equal(t, want, got, "each name comes back exactly as written")

			// A search still ignores case: the fallback planner lower-cases
			// the word it sends, so a case-sensitive LIKE would find nothing.
			hits, err := store.SearchNodes(ctx, st.ID, "%readme%", 50)
			require.NoError(t, err)
			require.Len(t, hits, 2, "search matches both spellings")

			// A grant is keyed by path too: one on /Docs must not overwrite
			// one on /docs.
			user, err := store.CreateUser(ctx, "grants@example.com", "hash", "user", "en", "")
			require.NoError(t, err)
			for _, prefix := range []string{"Docs", "docs"} {
				_, err := store.CreateFileGrant(ctx, &model.FileGrant{
					StorageID: st.ID, PathPrefix: prefix, IsDir: true, UserID: user.ID, Level: model.GrantViewer,
				})
				require.NoError(t, err, "grant on %q", prefix)
			}
			grants, err := store.ListFileGrantsByStorageUser(ctx, st.ID, user.ID)
			require.NoError(t, err)
			require.Len(t, grants, 2, "two grants on two different folders")
		})
	}
}

// TestSearchNodesAllOnEveryEngine: a multi-word search puts every word in the
// query, so its LIMIT counts rows that can answer the whole query.
//
// The index-less search used to send the database one word, take the first
// rows by name that held it, and check the other words afterwards. A file
// that answered everything but sorted after those rows was never seen —
// measured in production on 2026-09-24 with the one word in 64 483 names and
// the file at row 33 623. A term is the spellings of one word (composed,
// decomposed, cased); a row's name needs one of them for every term.
func TestSearchNodesAllOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			add := func(p string) int64 {
				t.Helper()
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, Name: path.Base(p), Path: p,
					PathHash: pathkey.Hash(st.ID, p), Type: model.NodeTypeFile,
				})
				require.NoError(t, err, "catalogue %q", p)
				return n.ID
			}
			// Rows that hold one of the words and sort before the file.
			for i := 0; i < 300; i++ {
				add(fmt.Sprintf("/2026/Plan - Aa%03d - Yeni.pdf", i))
			}
			// What a Mac uploads: ş and ü decomposed into a letter and a mark.
			decomposed := add("/2026/Plan - Ays\u0327e Gu\u0308rel - Yeni.pdf")
			upper := add("/2026/PLAN - AYŞE GÜREL - ESKİ.pdf")
			// The words are matched against the name: a folder does not count.
			add("/Gürel/plan.pdf")
			deleted := add("/2026/Plan - Gürel - silindi.pdf")
			require.NoError(t, store.SoftDeleteNode(ctx, deleted))

			gurel := []string{"%gürel%", "%GÜREL%", "%gu\u0308rel%"}
			want := []int64{decomposed, upper}
			rows, err := store.SearchNodesAll(ctx, st.ID, [][]string{gurel, {"%plan%"}}, 50)
			require.NoError(t, err)
			require.ElementsMatch(t, want, nodeIDs(rows))

			// The order of the terms is not a condition.
			rows, err = store.SearchNodesAll(ctx, st.ID, [][]string{{"%plan%"}, gurel}, 50)
			require.NoError(t, err)
			require.ElementsMatch(t, want, nodeIDs(rows))

			// The LIMIT counts answers, not rows that hold one word: the
			// first rows by name holding `plan` are the 300 above.
			rows, err = store.SearchNodesAll(ctx, st.ID, [][]string{{"%plan%"}, gurel}, 1)
			require.NoError(t, err)
			require.Len(t, rows, 1)
			require.Subset(t, want, nodeIDs(rows))

			rows, err = store.SearchNodesAll(ctx, st.ID, nil, 50)
			require.NoError(t, err)
			require.Empty(t, rows, "no terms, no rows")
		})
	}
}

func nodeIDs(rows []*model.Node) []int64 {
	out := make([]int64, 0, len(rows))
	for _, n := range rows {
		out = append(out, n.ID)
	}
	return out
}

func createEngineStorage(t *testing.T, store interface {
	CreateStorage(context.Context, *model.Storage) (*model.Storage, error)
}) *model.Storage {
	t.Helper()
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name:          "local",
		Driver:        "local",
		MountPath:     "/data/files",
		ConfigJSON:    json.RawMessage(`{"root":"/data/files"}`),
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
	})
	require.NoError(t, err, "create storage")
	return st
}
