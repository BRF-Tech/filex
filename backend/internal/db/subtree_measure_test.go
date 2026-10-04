//go:build measure

package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// TestMeasureSubtreeQuestions times what a store answers about the rows below
// a folder, on a catalogue the size of a real install (170,245 rows, 152,562
// of them in the storage that is asked), on every engine that is configured.
// It is how the figures in migration 00081 and its changelog entry were taken.
//
//	FILEX_TEST_PG_DSN, FILEX_TEST_MYSQL_DSN   as for the engine tests
//	FILEX_MEASURE_ORDER=shuffled              write the rows in an order unrelated
//	                                          to their paths (dbtest.SeedCatalogue)
//	FILEX_MEASURE_PLAIN_INDEX=1               SQLite: ask with the index as it was
//	                                          first written, (storage_id, path) with
//	                                          no WHERE - what the clause is there for
//
//	go test -tags measure -run TestMeasureSubtreeQuestions -v -timeout 60m ./internal/db
//
// The file asks only what db.Store has had since 0.51, so this file and
// internal/testutil/dbtest/catalogue.go, copied to a tree from before 00081,
// give the other half of the comparison: there "is anything below" is
// CountLiveNodesUnder > 0, as the encryption policy asked it. The last two
// questions are not about a folder: they are the statements a new index on
// nodes could have taken off their own, and should cost what they did.
//
// ⚠ Compare only numbers taken on the same machine, one run after another.
func TestMeasureSubtreeQuestions(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			store := drv.NewStore(sqlDB)
			ctx := context.Background()
			st := createEngineStorage(t, store)
			other, err := store.CreateStorage(ctx, &model.Storage{
				Name: "other", Driver: "local", MountPath: "/data/other", ConfigJSON: []byte(`{"root":"/data/other"}`),
				SyncMode: model.SyncModePoll, SyncIntervalS: 900, Enabled: true,
			})
			require.NoError(t, err)

			began := time.Now()
			shuffled := os.Getenv("FILEX_MEASURE_ORDER") == "shuffled"
			rows := dbtest.SeedCatalogue(t, store, st.ID, "/Arşiv", 60, 25, 100, shuffled)
			rows += dbtest.SeedCatalogue(t, store, other.ID, "/Arşiv", 7, 25, 100, shuffled)
			rows += dbtest.SeedEmptyFolders(t, store, st.ID, "/Yeni", 1000)
			seeded := time.Since(began)
			// What autovacuum and InnoDB's own sampling do on an install that
			// has been running; SQLite keeps no statistics unless told to.
			switch e.name {
			case "postgres":
				_, err = sqlDB.ExecContext(ctx, `ANALYZE nodes`)
			case "mysql":
				_, err = sqlDB.ExecContext(ctx, `ANALYZE TABLE nodes`)
			}
			require.NoError(t, err)
			order := "path order"
			if shuffled {
				order = "shuffled"
			}
			t.Logf("%-9s %d rows catalogued in %s, %s (%.0f rows/s)%s", e.name, rows, seeded.Round(time.Millisecond), order,
				float64(rows)/seeded.Seconds(), measureIndexSize(e.name, sqlDB))
			if e.name == "sqlite" {
				measureSQLiteIndexBuild(t, sqlDB, os.Getenv("FILEX_MEASURE_PLAIN_INDEX") != "")
			}

			has := func(dir string) (bool, error) {
				if s, ok := store.(interface {
					HasLiveNodesUnder(context.Context, int64, string) (bool, error)
				}); ok {
					return s.HasLiveNodesUnder(ctx, st.ID, dir)
				}
				n, err := store.CountLiveNodesUnder(ctx, st.ID, dir)
				return n > 0, err
			}
			cutoff := time.Now().Add(time.Hour)     // every row was last seen before it, as in a rescan of another folder
			rescanned := time.Now().Add(-time.Hour) // every row was seen after it, as after a rescan of this folder
			const (
				whole    = "/Arşiv"                             // 151,560 rows below
				customer = "/Arşiv/Müşteri-07"                  // 2,525 rows below
				folder   = "/Arşiv/Müşteri-07/Sözleşmeler-03"   // 100 rows below
				empty    = "/Yeni/klasör-0500"                  // a folder with nothing below
				absent   = "/Arşiv/Müşteri-07/Sözleşmeler-yeni" // not there
			)
			for _, q := range []struct {
				name string
				ask  func() (int, error)
			}{
				{"is anything below: a folder of 100 files", func() (int, error) { ok, err := has(folder); return measureBool(ok), err }},
				{"is anything below: an empty folder", func() (int, error) { ok, err := has(empty); return measureBool(ok), err }},
				{"is anything below: a folder that is not there", func() (int, error) { ok, err := has(absent); return measureBool(ok), err }},
				{"is anything below: 151,560 rows", func() (int, error) { ok, err := has(whole); return measureBool(ok), err }},
				{"count below: 100 rows", func() (int, error) { n, err := store.CountLiveNodesUnder(ctx, st.ID, folder); return int(n), err }},
				{"count below: 2,525 rows", func() (int, error) { n, err := store.CountLiveNodesUnder(ctx, st.ID, customer); return int(n), err }},
				{"count below: 151,560 rows", func() (int, error) { n, err := store.CountLiveNodesUnder(ctx, st.ID, whole); return int(n), err }},
				{"list at and below: 101 rows", func() (int, error) { ns, err := store.ListNodesUnder(ctx, st.ID, folder, false); return len(ns), err }},
				{"list at and below: 2,526 rows", func() (int, error) { ns, err := store.ListNodesUnder(ctx, st.ID, customer, true); return len(ns), err }},
				{"stale below: 100 rows", func() (int, error) {
					ns, err := store.ListStaleNodesUnder(ctx, st.ID, folder, cutoff)
					return len(ns), err
				}},
				{"stale below: an empty folder", func() (int, error) {
					ns, err := store.ListStaleNodesUnder(ctx, st.ID, empty, cutoff)
					return len(ns), err
				}},
				{"stale below: 151,560 rows, none of them stale", func() (int, error) {
					ns, err := store.ListStaleNodesUnder(ctx, st.ID, whole, rescanned)
					return len(ns), err
				}},
				{"name search: one word, no name has it", func() (int, error) {
					ns, err := store.SearchNodes(ctx, st.ID, model.NameMatch{Words: []string{"tutanak"}, Runs: []string{"tutanak"}}, 100)
					return len(ns), err
				}},
				{"rows deleted where they stood: none", func() (int, error) {
					ids, err := store.ListVanishedNodeIDs(ctx, st.ID, 0, 500)
					return len(ids), err
				}},
			} {
				answer, mean, calls := measureMean(t, q.ask)
				t.Logf("%-9s %-46s %12s  (answer %d, mean of %d)", e.name, q.name, measureDuration(mean), answer, calls)
			}
		})
	}
}

