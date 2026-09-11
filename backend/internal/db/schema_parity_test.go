package db_test

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// allowedExtras lists the differences that are a deliberate engine-specific
// answer to the same requirement, keyed by the exact sentence diffSchemas
// produces. Everything not listed here is drift.
var allowedExtras = map[string]map[string]bool{
	"mysql": {
		// 00018: MySQL has no partial indexes, so the trash-aware unique key
		// is built on a STORED generated column that is 1 for live rows and
		// NULL for trashed ones. SQLite and Postgres express the same rule as
		// a WHERE deleted_at IS NULL index and need no column.
		`table "nodes" has column(s) SQLite does not: is_live`: true,
	},
}

// TestSchemaParityAcrossEngines compares the schema each dialect's migrations
// actually build against the SQLite one, which is the dialect every test in
// this repo runs on and therefore the only one kept honest by accident.
//
// The three directories under db/migrations are three hand-maintained copies
// of the same intent. Nothing forces a column added to one to appear in the
// others, and the failure is silent until an operator on the forgotten engine
// writes the row: MySQL was missing storages.replica_target_id, so the first
// CreateStorage on a MySQL install died with "Unknown column" long after the
// migrations reported success (issue #19).
//
// Types are deliberately NOT compared — TEXT/VARCHAR(190)/JSONB are legitimate
// per-engine choices. Names are not: a name that differs is a query that fails.
func TestSchemaParityAcrossEngines(t *testing.T) {
	reference := map[string][]string{}
	found := map[string]map[string][]string{}

	for _, e := range engines() {
		e := e
		t.Run(e.name, func(t *testing.T) {
			sqlDB, _ := openMigrated(t, e)
			schema := readSchema(t, e.name, sqlDB)
			found[e.name] = schema
			if e.name == "sqlite" {
				reference = schema
			}
		})
	}

	if len(reference) == 0 {
		t.Fatal("sqlite schema was not read — the reference dialect must always run")
	}

	for name, schema := range found {
		if name == "sqlite" || len(schema) == 0 {
			continue
		}
		name, schema := name, schema
		t.Run("parity/"+name, func(t *testing.T) {
			for _, diff := range diffSchemas(reference, schema) {
				if allowedExtras[name][diff] {
					continue
				}
				t.Error(diff)
			}
		})
	}
}

// readSchema returns table -> sorted column names, lowercased, with goose's
// bookkeeping table left out. A column that is NOT NULL with no default of its
// own is marked with a trailing "!" — see diffSchemas.
func readSchema(t *testing.T, engine string, sqlDB *sql.DB) map[string][]string {
	t.Helper()

	var q string
	switch engine {
	case "sqlite":
		q = `SELECT m.name, p.name, p."notnull", p.dflt_value IS NOT NULL,
		            CASE WHEN p.pk > 0 THEN 1 ELSE 0 END
		       FROM sqlite_master m
		       JOIN pragma_table_info(m.name) p
		      WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'`
	case "postgres":
		q = `SELECT table_name, column_name,
		            CASE WHEN is_nullable = 'NO' THEN 1 ELSE 0 END,
		            CASE WHEN column_default IS NOT NULL THEN 1 ELSE 0 END,
		            CASE WHEN column_default LIKE 'nextval(%' OR is_identity = 'YES'
		                      OR EXISTS (SELECT 1
		                                   FROM information_schema.table_constraints tc
		                                   JOIN information_schema.key_column_usage k
		                                     ON k.constraint_name = tc.constraint_name
		                                    AND k.table_schema = tc.table_schema
		                                  WHERE tc.constraint_type = 'PRIMARY KEY'
		                                    AND tc.table_schema = c.table_schema
		                                    AND tc.table_name = c.table_name
		                                    AND k.column_name = c.column_name)
		                 THEN 1 ELSE 0 END
		       FROM information_schema.columns c
		      WHERE table_schema = current_schema()`
	case "mysql":
		q = `SELECT table_name, column_name,
		            CASE WHEN is_nullable = 'NO' THEN 1 ELSE 0 END,
		            CASE WHEN column_default IS NOT NULL THEN 1 ELSE 0 END,
		            CASE WHEN extra LIKE '%auto_increment%' OR extra LIKE '%GENERATED%'
		                      OR column_key = 'PRI' THEN 1 ELSE 0 END
		       FROM information_schema.columns
		      WHERE table_schema = DATABASE()`
	default:
		t.Fatalf("no schema query for engine %q", engine)
	}

	rows, err := sqlDB.QueryContext(context.Background(), q)
	if err != nil {
		t.Fatalf("%s: read schema: %v", engine, err)
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var table, column string
		var notNull, hasDefault, generated int
		if err := rows.Scan(&table, &column, &notNull, &hasDefault, &generated); err != nil {
			t.Fatalf("%s: scan schema: %v", engine, err)
		}
		table, column = strings.ToLower(table), strings.ToLower(column)
		if table == "goose_db_version" {
			continue
		}
		// A NOT NULL column with no default is one an INSERT must name. When
		// the engine fills it in itself (auto-increment, identity, generated)
		// — or when it is part of the primary key, which every caller supplies
		// anyway — it is not a demand on the caller.
		if notNull == 1 && hasDefault == 0 && generated == 0 {
			column += "!"
		}
		out[table] = append(out[table], column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: schema rows: %v", engine, err)
	}
	for table := range out {
		sort.Strings(out[table])
	}
	return out
}

func diffSchemas(want, got map[string][]string) []string {
	var problems []string

	for _, table := range sortedKeys(want) {
		gotCols, ok := got[table]
		if !ok {
			problems = append(problems, fmt.Sprintf("table %q is missing", table))
			continue
		}
		wantSet, gotSet := strictness(want[table]), strictness(gotCols)

		var missing, extra []string
		for _, col := range sortedNames(wantSet) {
			gotStrict, present := gotSet[col]
			if !present {
				missing = append(missing, col)
				continue
			}
			// The dangerous direction only: an engine that demands a value
			// SQLite supplies breaks every INSERT the shared store writes.
			if gotStrict && !wantSet[col] {
				problems = append(problems, fmt.Sprintf(
					"table %q column %q is NOT NULL with no default, while SQLite fills it in — "+
						"an INSERT that omits it works on SQLite and fails here", table, col))
			}
		}
		for _, col := range sortedNames(gotSet) {
			if _, ok := wantSet[col]; !ok {
				extra = append(extra, col)
			}
		}
		if len(missing) > 0 {
			problems = append(problems, fmt.Sprintf("table %q is missing column(s): %s",
				table, strings.Join(missing, ", ")))
		}
		if len(extra) > 0 {
			problems = append(problems, fmt.Sprintf("table %q has column(s) SQLite does not: %s",
				table, strings.Join(extra, ", ")))
		}
	}
	for _, table := range sortedKeys(got) {
		if _, ok := want[table]; !ok {
			problems = append(problems, fmt.Sprintf("table %q exists but SQLite has no such table", table))
		}
	}
	return problems
}

// strictness splits readSchema's "name" / "name!" encoding into a lookup of
// column name -> "an INSERT must name this column".
func strictness(cols []string) map[string]bool {
	out := make(map[string]bool, len(cols))
	for _, c := range cols {
		if name, ok := strings.CutSuffix(c, "!"); ok {
			out[name] = true
			continue
		}
		out[c] = false
	}
	return out
}

func sortedNames(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
