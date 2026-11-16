package db_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
)

// upgradeFixtures are the releases whose first-start schema is kept in
// testdata/upgrade (make-fixture.sh writes them from each release's published
// image), and the schema repairs each one needs on the way to this tree.
//
//   - 0.1.57, the first public release, and 0.10.0, the release of the
//     installs #356 was found on, and 0.26.1, the last one before the ALTER at
//     start that added pending_ops.dest_storage_id: all three made pending_ops
//     at start without that column, and 00036's CREATE TABLE IF NOT EXISTS
//     kept it so.
//   - 0.37.0, the last release that made pending_ops at start (with the
//     ALTER), and 0.38.0, the first that made it with a migration: the column
//     is there, nothing to repair.
//   - 0.61.0, the newest release when this test was written. A migration that
//     shipped and was edited afterwards reaches fresh installs and never the
//     ones that ran it already: this fixture, and every newer one added here,
//     is where that shows.
//
// Every file in testdata/upgrade must be listed: a fixture added without an
// entry fails, so whoever adds one says what it needs.
var upgradeFixtures = map[string][]string{
	"0.1.57": {"pending_ops.dest_storage_id"},
	"0.10.0": {"pending_ops.dest_storage_id"},
	"0.26.1": {"pending_ops.dest_storage_id"},
	"0.37.0": nil,
	"0.38.0": nil,
	"0.61.0": nil,
}

var fixtureHeader = regexp.MustCompile(`(?m)^-- filex-fixture-(version|goose): (\S+)$`)

