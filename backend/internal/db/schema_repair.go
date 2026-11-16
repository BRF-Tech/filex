package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
)

// Schema repairs: columns the migrations take for granted that some installs
// never got.
//
// A migration that writes `CREATE TABLE IF NOT EXISTS` for a table an older
// release made somewhere else finds that table and does nothing, and goose
// records it as applied: whatever the older release never added stays
// missing, and no later migration looks again. That is how pending_ops lost
// dest_storage_id (#356). Until 0.38 internal/ops created the table at start,
// from Go, and from 0.27 on an ALTER at start added dest_storage_id. 00036
// made the table a migration, with the column inside its CREATE TABLE IF NOT
// EXISTS, on the belief that every existing install had the column from that
// ALTER. An install whose last start was 0.26.1 or older never ran the ALTER:
// taken straight to 0.38 or later, it kept a pending_ops without the column,
// and every read of the operations list failed with "no such column:
// dest_storage_id". PostgreSQL and MySQL never had the table before 00036 (the
// SQLite DDL run at start was a syntax error there), so they did get it whole.
//
// A repair is not a migration. On an install whose schema already is what the
// migrations make it changes nothing, and on a drifted one it adds exactly the
// column a fresh install has, so a release that carries one is still a patch
// (docs/UPDATES.md: a patch changes no schema) and the deploy's "the database
// is at the newest migration" check still holds. Migrate runs the list after
// goose on every start, which costs one catalogue read per table named here.
//
// ⚠ Add an entry only for a column a fresh install of the same engine has,
// with the DDL that makes it the same way (type, NOT NULL, default): the
// upgrade test (upgrade_schema_test.go) compares an install repaired by this
// list with a fresh one, and TestEverySchemaRepairMakesTheColumnAFreshInstallHas
// drops each column from a fresh database and wants this DDL to bring back the
// same one.
type schemaRepair struct {
	dialect string // goose dialect: "sqlite3", "postgres" or "mysql"
	table   string
	column  string
	ddl     string // adds column, exactly as a fresh install of dialect has it
}

var schemaRepairs = []schemaRepair{
	// #356: the SQLite installs whose last start was 0.26.1 or older and that
	// were then taken to 0.38 or later (00036 is pending_ops' migration).
	{
		dialect: "sqlite3",
		table:   "pending_ops",
		column:  "dest_storage_id",
		ddl:     `ALTER TABLE pending_ops ADD COLUMN dest_storage_id INTEGER NOT NULL DEFAULT 0`,
	},
}

// repairSchema adds every column of schemaRepairs this dialect's database
// lacks, and returns them as "table.column". A table that does not exist is
// left alone: the repairs bring a table to its migrated shape, they do not
// make tables.
func repairSchema(ctx context.Context, dialect string, sqlDB *sql.DB) ([]string, error) {
	var added []string
	seen := map[string]map[string]bool{}
	for _, r := range schemaRepairs {
		if r.dialect != dialect {
			continue
		}
		cols, ok := seen[r.table]
		if !ok {
			var err error
			if cols, err = tableColumns(ctx, dialect, sqlDB, r.table); err != nil {
				return added, fmt.Errorf("db: schema repair: read %s: %w", r.table, err)
			}
			seen[r.table] = cols
		}
		if cols == nil || cols[r.column] {
			continue
		}
		if _, err := sqlDB.ExecContext(ctx, r.ddl); err != nil {
			// Another process starting on the same database may have added it
			// between the read and the ALTER: what counts is that it is there.
			again, rerr := tableColumns(ctx, dialect, sqlDB, r.table)
			if rerr != nil || !again[r.column] {
				return added, fmt.Errorf("db: schema repair: add %s.%s: %w", r.table, r.column, err)
			}
			seen[r.table] = again
			continue
		}
		cols[r.column] = true
		added = append(added, r.table+"."+r.column)
	}
	return added, nil
}

// logSchemaRepairs is the one start-up line an administrator sees when a
// repair ran: which columns were added, and why they were missing.
func logSchemaRepairs(ctx context.Context, dialect string, added []string) {
	if len(added) == 0 {
		return
	}
	slog.InfoContext(ctx, "db: schema repaired: added the column(s) an earlier release left out of this database",
		slog.String("columns", strings.Join(added, ", ")),
		slog.String("engine", dialect))
}

// tableColumns returns the column names of table as the engine's own
// catalogue lists them, lowercased - nil when there is no such table.
func tableColumns(ctx context.Context, dialect string, sqlDB *sql.DB, table string) (map[string]bool, error) {
	var q string
	switch dialect {
	case "sqlite3":
		q = `SELECT name FROM pragma_table_info(?)`
	case "postgres":
		q = `SELECT column_name FROM information_schema.columns
		      WHERE table_schema = current_schema() AND table_name = $1`
	case "mysql":
		q = `SELECT column_name FROM information_schema.columns
		      WHERE table_schema = DATABASE() AND table_name = ?`
	default:
		return nil, fmt.Errorf("no catalogue reader for dialect %q", dialect)
	}
	rows, err := sqlDB.QueryContext(ctx, q, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cols map[string]bool
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if cols == nil {
			cols = map[string]bool{}
		}
		cols[strings.ToLower(name)] = true
	}
	return cols, rows.Err()
}
