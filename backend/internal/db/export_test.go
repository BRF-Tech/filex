package db

import "time"

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
