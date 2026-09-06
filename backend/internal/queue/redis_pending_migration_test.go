package queue_test

// The Redis driver's pending set changed shape: a LIST consumed with BLMOVE
// became a ZSET ordered by `priority DESC, arrival ASC`.
//
// ⚠ An install upgrading across that change has a LIST sitting at the key, and
// ZADD against a LIST is a WRONGTYPE error — every op already queued would be
// stranded and every new one refused. So Init converts it, and this is the
// test that the conversion keeps the ops AND serves them in the order the old
// build was about to: rightmost first, because the old list was pushed on the
// left and claimed from the right.
//
// It also proves the conversion is an improvement rather than a transcription:
// the sweep ops the old driver was about to hand out ahead of a person's
// upload sort behind it once they carry their priority.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/queue"
)

func TestRedisConvertsALegacyPendingList(t *testing.T) {
	url := os.Getenv("FILEX_TEST_REDIS_URL")
	if url == "" {
		t.Skip("set FILEX_TEST_REDIS_URL to run the Redis driver tests")
	}
	ctx := context.Background()
	opts, err := goredis.ParseURL(url)
	require.NoError(t, err)
	rc := goredis.NewClient(opts)
	t.Cleanup(func() { _ = rc.Close() })

	prefix := fmt.Sprintf("filextest:%d:%s", time.Now().UnixNano(), t.Name())

	// Hand-build the state the old driver left behind: a pending LIST, and a
	// data hash per id with no `seq` field, because that build had none.
	//
	// The old driver LPUSHed and claimed from the right, so this arrival
	// order — three sweep ops, then a person's upload — is a list whose claim
	// order was sweep-a, sweep-b, sweep-c, interactive: the upload LAST,
	// behind the whole backlog. That is the bug, preserved in the state.
	type legacy struct {
		id       string
		priority int
	}
	rows := []legacy{
		{"sweep-a", -1},
		{"sweep-b", -1},
		{"sweep-c", -1},
		{"interactive", 0},
	}
	for _, r := range rows {
		body, _ := json.Marshal(map[string]any{"id": r.id})
		require.NoError(t, rc.HSet(ctx, prefix+":data:"+r.id, map[string]any{
			"id": r.id, "type": "scan", "payload": string(body),
			"status": "pending", "priority": fmt.Sprint(r.priority),
			"attempts": "0", "max_attempts": "3",
			"enqueued_at": time.Now().UTC().Format(time.RFC3339Nano),
		}).Err())
		require.NoError(t, rc.LPush(ctx, prefix+":pending", r.id).Err())
	}
	// An id whose op is gone — the orphan the old driver dropped on the floor.
	require.NoError(t, rc.LPush(ctx, prefix+":pending", "orphan").Err())
	t.Cleanup(func() {
		keys, _ := rc.Keys(ctx, prefix+"*").Result()
		if len(keys) > 0 {
			_ = rc.Del(ctx, keys...).Err()
		}
	})

	drv, err := queue.Get("redis")
	require.NoError(t, err)
	require.NoError(t, drv.Init(ctx, map[string]any{"url": url, "key_prefix": prefix}))
	t.Cleanup(func() { _ = drv.Close() })

	typ, err := rc.Type(ctx, prefix+":pending").Result()
	require.NoError(t, err)
	assert.Equal(t, "zset", typ, "the pending list must have been converted")

	_, total, err := drv.List(ctx, queue.StatusPending, 100, 0)
	require.NoError(t, err)
	assert.EqualValues(t, len(rows), total, "the orphan is dropped, every real op is kept")

	// The person's op first — it was LAST in claim order on the old driver.
	// Then the sweep, oldest-first, in the order the list would have served
	// them among themselves.
	var got []string
	for i := 0; i < len(rows); i++ {
		op, err := drv.Dequeue(ctx, []string{"scan"})
		require.NoError(t, err)
		got = append(got, op.ID)
		require.NoError(t, drv.Ack(ctx, op.ID))
	}
	assert.Equal(t, []string{"interactive", "sweep-a", "sweep-b", "sweep-c"}, got)
}
