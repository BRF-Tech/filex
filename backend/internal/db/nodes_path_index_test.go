package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
)

// TestSubtreeQuestionsAreAnsweredFromThePathIndexOnEveryEngine: what is below
// a folder is found through an index on nodes.path (migration 00081), by every
// statement that asks.
//
// ⚠⚠ Until 00081 nothing indexed nodes.path, and "below this folder" was
// SUBSTR(path,1,n)=?, which no index can answer: counting or listing what is
// below a folder read every row of the storage. A folder rescan asked, and
// every scan of a storage for its own two trees. The encryption policy (00080)
// then asked for every folder somebody may encrypt (POST
// /api/files/e2e/allowed: the explorer when a menu opens on one, an API caller
// for up to 1000 at a time), under the approval policy and for whoever holds
// files.encrypt there and is not an administrator. Measured on a catalogue of
// 170,245 rows (TestMeasureSubtreeQuestions, -tags measure), per folder: 206
// to 262 ms on SQLite, where the store has ONE connection and every other
// request waits behind it; 140 to 154 ms on MySQL; 15 to 26 ms on PostgreSQL.
// With the index: 0.02 ms, 0.15 ms and 0.4 ms.
//
// The plans read here are those of the statements the store runs
// (SubtreeStatements), not of a query written for the test: an OR between the
// two path spellings has the same rows and, for the listing and the stale rows
// on SQLite, the old plan; and a range PostgreSQL is asked for in the
// database's own collation is one its index cannot be entered with (and not
// even the same rows: TestSubtreeQuestionsMatchTheFolderExactlyOnEveryEngine).
func TestSubtreeQuestionsAreAnsweredFromThePathIndexOnEveryEngine(t *testing.T) {
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
			// Enough rows, in two storages, that the storage alone is a poor
			// way in and the folder a good one - on PostgreSQL 16 and 17, the
			// two this was run on (filex supports 13 and up). The folder's own
			// row is asked for with an `= ANY`, and on a table of one or two
			// index pages the planner prices that against reading the
			// storage's rows by storage_id alone: at 78 rows a storage
			// PostgreSQL 16 chose the storage, and at 84 to 104 PostgreSQL 17
			// did too. From 117 up neither does; this is 372.
			require.NoError(t, store.WithTx(ctx, func(ctx context.Context) error {
				mk := func(storageID int64, p string, typ model.NodeType) error {
					_, err := store.CreateNode(ctx, &model.Node{
						StorageID: storageID, Name: path.Base(p), Path: p, PathHash: pathkey.Hash(storageID, p),
						StorageKey: p, Type: typ, Size: 1,
					})
					return err
				}
				for _, id := range []int64{st.ID, other.ID} {
					for a := 0; a < 12; a++ {
						dir := fmt.Sprintf("/Müşteri-%d", a)
						if err := mk(id, dir, model.NodeTypeDirectory); err != nil {
							return err
						}
						for f := 0; f < 30; f++ {
							if err := mk(id, fmt.Sprintf("%s/belge-%02d.pdf", dir, f), model.NodeTypeFile); err != nil {
								return err
							}
						}
					}
				}
				return nil
			}))

			asked, ok := store.(interface {
				SubtreeStatements(storageID int64, dir string, before time.Time) []db.SubtreeStatement
			})
			require.True(t, ok, "the %s store does not answer subtree questions through db.NodesUnderSQL", e.name)
			// A cut-off an hour ahead, on purpose: every row was last seen
			// before it, so the folder's range is the selective term. With a
			// cut-off few rows are older than, a cost-based planner is right to
			// enter by (storage_id, seen_at) instead.
			statements := asked.SubtreeStatements(st.ID, "Müşteri-3", time.Now().Add(time.Hour))
			require.Len(t, statements, 5)

			for _, s := range statements {
				plan := explainSubtree(t, e.name, sqlDB, s)
				require.NotEmpty(t, plan, "%s: %s has no plan", e.name, s.Name)
				for _, line := range plan {
					if !readsNodes(e.name, line) {
						continue
					}
					require.True(t, usesPathIndex(e.name, line),
						"%s: %s reads nodes without the folder's range on idx_nodes_storage_path:\n  %s\nthe whole plan:\n  %s",
						e.name, s.Name, line, strings.Join(plan, "\n  "))
					// What deleted_at is in the SQLite index for: whether
					// anything live is below, and how many, are read off the
					// index without fetching a row.
					if e.name == "sqlite" && (s.Name == "HasLiveNodesUnder" || s.Name == "CountLiveNodesUnder") {
						require.Contains(t, line, "USING COVERING INDEX idx_nodes_storage_path ",
							"sqlite: %s goes to the table for every row in the folder's range:\n  %s", s.Name, line)
					}
				}
				require.True(t, slices.ContainsFunc(plan, func(l string) bool { return readsNodes(e.name, l) }),
					"%s: %s: no line of the plan reads nodes - the test no longer understands this engine's plans:\n  %s",
					e.name, s.Name, strings.Join(plan, "\n  "))
			}
		})
	}
}

