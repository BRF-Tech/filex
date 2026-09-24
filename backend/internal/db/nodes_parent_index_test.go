package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNodesParentIDIsIndexedOnEveryEngine: a node's children are found through
// an index.
//
// ⚠⚠ nodes.parent_id references nodes(id) ON DELETE CASCADE, and on SQLite
// and PostgreSQL nothing indexed it: the only index holding the column is
// (storage_id, parent_id, name), which cannot answer "parent_id = ?". So every
// hard delete of a node — each row a trash purge removes — made the engine read
// the whole nodes table looking for children to cascade to. Measured on a
// production copy (231,074 nodes): 300 ms a row, 85 ms with the index. And on
// SQLite the store runs one connection (SetMaxOpenConns(1)), so a purge of
// 61,844 rows at two rows a second held that connection for hours while every
// other request queued behind it: p50 19 ms before the purge, 333 ms during it,
// 46 ms once the index existed.
//
// MySQL needs no migration: InnoDB refuses a foreign key without an index led
// by its column and creates one itself (fk_nodes_parent). The test holds it to
// that too.
func TestNodesParentIDIsIndexedOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, _ := openMigrated(t, e)
			ctx := context.Background()
			switch e.name {
			case "sqlite":
				rows, err := sqlDB.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT id FROM nodes WHERE parent_id = 1`)
				require.NoError(t, err)
				defer rows.Close()
				var plan []string
				for rows.Next() {
					var id, parent, notused int
					var detail string
					require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
					plan = append(plan, detail)
				}
				require.NoError(t, rows.Err())
				joined := strings.Join(plan, " | ")
				require.Contains(t, joined, "USING", "children are looked up by scanning nodes: %s", joined)
				require.NotContains(t, joined, "SCAN nodes", "children are looked up by scanning nodes: %s", joined)
			case "postgres":
				var n int
				require.NoError(t, sqlDB.QueryRowContext(ctx, `
					SELECT COUNT(*) FROM pg_index i
					  JOIN pg_class t ON t.oid = i.indrelid
					  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = i.indkey[0]
					 WHERE t.relname = 'nodes' AND a.attname = 'parent_id'`).Scan(&n))
				require.Positive(t, n, "no index on nodes is led by parent_id")
			case "mysql":
				var n int
				require.NoError(t, sqlDB.QueryRowContext(ctx, `
					SELECT COUNT(*) FROM information_schema.STATISTICS
					 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'nodes'
					   AND COLUMN_NAME = 'parent_id' AND SEQ_IN_INDEX = 1`).Scan(&n))
				require.Positive(t, n, "no index on nodes is led by parent_id")
			}
		})
	}
}