// measureMean asks once to warm the engine up, then for two seconds or 2000
// calls, whichever ends first (three calls at least), and returns the answer
// and the mean.
func measureMean(t *testing.T, ask func() (int, error)) (answer int, mean time.Duration, calls int) {
	t.Helper()
	answer, err := ask()
	require.NoError(t, err)
	began := time.Now()
	for calls < 3 || (calls < 2000 && time.Since(began) < 2*time.Second) {
		got, err := ask()
		require.NoError(t, err)
		require.Equal(t, answer, got, "the answer changed between calls")
		calls++
	}
	return answer, time.Since(began) / time.Duration(calls), calls
}

func measureBool(b bool) int {
	if b {
		return 1
	}
	return 0
}

// measureDuration prints a mean with three significant figures in the unit
// that reads best.
func measureDuration(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2f s", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.1f ms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%.3f ms", float64(d)/float64(time.Millisecond))
	}
}

// measureSQLiteIndexBuild drops idx_nodes_storage_path, if the tree has it, and
// builds it again as the migration does on a catalogue that exists, timed - or,
// with plain, as (storage_id, path) with no WHERE, which is how it was first
// written and what TestThePathIndexIsOnlyForStatementsThatComparePath is about.
func measureSQLiteIndexBuild(t *testing.T, sqlDB *sql.DB, plain bool) {
	t.Helper()
	var ddl sql.NullString
	err := sqlDB.QueryRow(`SELECT sql FROM sqlite_master WHERE type='index' AND name='idx_nodes_storage_path'`).Scan(&ddl)
	if err != nil || !ddl.Valid {
		return // a tree from before 00081
	}
	if plain {
		ddl.String = `CREATE INDEX idx_nodes_storage_path ON nodes(storage_id, path)`
	}
	_, err = sqlDB.Exec(`DROP INDEX idx_nodes_storage_path`)
	require.NoError(t, err)
	began := time.Now()
	_, err = sqlDB.Exec(ddl.String)
	require.NoError(t, err)
	t.Logf("sqlite    %s: built in %s%s", ddl.String, time.Since(began).Round(time.Millisecond), measureIndexSize("sqlite", sqlDB))
}

// measureIndexSize is what the engine says idx_nodes_storage_path takes on
// disk, or "" where there is no such index or the engine does not say.
func measureIndexSize(engine string, sqlDB *sql.DB) string {
	var q string
	switch engine {
	case "sqlite":
		q = `SELECT SUM(pgsize) FROM dbstat WHERE name='idx_nodes_storage_path'`
	case "postgres":
		q = `SELECT pg_relation_size(to_regclass('idx_nodes_storage_path'))`
	case "mysql":
		q = `SELECT stat_value * @@innodb_page_size FROM mysql.innodb_index_stats
		      WHERE database_name = DATABASE() AND table_name = 'nodes'
		        AND index_name = 'idx_nodes_storage_path' AND stat_name = 'size'`
	}
	var bytes sql.NullInt64
	if err := sqlDB.QueryRow(q).Scan(&bytes); err != nil || !bytes.Valid || bytes.Int64 == 0 {
		return ""
	}
	return fmt.Sprintf("; idx_nodes_storage_path is %.1f MiB", float64(bytes.Int64)/(1<<20))
}