// TestAnUpgradedSQLiteInstallHasTheSchemaOfAFreshOne takes the schema each
// kept release left on its first start to this tree's, the way an install
// upgraded in place gets there (db.Migrate: goose, then the schema repairs),
// and compares it with a fresh install's, table by table, column by column,
// index by index. Any difference is a query that works on a new install and
// fails on an upgraded one: #356 was one column, pending_ops.dest_storage_id,
// on installs from 0.26.1 and older, and every GET /api/files/ops failed there
// with "no such column" while the server reported itself healthy.
//
// A row the old release left in pending_ops is carried through as well: the
// repair keeps the rows, and the column it adds reads 0 on them, as on a
// fresh install's default.
func TestAnUpgradedSQLiteInstallHasTheSchemaOfAFreshOne(t *testing.T) {
	ctx := context.Background()
	drv, err := db.Get("sqlite")
	require.NoError(t, err)

	freshDB, err := drv.Open(ctx, filepath.Join(t.TempDir(), "fresh.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = freshDB.Close() })
	require.NoError(t, db.Migrate(ctx, drv, freshDB))
	want := readSQLiteCatalogue(t, freshDB)

	files, err := filepath.Glob(filepath.Join("testdata", "upgrade", "sqlite-v*.sql"))
	require.NoError(t, err)
	seen := map[string]bool{}
	for _, f := range files {
		script, err := os.ReadFile(f)
		require.NoError(t, err)
		header := map[string]string{}
		for _, m := range fixtureHeader.FindAllStringSubmatch(string(script), -1) {
			header[m[1]] = m[2]
		}
		version := header["version"]
		require.NotEmpty(t, version, "%s: no filex-fixture-version line", f)
		require.Equal(t, "sqlite-v"+version+".sql", filepath.Base(f), "%s: named after another release", f)
		repairs, listed := upgradeFixtures[version]
		require.True(t, listed, "%s: add v%s to upgradeFixtures with the repairs it needs", f, version)
		seen[version] = true
		gooseAt, err := strconv.ParseInt(header["goose"], 10, 64)
		require.NoError(t, err, "%s: filex-fixture-goose", f)

		t.Run("v"+version, func(t *testing.T) {
			sqlDB, err := drv.Open(ctx, filepath.Join(t.TempDir(), "upgraded.sqlite"))
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })
			_, err = sqlDB.ExecContext(ctx, string(script))
			require.NoError(t, err, "load the schema v%s left", version)
			var at int64
			require.NoError(t, sqlDB.QueryRowContext(ctx,
				`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&at))
			require.Equal(t, gooseAt, at, "the fixture is at the migration its header says")

			// What the old release left in its queue: a copy that ran before
			// the upgrade. Only the columns every release had.
			res, err := sqlDB.ExecContext(ctx, `INSERT INTO pending_ops
				(kind, storage_id, sources_json, dest, total, done, status)
				VALUES ('copy', 7, '["/a.txt"]', '/b', 1, 1, 'done')`)
			require.NoError(t, err)
			opID, err := res.LastInsertId()
			require.NoError(t, err)

			// goose alone first, so the repairs can be told apart from the
			// migrations: they are what this release needs on top.
			goose.SetBaseFS(drv.MigrationsFS())
			require.NoError(t, goose.SetDialect(drv.Dialect()))
			err = goose.UpContext(ctx, sqlDB, ".")
			goose.SetBaseFS(nil)
			require.NoError(t, err, "the migrations from v%s up", version)
			added, err := db.RepairSchema(ctx, drv.Dialect(), sqlDB)
			require.NoError(t, err)
			assert.ElementsMatch(t, repairs, added, "the schema repairs an install from v%s needs", version)

			// And then what a start does: nothing more to do.
			require.NoError(t, db.Migrate(ctx, drv, sqlDB), "a start after the upgrade")
			again, err := db.RepairSchema(ctx, drv.Dialect(), sqlDB)
			require.NoError(t, err)
			require.Empty(t, again, "a repair runs once")

			for _, d := range diffSQLiteCatalogues(want, readSQLiteCatalogue(t, sqlDB)) {
				t.Errorf("an install upgraded from v%s differs from a fresh one: %s", version, d)
			}

			var kind, status string
			var storageID, destStorageID int64
			require.NoError(t, sqlDB.QueryRowContext(ctx,
				`SELECT kind, storage_id, dest_storage_id, status FROM pending_ops WHERE id = ?`, opID).
				Scan(&kind, &storageID, &destStorageID, &status), "the op v%s left is still there", version)
			require.Equal(t, []any{"copy", int64(7), int64(0), "done"}, []any{kind, storageID, destStorageID, status})
		})
	}
	for version := range upgradeFixtures {
		require.True(t, seen[version], "testdata/upgrade/sqlite-v%s.sql is missing: make-fixture.sh %s", version, version)
	}
}

// TestTheUpgradeComparisonSeesWhatItLooksFor keeps the comparison above from
// passing on nothing: a fresh database with an index dropped, a column added
// and a column remade with another collation (which no pragma reports) reads
// as exactly those differences.
func TestTheUpgradeComparisonSeesWhatItLooksFor(t *testing.T) {
	ctx := context.Background()
	drv, err := db.Get("sqlite")
	require.NoError(t, err)
	open := func(name string) *sql.DB {
		sqlDB, err := drv.Open(ctx, filepath.Join(t.TempDir(), name))
		require.NoError(t, err)
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, db.Migrate(ctx, drv, sqlDB))
		return sqlDB
	}
	want := readSQLiteCatalogue(t, open("fresh.sqlite"))
	other := open("drifted.sqlite")
	require.Empty(t, diffSQLiteCatalogues(want, readSQLiteCatalogue(t, other)), "two fresh installs are the same")
	for _, stmt := range []string{
		`DROP INDEX idx_pending_ops_actor`,
		`ALTER TABLE pending_ops ADD COLUMN note TEXT`,
		`ALTER TABLE pending_ops DROP COLUMN error`,
		`ALTER TABLE pending_ops ADD COLUMN error TEXT COLLATE NOCASE`,
	} {
		_, err := other.ExecContext(ctx, stmt)
		require.NoError(t, err, stmt)
	}
	got := diffSQLiteCatalogues(want, readSQLiteCatalogue(t, other))
	require.Len(t, got, 4, "%q", got)
	joined := strings.Join(got, " | ")
	for _, s := range []string{
		`table "pending_ops" has column "note" a fresh install does not`,
		`table "pending_ops" lacks the definition "error TEXT"`,
		`table "pending_ops" has the definition "error TEXT COLLATE NOCASE" a fresh install does not`,
		`index idx_pending_ops_actor is missing`,
	} {
		require.Contains(t, joined, s)
	}
}

// TestEverySchemaRepairMakesTheColumnAFreshInstallHas holds schemaRepairs to
// its one rule: an entry adds a column a fresh install of its engine has, and
// makes it the way the migrations do. Each column is dropped from a fresh
// database and Migrate must bring the same one back, on the engines this run
// has (SQLite always; PostgreSQL and MySQL with their DSNs).
func TestEverySchemaRepairMakesTheColumnAFreshInstallHas(t *testing.T) {
	repairs := db.SchemaRepairs()
	require.NotEmpty(t, repairs)
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			drv, err := db.Get(e.driver)
			require.NoError(t, err)
			var mine []db.SchemaRepair
			for _, r := range repairs {
				if r.Dialect == drv.Dialect() {
					mine = append(mine, r)
				}
			}
			if len(mine) == 0 {
				t.Skipf("no schema repair for %s", e.name)
			}
			sqlDB, _ := openMigrated(t, e)
			ctx := context.Background()
			for _, r := range mine {
				before := readSchema(t, e.name, sqlDB)
				require.Contains(t, before[r.Table], columnEntry(before[r.Table], r.Column),
					"%s: a fresh install has %s.%s", e.name, r.Table, r.Column)
				var detail map[string]string
				if e.name == "sqlite" {
					detail = readSQLiteCatalogue(t, sqlDB).columns[r.Table]
				}

				_, err := sqlDB.ExecContext(ctx, `ALTER TABLE `+r.Table+` DROP COLUMN `+r.Column)
				require.NoError(t, err, "%s: drop %s.%s", e.name, r.Table, r.Column)
				require.NoError(t, db.Migrate(ctx, drv, sqlDB), "%s: the start that repairs it", e.name)

				after := readSchema(t, e.name, sqlDB)
				require.Equal(t, before[r.Table], after[r.Table], "%s: %s as a fresh install has it", e.name, r.Table)
				if detail != nil {
					require.Equal(t, detail, readSQLiteCatalogue(t, sqlDB).columns[r.Table],
						"%s: %s, column by column", e.name, r.Table)
				}
			}
		})
	}
}

// TestTheSchemaRepairsReadTheCatalogueOfEveryEngine: the one reader the
// repairs decide with sees a table's columns on each engine this run has, and
// says nil for a table that does not exist (which the repairs leave alone).
func TestTheSchemaRepairsReadTheCatalogueOfEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, drv := openMigrated(t, e)
			ctx := context.Background()
			cols, err := db.TableColumns(ctx, drv.Dialect(), sqlDB, "pending_ops")
			require.NoError(t, err)
			want := map[string]bool{}
			for _, c := range readSchema(t, e.name, sqlDB)["pending_ops"] {
				want[strings.TrimSuffix(c, "!")] = true
			}
			require.Equal(t, want, cols, "%s: pending_ops as the engine lists it", e.name)
			require.True(t, cols["dest_storage_id"])

			none, err := db.TableColumns(ctx, drv.Dialect(), sqlDB, "no_such_table")
			require.NoError(t, err)
			require.Nil(t, none, "%s: no table, no columns", e.name)
		})
	}
}

// TestASchemaRepairSaysSoOnceAtStart: the administrator of an install that was
// repaired reads one line about it in the start-up log, and none on the
// starts after.
func TestASchemaRepairSaysSoOnceAtStart(t *testing.T) {
	ctx := context.Background()
	drv, err := db.Get("sqlite")
	require.NoError(t, err)
	sqlDB, err := drv.Open(ctx, filepath.Join(t.TempDir(), "repaired.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Migrate(ctx, drv, sqlDB))
	_, err = sqlDB.ExecContext(ctx, `ALTER TABLE pending_ops DROP COLUMN dest_storage_id`)
	require.NoError(t, err)

	log := captureDefaultLog(t)
	require.NoError(t, db.Migrate(ctx, drv, sqlDB))
	require.NoError(t, db.Migrate(ctx, drv, sqlDB))

	var lines []string
	for _, l := range strings.Split(strings.TrimSpace(log.String()), "\n") {
		if strings.Contains(l, "schema repaired") {
			lines = append(lines, l)
		}
	}
	require.Len(t, lines, 1, "one line, on the start that repaired it: %q", log.String())
	require.Contains(t, lines[0], "level=INFO")
	require.Contains(t, lines[0], "pending_ops.dest_storage_id")
}

// captureDefaultLog points slog's default logger at a buffer, in text, for
// the rest of the test.
func captureDefaultLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// columnEntry is readSchema's spelling of column in cols ("name" or "name!").
func columnEntry(cols []string, column string) string {
	for _, c := range cols {
		if strings.TrimSuffix(c, "!") == column {
			return c
		}
	}
	return column
}

// sqliteCatalogue is everything SQLite says about a schema that a query can
// trip over, keyed so that two databases compare entry by entry. Column order
// is deliberately not part of it: a column a repair adds (or an earlier
// release's ALTER added) comes last, and filex names every column it reads
// and writes.
type sqliteCatalogue struct {
	// table -> column -> its declared type, NOT NULL, default, key place and
	// whether it is hidden or generated (pragma table_xinfo).
	columns map[string]map[string]string
	// table -> the items of its CREATE TABLE, whitespace folded: what the
	// pragma does not report (CHECK, COLLATE, REFERENCES, the table's own
	// options) is still compared.
	definitions map[string]map[string]bool
	// index -> its table, uniqueness, origin, partial clause and key columns
	// (pragma index_list / index_xinfo), and its CREATE INDEX.
	indexes map[string]string
	// "view x" / "trigger y" -> its definition.
	others map[string]string
}

func readSQLiteCatalogue(t *testing.T, sqlDB *sql.DB) sqliteCatalogue {
	t.Helper()
	ctx := context.Background()
	c := sqliteCatalogue{
		columns:     map[string]map[string]string{},
		definitions: map[string]map[string]bool{},
		indexes:     map[string]string{},
		others:      map[string]string{},
	}
	type object struct{ kind, name, table, sql string }
	var objects []object
	rows, err := sqlDB.QueryContext(ctx, `SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%' OR type = 'index'`)
	require.NoError(t, err)
	for rows.Next() {
		var o object
		require.NoError(t, rows.Scan(&o.kind, &o.name, &o.table, &o.sql))
		if o.table == "goose_db_version" || strings.HasPrefix(o.table, "sqlite_") {
			continue
		}
		objects = append(objects, o)
	}
	require.NoError(t, rows.Err())
	rows.Close()

	for _, o := range objects {
		switch o.kind {
		case "table":
			cols := map[string]string{}
			r, err := sqlDB.QueryContext(ctx, `SELECT name, type, "notnull", COALESCE(dflt_value, '<none>'), pk, hidden
				FROM pragma_table_xinfo(?)`, o.name)
			require.NoError(t, err)
			for r.Next() {
				var name, typ, dflt string
				var notNull, pk, hidden int
				require.NoError(t, r.Scan(&name, &typ, &notNull, &dflt, &pk, &hidden))
				cols[strings.ToLower(name)] = fmt.Sprintf("%s notnull=%d default=%s pk=%d hidden=%d",
					strings.ToUpper(typ), notNull, dflt, pk, hidden)
			}
			require.NoError(t, r.Err())
			r.Close()
			c.columns[o.name] = cols
			c.definitions[o.name] = tableDefinitionItems(o.sql)
		case "index":
			c.indexes[o.name] = describeSQLiteIndex(t, sqlDB, o.table, o.name) + " " + foldSQL(o.sql)
		default:
			c.others[o.kind+" "+o.name] = foldSQL(o.sql)
		}
	}
	return c
}

func describeSQLiteIndex(t *testing.T, sqlDB *sql.DB, table, index string) string {
	t.Helper()
	ctx := context.Background()
	var unique, partial int
	var origin string
	require.NoError(t, sqlDB.QueryRowContext(ctx,
		`SELECT "unique", origin, partial FROM pragma_index_list(?) WHERE name = ?`, table, index).
		Scan(&unique, &origin, &partial), "index %s on %s", index, table)
	r, err := sqlDB.QueryContext(ctx,
		`SELECT seqno, cid, COALESCE(name, '<expr>'), "desc", coll FROM pragma_index_xinfo(?) WHERE key = 1 ORDER BY seqno`, index)
	require.NoError(t, err)
	defer r.Close()
	var keys []string
	for r.Next() {
		var seq, cid, desc int
		var name, coll string
		require.NoError(t, r.Scan(&seq, &cid, &name, &desc, &coll))
		keys = append(keys, fmt.Sprintf("%s desc=%d coll=%s", strings.ToLower(name), desc, coll))
	}
	require.NoError(t, r.Err())
	return fmt.Sprintf("on %s unique=%d origin=%s partial=%d keys=(%s)", table, unique, origin, partial, strings.Join(keys, "; "))
}

var (
	sqlLineComment = regexp.MustCompile(`--[^\n]*`)
	sqlSpace       = regexp.MustCompile(`\s+`)
	sqlPunctSpace  = regexp.MustCompile(`\s*([(),])\s*`)
)

// foldSQL folds whitespace and drops line comments, so that the same
// statement written with tabs (Go, at start) or spaces (a migration) reads the
// same.
func foldSQL(s string) string {
	s = sqlLineComment.ReplaceAllString(s, " ")
	s = sqlSpace.ReplaceAllString(s, " ")
	s = sqlPunctSpace.ReplaceAllString(s, "$1")
	return strings.TrimSpace(s)
}

// tableDefinitionItems splits a CREATE TABLE into its column definitions and
// table constraints (the commas at the top level of its parentheses), plus
// what follows them (WITHOUT ROWID, STRICT). The name before the parentheses
// is left out: ALTER TABLE ... RENAME rewrites it with quotes.
func tableDefinitionItems(create string) map[string]bool {
	s := foldSQL(create)
	open := strings.Index(s, "(")
	end := strings.LastIndex(s, ")")
	items := map[string]bool{}
	if open < 0 || end < open {
		items[s] = true
		return items
	}
	depth, quote, start := 0, byte(0), open+1
	body := s[:end]
	for i := open + 1; i < len(body); i++ {
		ch := body[i]
		switch {
		case quote != 0:
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"' || ch == '`':
			quote = ch
		case ch == '(':
			depth++
		case ch == ')':
			depth--
		case ch == ',' && depth == 0:
			items[strings.TrimSpace(body[start:i])] = true
			start = i + 1
		}
	}
	items[strings.TrimSpace(body[start:])] = true
	if rest := strings.TrimSpace(s[end+1:]); rest != "" {
		items["options: "+rest] = true
	}
	return items
}

// diffSQLiteCatalogues lists what got (an upgraded database) has that want (a
// fresh one) does not, and the reverse, one sentence each.
func diffSQLiteCatalogues(want, got sqliteCatalogue) []string {
	var out []string
	for _, table := range sortedStringKeys(want.columns) {
		gotCols, ok := got.columns[table]
		if !ok {
			out = append(out, fmt.Sprintf("table %q is missing", table))
			continue
		}
		reported := map[string]bool{}
		for _, col := range sortedStringKeys(want.columns[table]) {
			g, ok := gotCols[col]
			switch {
			case !ok:
				out = append(out, fmt.Sprintf("table %q is missing column %q (%s)", table, col, want.columns[table][col]))
				reported[col] = true
			case g != want.columns[table][col]:
				out = append(out, fmt.Sprintf("table %q column %q is %s, fresh: %s", table, col, g, want.columns[table][col]))
				reported[col] = true
			}
		}
		for _, col := range sortedStringKeys(gotCols) {
			if _, ok := want.columns[table][col]; !ok {
				out = append(out, fmt.Sprintf("table %q has column %q a fresh install does not (%s)", table, col, gotCols[col]))
				reported[col] = true
			}
		}
		firstWord := func(item string) string {
			return strings.ToLower(strings.Trim(strings.SplitN(item, " ", 2)[0], "\"`[]"))
		}
		for _, item := range sortedBoolKeys(want.definitions[table]) {
			if !got.definitions[table][item] && !reported[firstWord(item)] {
				out = append(out, fmt.Sprintf("table %q lacks the definition %q", table, item))
			}
		}
		for _, item := range sortedBoolKeys(got.definitions[table]) {
			if !want.definitions[table][item] && !reported[firstWord(item)] {
				out = append(out, fmt.Sprintf("table %q has the definition %q a fresh install does not", table, item))
			}
		}
	}
	for _, table := range sortedStringKeys(got.columns) {
		if _, ok := want.columns[table]; !ok {
			out = append(out, fmt.Sprintf("table %q exists, a fresh install has no such table", table))
		}
	}
	out = append(out, diffNamed("index", want.indexes, got.indexes)...)
	out = append(out, diffNamed("", want.others, got.others)...)
	return out
}

func diffNamed(kind string, want, got map[string]string) []string {
	var out []string
	label := func(name string) string {
		if kind == "" {
			return name
		}
		return kind + " " + name
	}
	for _, name := range sortedStringKeys(want) {
		g, ok := got[name]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("%s is missing (%s)", label(name), want[name]))
		case g != want[name]:
			out = append(out, fmt.Sprintf("%s is %s, fresh: %s", label(name), g, want[name]))
		}
	}
	for _, name := range sortedStringKeys(got) {
		if _, ok := want[name]; !ok {
			out = append(out, fmt.Sprintf("%s exists, a fresh install has no such one (%s)", label(name), got[name]))
		}
	}
	return out
}

func sortedStringKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedBoolKeys(m map[string]bool) []string { return sortedStringKeys(m) }
