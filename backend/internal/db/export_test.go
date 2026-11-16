package db

import (
	"context"
	"database/sql"
	"time"
)

// SubtreeStatement is one statement NodesUnderSQL runs, as the tests that
// read its plan need it.
type SubtreeStatement struct {
	Name  string
	Query string
	Args  []any
}

// SubtreeStatements is every statement NodesUnderSQL would run about dir on
// storageID - the statements themselves, so that a test reading their plans
// (nodes_path_index_test.go) cannot pass on a look-alike. It is promoted to
// each driver's Store with the methods it stands behind.
func (o *NodesUnderSQL) SubtreeStatements(storageID int64, dir string, before time.Time) []SubtreeStatement {
	var out []SubtreeStatement
	add := func(name string, st subtreeStatement, ok bool) {
		if ok {
			out = append(out, SubtreeStatement{Name: name, Query: st.query, Args: st.args})
		}
	}
	st, ok := o.hasLiveStatement(storageID, dir)
	add("HasLiveNodesUnder", st, ok)
	st, ok = o.countLiveStatement(storageID, dir)
	add("CountLiveNodesUnder", st, ok)
	st, ok = o.listStatement(storageID, dir, false)
	add("ListNodesUnder", st, ok)
	st, ok = o.listStatement(storageID, dir, true)
	add("ListNodesUnder, deleted too", st, ok)
	st, ok = o.staleStatement(storageID, dir, before)
	add("ListStaleNodesUnder", st, ok)
	return out
}

// VanishedNodeIDsSQL is the statement ListVanishedNodeIDs runs, for the test
// that reads its plan.
const VanishedNodeIDsSQL = vanishedNodeIDsSQL

// SchemaRepair is one entry of schemaRepairs, for the tests that hold the
// list to what a fresh install has.
type SchemaRepair struct {
	Dialect, Table, Column, DDL string
}

// SchemaRepairs is schemaRepairs as the tests read it.
func SchemaRepairs() []SchemaRepair {
	out := make([]SchemaRepair, 0, len(schemaRepairs))
	for _, r := range schemaRepairs {
		out = append(out, SchemaRepair{Dialect: r.dialect, Table: r.table, Column: r.column, DDL: r.ddl})
	}
	return out
}

// RepairSchema runs the schema repairs alone and says what they added, so a
// test can tell a fixture that needed one from one that did not.
func RepairSchema(ctx context.Context, dialect string, sqlDB *sql.DB) ([]string, error) {
	return repairSchema(ctx, dialect, sqlDB)
}

// TableColumns is the catalogue reader the schema repairs use.
func TableColumns(ctx context.Context, dialect string, sqlDB *sql.DB, table string) (map[string]bool, error) {
	return tableColumns(ctx, dialect, sqlDB, table)
}
