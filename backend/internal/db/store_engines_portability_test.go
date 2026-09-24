package db_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/text/unicode/norm"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/namefold"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	searchpkg "github.com/brf-tech/filex/backend/internal/search"
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
			hits, err := store.SearchNodes(ctx, st.ID, model.NameMatch{Words: []string{"readme"}}, 50)
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

// TestSearchNodesOnEveryEngine — SearchNodes is the search an install without
// the index runs, and every engine spells it differently (fx_match on SQLite,
// normalize+lower on PostgreSQL, a collation over both Unicode forms on
// MySQL), so every promise it makes is held here on all three:
//
//   - EVERY word is a condition, so the LIMIT counts rows that answer the
//     whole query. It used to be one word, the longest, and a file that
//     answered everything but sorted after the first thousand rows holding
//     that word was never seen (GitHub PR #46: row 33 623 of a 169k-file
//     catalogue).
//   - Rank BEFORE the limit: with 1,001 `a-report-NNNN.txt` beside
//     `report.txt`, the search for `report` returned 1,000 rows and not the
//     one file called report.
//   - The stored name goes through the same normaliser as the words
//     (internal/namefold): a name a Mac wrote decomposed, a Turkish name in
//     capitals, the four i's as one letter.
//   - The words are text, not LIKE grammar.
//
// And the same promises through the planner (search.PlanFallback, whose runs
// the engine checks natively first): its answer must be exactly what the
// normaliser, applied in Go, says it is.
func TestSearchNodesOnEveryEngine(t *testing.T) {
	dot := string(rune(0x0307))
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)

			ids := map[string]int64{}
			add := func(p string) int64 {
				t.Helper()
				n, err := store.CreateNode(ctx, &model.Node{
					StorageID: st.ID, Name: path.Base(p), Path: p,
					PathHash: pathkey.Hash(st.ID, p), Type: model.NodeTypeFile,
				})
				require.NoError(t, err, "catalogue %q", p)
				ids[p] = n.ID
				return n.ID
			}
			names := func(nodes []*model.Node) []string {
				out := make([]string, 0, len(nodes))
				for _, n := range nodes {
					out = append(out, n.Name)
				}
				return out
			}
			search := func(m model.NameMatch, limit int) []*model.Node {
				t.Helper()
				rows, err := store.SearchNodes(ctx, st.ID, m, limit)
				require.NoError(t, err, "%+v", m)
				return rows
			}

			// ── ranking before the limit ───────────────────────────────
			for _, name := range []string{
				// Every one of these holds the word, and sorts before the best
				// answers in some engine's `ORDER BY name`.
				"a-report-1.txt", "a-report-2.txt", "B-report.txt",
				"report-final.txt", // a prefix match
				"report.txt",       // the name without its extension IS the word
				"REPORT",           // the name IS the word, in another case
				"my_file.txt", "myXfile.txt",
			} {
				add("/r/" + name)
			}
			report := model.NameMatch{Words: []string{"report"}, Prefer: "report"}
			require.Equal(t, []string{"REPORT", "report.txt", "report-final.txt"}, names(search(report, 3)),
				"exact (the name, or the name plus an extension) first, then prefix; shorter first")
			require.Len(t, search(model.NameMatch{Words: []string{"report"}}, 50), 6, "no preference: every match")
			// `_` is a letter: the store escapes it on every engine. SQLite has
			// no escape character unless the statement names one.
			require.Equal(t, []string{"my_file.txt"}, names(search(model.NameMatch{Words: []string{"my_file"}}, 10)))
			require.Equal(t, []string{"my_file.txt"}, names(search(model.NameMatch{Words: []string{"file"}, Prefer: "my_file"}, 1)),
				"the preferred word is literal too: myXfile.txt is no exact match for my_file")

			// ── every word, and the LIMIT counts answers ───────────────
			for i := 0; i < 300; i++ {
				add(fmt.Sprintf("/2026/Plan - Aa%03d - Yeni.pdf", i))
			}
			// What a Mac uploads: ş and ü decomposed into a letter and a mark.
			decomposed := add("/2026/" + norm.NFD.String("Plan - Ayşe Gürel - Yeni.pdf"))
			upper := add("/2026/PLAN - AYŞE GÜREL - ESKİ.pdf")
			// The words are matched against the name: a folder does not count.
			add("/Gürel/plan.pdf")
			deleted := add("/2026/Plan - Gürel - silindi.pdf")
			require.NoError(t, store.SoftDeleteNode(ctx, deleted))

			want := []int64{decomposed, upper}
			for _, m := range []model.NameMatch{
				{Words: []string{"gürel", "plan"}},
				{Words: []string{"plan", "gürel"}},                  // the order is not a condition
				{Words: []string{"GÜREL", norm.NFD.String("Plan")}}, // the store folds what it is handed
				{Words: []string{norm.NFD.String("gürel"), "plan"}, Runs: []string{"rel", "plan"}},
			} {
				require.ElementsMatch(t, want, nodeIDs(search(m, 50)), "%+v", m)
			}
			// The first rows by name holding `plan` are the 300 above.
			one := search(model.NameMatch{Words: []string{"plan", "gürel"}}, 1)
			require.Len(t, one, 1)
			require.Subset(t, want, nodeIDs(one))
			require.Empty(t, search(model.NameMatch{}, 50), "no words, no rows")

			// ── one normaliser, both sides, every engine ───────────────
			for _, p := range []string{
				"/tr/IŞIK.pdf", "/tr/ışık notları.txt",
				"/tr/" + norm.NFD.String("İPEK ADA.pdf"), "/tr/i" + dot + "pek-lower.txt",
				"/tr/KIŞ LİSTESİ.xlsx", "/tr/ŞUBAT RAPORU.pdf", "/tr/ÇALIŞMA.txt",
				"/tr/" + norm.NFD.String("çalışma planı.docx"),
			} {
				add(p)
			}
			for _, q := range []string{
				"ışık", "IŞIK", "Işık", "işik", // an all-caps Turkish name typed in lower case, and back
				"ipek", "İPEK", "i" + dot + "pek", // İ decomposed, and lower-cased the full Unicode way
				"kış listesi", "KIŞ", "liste",
				"şubat", "ŞUBAT RAPORU",
				"çalışma", "ÇALIŞMA", "çalişma", norm.NFD.String("Çalışma"),
				"Ayşe Gürel", norm.NFD.String("ayşe gürel yeni"),
			} {
				words := searchpkg.NormWords(q)
				var ref []string
				for p, id := range ids {
					if id != deleted && refMatch(path.Base(p), words) {
						ref = append(ref, path.Base(p))
					}
				}
				require.NotEmpty(t, ref, "the fixture must answer %q", q)
				require.ElementsMatch(t, ref, names(search(model.NameMatch{Words: words}, 500)), "store, query %q", q)
				rows, err := searchpkg.PlanFallback(q).Candidates(ctx, store, st.ID, 500)
				require.NoError(t, err)
				require.ElementsMatch(t, ref, names(rows), "planner + store, query %q", q)
			}
			// Ranking before the limit folds the same way, for a word of
			// Turkish letters too: the file that IS the word comes first,
			// written in capitals or decomposed.
			// Each has a decoy the ranking must see past: a name that is also
			// the word but longer (`IŞIK.pdf` next to `ışık.md`), or a shorter
			// name holding the word in the middle (`a gürel`), which wins on
			// length the moment the right name is not recognised as the word.
			add("/tr/" + norm.NFD.String("Gürel.pdf"))
			add("/tr/ışık.md")
			add("/tr/a gürel")
			for word, want := range map[string]string{
				"işik":    "ışık.md",
				"gürel":   norm.NFD.String("Gürel.pdf"),
				"ÇALIŞMA": "ÇALIŞMA.txt",
			} {
				got := names(search(model.NameMatch{Words: []string{word}, Prefer: word}, 1))
				require.Equal(t, []string{want}, got, "the best match for %q must survive a LIMIT of 1", word)
			}

			// Accents stay significant to the normaliser. MySQL's collation is
			// accent-insensitive, so it may hand the scorer more rows — never
			// fewer; the other two engines answer exactly.
			if e.name != "mysql" {
				require.Empty(t, search(model.NameMatch{Words: []string{"gurel"}}, 50), "`gurel` is not `gürel`")
			}
		})
	}
}

// refMatch is the Go reference the engines are held to: the name, folded by
// internal/namefold, holds every folded word.
func refMatch(name string, words []string) bool {
	f := namefold.String(name)
	for _, w := range words {
		if !strings.Contains(f, namefold.String(w)) {
			return false
		}
	}
	return true
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