// TestThePathIndexIsOnlyForStatementsThatComparePath: on SQLite the index of
// 00081 changes the plan of no statement that does not compare path.
//
// ⚠⚠ SQLite has no statistics here - nothing runs ANALYZE - and with none it
// enters nodes, for a statement that only says `storage_id=?`, through
// whichever index led by storage_id it estimates narrowest. A plain
// (storage_id, path) index is that one. Written that way first, it took eight
// of the driver's statements off their own index - five that never mention
// path, three that read it only through SUBSTR - and the name search
// (SearchNodes) off the only index that holds `name`: every candidate row was
// then read from the table - 27 ms became 51 ms on a storage of 152,562 rows
// written in path order and 240 ms on one that was not
// (FILEX_MEASURE_PLAIN_INDEX=1 in TestMeasureSubtreeQuestions) - with every
// test green. An install that HAS statistics, from an ANALYZE of its own,
// loses its plans the same way: the new index has no row among them.
//
// The index is partial - WHERE path IS NOT NULL, true of every row - so the
// planner may use it only for a statement that compares path. Read here: the
// plan of every statement of the SQLite driver that can be told from its
// source (the extractor of the MySQL prepare gate; the ones put together at
// run time it cannot), and by hand the name search and the statements
// internal/db shares between the engines that reach nodes by storage
// (SetNodeDeletedBy, ThumbnailGenerators and, last, ListVanishedNodeIDs). What
// NodesUnderSQL asks is not among them: it is meant to use the index.
func TestThePathIndexIsOnlyForStatementsThatComparePath(t *testing.T) {
	sqlDB, _ := openMigrated(t, sqliteEngine(t))
	plan := func(query string) string {
		args := make([]any, strings.Count(query, "?"))
		for i := range args {
			args[i] = 1
		}
		rows, err := sqlDB.QueryContext(context.Background(), `EXPLAIN QUERY PLAN `+query, args...)
		require.NoError(t, err, "SQLite does not explain: %s", query)
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
			lines = append(lines, detail)
		}
		require.NoError(t, rows.Err())
		return strings.Join(lines, " | ")
	}

	stmts, _ := extractStoreSQL(t, filepath.Join("drivers", "sqlite"))
	stmts = append(stmts,
		// What SearchNodes builds for a plain word.
		storeStatement{at: "sqlite.go", fn: "SearchNodes", sql: `SELECT id, name FROM nodes
			WHERE storage_id=? AND deleted_at IS NULL AND name LIKE ? ORDER BY name LIMIT ?`},
		// It reads path, but through SUBSTR: that compares nothing an index holds.
		storeStatement{at: "node_deleted_by_sql.go", fn: "SetNodeDeletedBy", sql: `UPDATE nodes SET deleted_by=?
			WHERE storage_id=? AND deleted_at IS NOT NULL AND deleted_by IS NULL AND SUBSTR(path,1,?)=?`},
		// What ThumbnailGenerators builds for a set of storages: it joins
		// nodes and enters them by storage.
		storeStatement{at: "file_assoc_sql.go", fn: "ThumbnailGenerators", sql: `SELECT COALESCE(t.generator,''), COUNT(*) FROM thumbnails t JOIN nodes n ON n.id = t.node_id
			WHERE t.state = 'ready' AND n.deleted_at IS NULL AND n.storage_id IN (?,?) GROUP BY COALESCE(t.generator,'')`})
	readNodes := 0
	for _, s := range stmts {
		p := plan(s.sql)
		if sqliteReadsNodes.MatchString(p) {
			readNodes++
		}
		require.False(t, namesPathIndex.MatchString(p),
			"%s (%s) is planned on the path index, and compares no path:\n  %s\n  %s",
			s.at, s.fn, strings.Join(strings.Fields(s.sql), " "), p)
	}
	require.GreaterOrEqual(t, readNodes, 30, "the extractor found too few statements that read nodes to mean anything")

	// Two statements that were there before compare path. ListLiveNodesInTrash
	// does it inside an OR, which opens no partial index, and keeps its plan
	// (it is in the loop above). The rows deleted where they stood are the
	// deleted rows OUTSIDE the trash (`path <> ?`, ListVanishedNodeIDs): that
	// one may use the index and does - alone, without going to the table for a
	// row, where it used to fetch every row of the storage through
	// (storage_id, seen_at).
	vanished := plan(db.VanishedNodeIDsSQL)
	require.NotEmpty(t, vanished)
	require.NotRegexp(t, `USING INDEX idx_nodes_storage_path\b`, vanished,
		"ListVanishedNodeIDs reads the path index and then every row from the table: %s", vanished)
}

