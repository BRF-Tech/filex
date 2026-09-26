package db_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPendingOpsActorIsIndexedOnEveryEngine: a person's own operations are
// found through an index (migration 00062).
//
// Everybody but an administrator is shown the operations they queued, and the
// explorer asks for them when it opens and every two seconds while something
// runs: `WHERE actor_id = ? ORDER BY id DESC LIMIT 200`. pending_ops keeps
// every finished row and nothing indexed actor_id, so each of those polls read
// the whole table.
func TestPendingOpsActorIsIndexedOnEveryEngine(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.name, func(t *testing.T) {
			sqlDB, _ := openMigrated(t, e)
			ctx := context.Background()
			switch e.name {
			case "sqlite":
				rows, err := sqlDB.QueryContext(ctx,
					`EXPLAIN QUERY PLAN SELECT id FROM pending_ops WHERE actor_id = 1 ORDER BY id DESC LIMIT 200`)
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
				require.Contains(t, joined, "idx_pending_ops_actor", "a person's operations are found by scanning the queue: %s", joined)
			case "postgres":
				var n int
				require.NoError(t, sqlDB.QueryRowContext(ctx, `
					SELECT COUNT(*) FROM pg_index i
					  JOIN pg_class t ON t.oid = i.indrelid
					  JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = i.indkey[0]
					 WHERE t.relname = 'pending_ops' AND a.attname = 'actor_id'`).Scan(&n))
				require.Positive(t, n, "no index on pending_ops is led by actor_id")
			case "mysql":
				var n int
				require.NoError(t, sqlDB.QueryRowContext(ctx, `
					SELECT COUNT(*) FROM information_schema.STATISTICS
					 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'pending_ops'
					   AND COLUMN_NAME = 'actor_id' AND SEQ_IN_INDEX = 1`).Scan(&n))
				require.Positive(t, n, "no index on pending_ops is led by actor_id")
			}
		})
	}
}