// sqliteEngine is the engine every test runs on, for the tests that are about
// SQLite alone.
func sqliteEngine(t *testing.T) engine {
	t.Helper()
	for _, e := range engines() {
		if e.name == "sqlite" {
			return e
		}
	}
	t.Fatal("engines() no longer lists sqlite")
	return engine{}
}

// explainSubtree returns the engine's plan for s, one line per access.
func explainSubtree(t *testing.T, engine string, sqlDB *sql.DB, s db.SubtreeStatement) []string {
	t.Helper()
	ctx := context.Background()
	var plan []string
	switch engine {
	case "sqlite":
		rows, err := sqlDB.QueryContext(ctx, `EXPLAIN QUERY PLAN `+s.Query, s.Args...)
		require.NoError(t, err, "%s", s.Name)
		defer rows.Close()
		for rows.Next() {
			var id, parent, notused int
			var detail string
			require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
			plan = append(plan, detail)
		}
		require.NoError(t, rows.Err())
	case "postgres":
		// A table this small is read whole whatever it is asked. With the
		// sequential scan priced out and the statistics autovacuum would have
		// gathered, the plan shows which index answers the statement - which
		// is all that differs at 170,000 rows.
		tx, err := sqlDB.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer func() { _ = tx.Rollback() }()
		_, err = tx.ExecContext(ctx, `ANALYZE nodes`)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `SET LOCAL enable_seqscan = off`)
		require.NoError(t, err)
		rows, err := tx.QueryContext(ctx, `EXPLAIN (COSTS OFF) `+s.Query, s.Args...)
		require.NoError(t, err, "%s", s.Name)
		defer rows.Close()
		for rows.Next() {
			var line string
			require.NoError(t, rows.Scan(&line))
			plan = append(plan, line)
		}
		require.NoError(t, rows.Err())
	case "mysql":
		// The table form, by name: MySQL 9 answers a bare EXPLAIN as a tree.
		rows, err := sqlDB.QueryContext(ctx, `EXPLAIN FORMAT=TRADITIONAL `+s.Query, s.Args...)
		require.NoError(t, err, "%s", s.Name)
		defer rows.Close()
		cols, err := rows.Columns()
		require.NoError(t, err)
		for rows.Next() {
			vals := make([]sql.NullString, len(cols))
			dest := make([]any, len(cols))
			for i := range vals {
				dest[i] = &vals[i]
			}
			require.NoError(t, rows.Scan(dest...))
			var b strings.Builder
			for i, c := range cols {
				if c == "table" || c == "type" || c == "key" || c == "Extra" {
					fmt.Fprintf(&b, "%s=%s ", c, vals[i].String)
				}
			}
			plan = append(plan, strings.TrimSpace(b.String()))
		}
		require.NoError(t, rows.Err())
	default:
		t.Fatalf("no plan reader for engine %q", engine)
	}
	return plan
}

var (
	// The index by its whole name: idx_nodes_storage_pathhash is another.
	namesPathIndex     = regexp.MustCompile(`\bidx_nodes_storage_path\b`)
	sqliteReadsNodes   = regexp.MustCompile(`\b(SCAN|SEARCH) nodes\b`)
	sqlitePathRange    = regexp.MustCompile(`^SEARCH nodes USING (COVERING )?INDEX idx_nodes_storage_path \(storage_id=\? AND path[=><]`)
	postgresReadsNodes = regexp.MustCompile(`\bScan\b.*\b(on nodes|on idx_nodes_|using idx_nodes_|using nodes_)`)
	postgresPathIndex  = regexp.MustCompile(`\b(Index Scan|Index Only Scan|Bitmap Index Scan)\b.*\bidx_nodes_storage_path\b`)
	postgresHeapOfPath = regexp.MustCompile(`\bBitmap Heap Scan on nodes\b`)
	mysqlPathRange     = regexp.MustCompile(`\btype=range key=idx_nodes_storage_path\b`)
)

// readsNodes reports whether a plan line is an access to the nodes table.
func readsNodes(engine, line string) bool {
	switch engine {
	case "sqlite":
		return sqliteReadsNodes.MatchString(line)
	case "postgres":
		return postgresReadsNodes.MatchString(line) || strings.Contains(line, "Index Cond:")
	default:
		return strings.Contains(line, "table=nodes ")
	}
}

// usesPathIndex reports whether that access is the folder's range on
// idx_nodes_storage_path, and not a read of the storage's rows.
func usesPathIndex(engine, line string) bool {
	switch engine {
	case "sqlite":
		return sqlitePathRange.MatchString(strings.TrimSpace(line))
	case "postgres":
		if strings.Contains(line, "Index Cond:") {
			// The range itself is what the index is entered with; an index
			// read for storage_id alone is every row of the storage.
			return strings.Contains(line, "storage_id") && strings.Contains(line, "path")
		}
		return postgresPathIndex.MatchString(line) || postgresHeapOfPath.MatchString(line)
	default:
		return mysqlPathRange.MatchString(line)
	}
}
