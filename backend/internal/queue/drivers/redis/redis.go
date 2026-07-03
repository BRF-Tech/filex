// Package redis is the Redis-backed queue driver. Compared to the SQL
// drivers, this one trades a relational source-of-truth for the Redis
// work-list pattern: each op id flows between server-side LISTs
// (`pending` → `running` → `done` | `failed` | `cancelled`) while the
// canonical fields live in a parallel HASH at `<prefix>:data:<id>`.
//
// The headline win is BLMOVE on Dequeue: a worker blocks for up to
// 5s waiting for an id to appear, with no application-level polling.
// (BLMOVE source dest RIGHT LEFT — the modern replacement for the
// deprecated BRPOPLPUSH; same right-pop/left-push semantic.)
// The headline cost is that LIST membership is positional — Redis can't
// filter by op.Type the way SQL can — so the type-allowlist semantics
// are emulated by yielding non-matching ids back to the tail of the
// pending list. See Dequeue for the gritty detail.
//
// Delayed dispatch (Op.NotBefore) lives in a ZSET keyed by the unix
// epoch deadline. A promoter goroutine, started during Init, sweeps the
// ZSET every second and atomically moves due ids to the pending list.
package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/brf-tech/filex/backend/internal/queue"
)

func init() {
	queue.Register("redis", func() queue.Driver { return &Driver{} })
}

// Default key namespace. Override per-instance via cfg["key_prefix"]
// when a single Redis box hosts more than one filex queue (dev shares,
// staging vs prod multitenancy, …).
const defaultKeyPrefix = "filex:queue"

// blockTimeout is the BLMOVE block window. Five seconds keeps the
// worker responsive to ctx.Done() without hammering Redis with empty
// reads — Pool's outer loop will re-issue immediately if it still has
// work to do.
const blockTimeout = 5 * time.Second

// promoterInterval is how often the scheduled-set sweeper runs. One
// second matches the rest of the project's polling cadence and keeps
// NotBefore latency tight without overloading Redis.
const promoterInterval = time.Second

// promoterBatchSize caps the number of ids promoted per sweep so a
// large backlog doesn't monopolise the connection.
const promoterBatchSize = 100

// doneCap retains the most-recent N completed ops in <prefix>:done so
// the admin UI can show recent successes without unbounded memory use.
// Older entries fall off via LTRIM after each Ack.
const doneCap = 1000

// yieldRequeueGuard caps how many type-mismatch yields a single Dequeue
// call performs before giving up and returning ErrEmpty. Without this
// guard a queue full of unhandled types would spin forever (each yield
// is essentially zero-cost, but cumulative tail latency would build up
// against the worker's poll cadence).
const yieldRequeueGuard = 32

// Driver is the Redis-backed queue.
type Driver struct {
	client    *goredis.Client
	keyPrefix string

	// ownedClient is true when Init opened the connection itself (cfg
	// "url" path). When the bootstrap supplies cfg["client"] we don't
	// own its lifecycle — Close must be a no-op in that case.
	ownedClient bool

	// promoter goroutine plumbing.
	promoterStop   chan struct{}
	promoterDone   chan struct{}
	promoterCancel context.CancelFunc
	promoterOnce   sync.Once

	logger *slog.Logger
}

// Name implements queue.Driver. Pointer receiver because Driver embeds
// sync.Once via the promoter plumbing; copying it would copy the lock.
func (*Driver) Name() string { return "redis" }

// Init wires the driver to its backing store. cfg keys:
//
//	url        — Redis connection URL (redis://[:pass@]host:port/db).
//	             Required when cfg["client"] is absent.
//	key_prefix — Optional namespace, default "filex:queue". The driver
//	             appends `:pending`, `:running`, … to derive concrete
//	             key names.
//	client     — *redis.Client (preferred). When set, url is ignored
//	             and Close() will not Close() the borrowed handle —
//	             same lifecycle contract as the SQL drivers.
//	logger     — *slog.Logger override; defaults to slog.Default().
func (d *Driver) Init(ctx context.Context, cfg map[string]any) error {
	if v, ok := cfg["logger"].(*slog.Logger); ok && v != nil {
		d.logger = v
	} else {
		d.logger = slog.Default()
	}
	if v, ok := cfg["key_prefix"].(string); ok && v != "" {
		d.keyPrefix = strings.TrimRight(v, ":")
	} else {
		d.keyPrefix = defaultKeyPrefix
	}

	if v, ok := cfg["client"].(*goredis.Client); ok && v != nil {
		d.client = v
		d.ownedClient = false
	} else {
		urlStr, _ := cfg["url"].(string)
		if urlStr == "" {
			return errors.New("queue/redis: url required (or supply *redis.Client via cfg[\"client\"])")
		}
		opts, err := goredis.ParseURL(urlStr)
		if err != nil {
			return fmt.Errorf("queue/redis: parse url: %w", err)
		}
		d.client = goredis.NewClient(opts)
		d.ownedClient = true
	}

	// Smoke-ping so a misconfigured URL fails Init rather than the
	// first Enqueue. Use the caller's ctx so a deadline propagates.
	if err := d.client.Ping(ctx).Err(); err != nil {
		if d.ownedClient {
			_ = d.client.Close()
		}
		return fmt.Errorf("queue/redis: ping: %w", err)
	}

	// Spin up the scheduled-set promoter. Its lifetime is tied to
	// Close() rather than the Init ctx so the queue keeps draining
	// the schedule even after a request-scoped Init context expires.
	pctx, pcancel := context.WithCancel(context.Background())
	d.promoterStop = make(chan struct{})
	d.promoterDone = make(chan struct{})
	d.promoterCancel = pcancel
	go d.runPromoter(pctx)
	return nil
}

// Close releases resources. The connection is only closed when the
// driver opened it (cfg["url"] path) — borrowed clients keep running.
// The promoter goroutine is always cancelled.
func (d *Driver) Close() error {
	d.promoterOnce.Do(func() {
		if d.promoterCancel != nil {
			d.promoterCancel()
		}
		if d.promoterStop != nil {
			close(d.promoterStop)
		}
		// Wait for the promoter to finish its current sweep so we
		// don't race with Redis on shutdown.
		if d.promoterDone != nil {
			<-d.promoterDone
		}
	})
	if d.ownedClient && d.client != nil {
		if err := d.client.Close(); err != nil {
			return fmt.Errorf("queue/redis: close: %w", err)
		}
	}
	return nil
}

// Enqueue persists op and returns its assigned id. NotBefore set in the
// future routes the id into the scheduled ZSET; otherwise it lands on
// the pending LIST immediately.
func (d *Driver) Enqueue(ctx context.Context, op queue.Op) (string, error) {
	if op.Type == "" {
		return "", errors.New("queue/redis: op.Type required")
	}
	if op.MaxAttempts == 0 {
		op.MaxAttempts = queue.DefaultMaxAttempts
	}
	if op.Status == "" {
		op.Status = queue.StatusPending
	}
	id := op.ID
	if id == "" {
		var err error
		id, err = newID()
		if err != nil {
			return "", err
		}
	}
	payload := op.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("queue/redis: marshal payload: %w", err)
	}
	now := time.Now().UTC()
	if op.EnqueuedAt.IsZero() {
		op.EnqueuedAt = now
	}

	hashKey := d.dataKey(id)
	fields := map[string]any{
		"id":           id,
		"type":         op.Type,
		"payload":      string(body),
		"status":       op.Status,
		"priority":     strconv.Itoa(op.Priority),
		"attempts":     strconv.Itoa(op.Attempts),
		"max_attempts": strconv.Itoa(op.MaxAttempts),
		"last_error":   op.LastError,
		"enqueued_at":  formatTime(op.EnqueuedAt),
		"started_at":   formatTimePtr(op.StartedAt),
		"finished_at":  formatTimePtr(op.FinishedAt),
		"not_before":   formatTimePtr(op.NotBefore),
	}

	// Pipeline the hash write + list/zset placement so a partial
	// failure doesn't leave a half-written op visible.
	scheduled := op.NotBefore != nil && op.NotBefore.After(now)
	pipe := d.client.TxPipeline()
	pipe.HSet(ctx, hashKey, fields)
	if scheduled {
		pipe.ZAdd(ctx, d.scheduledKey(), goredis.Z{
			Score:  float64(op.NotBefore.Unix()),
			Member: id,
		})
	} else {
		pipe.LPush(ctx, d.pendingKey(), id)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return "", fmt.Errorf("queue/redis: enqueue: %w", err)
	}
	return id, nil
}

// Dequeue blocks for up to 5s waiting for an id, then verifies the
// op's type is in the requested allowlist. Type filtering in Redis
// LISTs is awkward because LISTs are positional — there's no SQL-style
// `type IN (?, ?)` predicate. We compromise: BLMOVE the head, peek
// the data hash, and yield mismatches back to the tail of pending
// (RPUSH preserves the rest of the queue's ordering). After
// yieldRequeueGuard misses we return ErrEmpty so the worker loop sleeps
// — protects against pathological "queue full of unhandled types" spin.
//
// Context handling: BLMOVE respects the underlying connection
// timeout. If ctx is cancelled mid-block, go-redis surfaces
// context.Canceled / context.DeadlineExceeded; we map them to ErrEmpty
// so the worker loop exits cleanly via its own ctx check.
func (d *Driver) Dequeue(ctx context.Context, types []string) (queue.Op, error) {
	allowed := typeSet(types)

	for i := 0; i < yieldRequeueGuard; i++ {
		select {
		case <-ctx.Done():
			return queue.Op{}, queue.ErrEmpty
		default:
		}

		// BLMOVE (right→left) returns the popped value. Empty queue →
		// redis.Nil after blockTimeout. ctx cancellation → ctx error.
		id, err := d.client.BLMove(ctx, d.pendingKey(), d.runningKey(), "RIGHT", "LEFT", blockTimeout).Result()
		if err != nil {
			switch {
			case errors.Is(err, goredis.Nil):
				return queue.Op{}, queue.ErrEmpty
			case errors.Is(err, context.Canceled),
				errors.Is(err, context.DeadlineExceeded):
				return queue.Op{}, queue.ErrEmpty
			default:
				return queue.Op{}, fmt.Errorf("queue/redis: blmove: %w", err)
			}
		}

		// Read the canonical record so we can both type-check and
		// return a fully-populated Op. If the hash is missing the id
		// is orphaned (rare — implies an out-of-band manual cleanup);
		// drop it on the floor and continue.
		op, err := d.fetchOp(ctx, id)
		if err != nil {
			if errors.Is(err, queue.ErrNotFound) {
				_ = d.client.LRem(ctx, d.runningKey(), 1, id).Err()
				continue
			}
			return queue.Op{}, err
		}

		if len(allowed) > 0 {
			if _, ok := allowed[op.Type]; !ok {
				// Yield back to the tail of pending so other workers
				// (with possibly different type allowlists) can pick
				// it up. LREM then RPUSH inside a tx so we don't
				// briefly double-list the id — important for List()
				// counters and Stats accuracy.
				pipe := d.client.TxPipeline()
				pipe.LRem(ctx, d.runningKey(), 1, id)
				pipe.RPush(ctx, d.pendingKey(), id)
				if _, err := pipe.Exec(ctx); err != nil {
					return queue.Op{}, fmt.Errorf("queue/redis: yield: %w", err)
				}
				continue
			}
		}

		// Claim it: bump attempts, flip status, stamp started_at.
		// All three writes share a tx so partial application is
		// impossible — the field-set as observed by Get() is
		// consistent.
		now := time.Now().UTC()
		pipe := d.client.TxPipeline()
		pipe.HSet(ctx, d.dataKey(id),
			"status", queue.StatusRunning,
			"started_at", formatTime(now),
			"finished_at", "",
			"last_error", "",
		)
		pipe.HIncrBy(ctx, d.dataKey(id), "attempts", 1)
		if _, err := pipe.Exec(ctx); err != nil {
			return queue.Op{}, fmt.Errorf("queue/redis: claim: %w", err)
		}
		op.Status = queue.StatusRunning
		op.StartedAt = &now
		op.FinishedAt = nil
		op.LastError = ""
		op.Attempts++
		return op, nil
	}

	// Hit the yield guard — every id we peeked was a type mismatch.
	// Tell the caller to back off so we don't busy-loop.
	return queue.Op{}, queue.ErrEmpty
}

// Ack marks the op completed. The id moves from running → done; older
// done entries fall off via LTRIM so the list stays bounded.
func (d *Driver) Ack(ctx context.Context, id string) error {
	if !d.opExists(ctx, id) {
		return queue.ErrNotFound
	}
	now := time.Now().UTC()
	pipe := d.client.TxPipeline()
	pipe.LRem(ctx, d.runningKey(), 1, id)
	pipe.HSet(ctx, d.dataKey(id),
		"status", queue.StatusDone,
		"finished_at", formatTime(now),
		"last_error", "",
	)
	pipe.LPush(ctx, d.doneKey(), id)
	pipe.LTrim(ctx, d.doneKey(), 0, doneCap-1)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("queue/redis: ack: %w", err)
	}
	return nil
}

// Fail records the failure. retry=true sends the op back through the
// scheduled ZSET with a 30s hold-off so a flapping upstream doesn't
// burn through the attempt budget instantly. retry=false parks the op
// in the failed list for operator inspection.
func (d *Driver) Fail(ctx context.Context, id, errMsg string, retry bool) error {
	if !d.opExists(ctx, id) {
		return queue.ErrNotFound
	}
	now := time.Now().UTC()
	if retry {
		// Hold-off pushes the next attempt 30s into the future via
		// the scheduled ZSET. The promoter sweeper moves it back to
		// pending when due.
		notBefore := now.Add(30 * time.Second)
		pipe := d.client.TxPipeline()
		pipe.LRem(ctx, d.runningKey(), 1, id)
		pipe.HSet(ctx, d.dataKey(id),
			"status", queue.StatusPending,
			"last_error", errMsg,
			"started_at", "",
			"finished_at", "",
			"not_before", formatTime(notBefore),
		)
		pipe.ZAdd(ctx, d.scheduledKey(), goredis.Z{
			Score:  float64(notBefore.Unix()),
			Member: id,
		})
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("queue/redis: requeue: %w", err)
		}
		return nil
	}
	pipe := d.client.TxPipeline()
	pipe.LRem(ctx, d.runningKey(), 1, id)
	pipe.HSet(ctx, d.dataKey(id),
		"status", queue.StatusFailed,
		"last_error", errMsg,
		"started_at", "",
		"finished_at", formatTime(now),
	)
	pipe.LPush(ctx, d.failedKey(), id)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("queue/redis: fail: %w", err)
	}
	return nil
}

// List returns ops filtered by status (or empty for any) with
// pagination. When status is empty we union all five status lists for
// totals — call Stats() instead if you only need counters.
func (d *Driver) List(ctx context.Context, status string, limit, offset int) ([]queue.Op, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	if status == "" {
		// Without a status filter we walk every per-status list and
		// merge in a deterministic order: pending → running → failed
		// → cancelled → done. The aggregate is paginated after the
		// fact, mirroring the SQL drivers' "all rows, ordered by
		// enqueued_at desc" semantics roughly enough for the admin
		// UI's purposes.
		listKeys := []string{
			d.pendingKey(), d.runningKey(),
			d.failedKey(), d.cancelledKey(), d.doneKey(),
		}
		var allIDs []string
		var total int64
		for _, key := range listKeys {
			ids, err := d.client.LRange(ctx, key, 0, -1).Result()
			if err != nil {
				return nil, 0, fmt.Errorf("queue/redis: list lrange: %w", err)
			}
			allIDs = append(allIDs, ids...)
			total += int64(len(ids))
		}
		// Apply offset/limit on the merged slice.
		end := offset + limit
		if offset >= len(allIDs) {
			return nil, total, nil
		}
		if end > len(allIDs) {
			end = len(allIDs)
		}
		ids := allIDs[offset:end]
		ops, err := d.fetchOps(ctx, ids)
		return ops, total, err
	}

	listKey, err := d.listKeyForStatus(status)
	if err != nil {
		return nil, 0, err
	}
	total, err := d.client.LLen(ctx, listKey).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("queue/redis: llen: %w", err)
	}
	if total == 0 {
		return nil, 0, nil
	}
	stop := int64(offset + limit - 1)
	ids, err := d.client.LRange(ctx, listKey, int64(offset), stop).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("queue/redis: lrange: %w", err)
	}
	ops, err := d.fetchOps(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	return ops, total, nil
}

// Get fetches a single op.
func (d *Driver) Get(ctx context.Context, id string) (queue.Op, error) {
	return d.fetchOp(ctx, id)
}

// Stats returns the dashboard counters. Done24h is approximate — the
// done list is capped at doneCap and we have no per-entry timestamp
// in the list itself, so we fall back to "size of done list, capped
// at doneCap" which matches what the SQL drivers report when their
// retention windows align.
func (d *Driver) Stats(ctx context.Context) (queue.Stats, error) {
	pipe := d.client.Pipeline()
	pendingCmd := pipe.LLen(ctx, d.pendingKey())
	runningCmd := pipe.LLen(ctx, d.runningKey())
	failedCmd := pipe.LLen(ctx, d.failedKey())
	cancelledCmd := pipe.LLen(ctx, d.cancelledKey())
	if _, err := pipe.Exec(ctx); err != nil {
		return queue.Stats{}, fmt.Errorf("queue/redis: stats: %w", err)
	}
	// done24h: count entries in done whose finished_at falls within
	// the last 24h. The done list is capped, so worst case we walk
	// doneCap ids. For higher accuracy a future revision can move
	// done into a ZSET keyed by finished_at; v0.1 prefers the simpler
	// LIST shape and accepts the over-count when the list is full.
	cutoff := time.Now().Add(-24 * time.Hour)
	ids, err := d.client.LRange(ctx, d.doneKey(), 0, -1).Result()
	if err != nil {
		return queue.Stats{}, fmt.Errorf("queue/redis: done24h scan: %w", err)
	}
	var done24h int64
	for _, id := range ids {
		ts, err := d.client.HGet(ctx, d.dataKey(id), "finished_at").Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				continue
			}
			return queue.Stats{}, fmt.Errorf("queue/redis: done24h hget: %w", err)
		}
		t, ok := parseTime(ts)
		if !ok {
			continue
		}
		if t.After(cutoff) {
			done24h++
		}
	}
	return queue.Stats{
		Pending:   pendingCmd.Val(),
		Running:   runningCmd.Val(),
		Failed:    failedCmd.Val(),
		Cancelled: cancelledCmd.Val(),
		Done24h:   done24h,
	}, nil
}

// Cancel transitions a pending op to cancelled. Running ops cannot be
// cancelled in v0.1 — same contract as the SQL drivers.
func (d *Driver) Cancel(ctx context.Context, id string) error {
	op, err := d.fetchOp(ctx, id)
	if err != nil {
		return err
	}
	if op.Status != queue.StatusPending {
		return fmt.Errorf("queue/redis: cancel: op already in status %q", op.Status)
	}
	now := time.Now().UTC()
	pipe := d.client.TxPipeline()
	// Try removing from both pending and scheduled; the op is in
	// exactly one but we don't pre-check which to keep the round-trip
	// count constant. LREM/ZRem on a missing entry is a no-op.
	pipe.LRem(ctx, d.pendingKey(), 1, id)
	pipe.ZRem(ctx, d.scheduledKey(), id)
	pipe.HSet(ctx, d.dataKey(id),
		"status", queue.StatusCancelled,
		"finished_at", formatTime(now),
		"not_before", "",
	)
	pipe.LPush(ctx, d.cancelledKey(), id)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("queue/redis: cancel: %w", err)
	}
	return nil
}

// Retry transitions a failed op back to pending. The attempts counter
// is preserved so the operator can see how many tries it took before
// the manual intervention.
func (d *Driver) Retry(ctx context.Context, id string) error {
	op, err := d.fetchOp(ctx, id)
	if err != nil {
		return err
	}
	if op.Status != queue.StatusFailed {
		return queue.ErrNotFound
	}
	pipe := d.client.TxPipeline()
	pipe.LRem(ctx, d.failedKey(), 1, id)
	pipe.HSet(ctx, d.dataKey(id),
		"status", queue.StatusPending,
		"last_error", "",
		"started_at", "",
		"finished_at", "",
		"not_before", "",
	)
	pipe.LPush(ctx, d.pendingKey(), id)
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("queue/redis: retry: %w", err)
	}
	return nil
}

// RecoverOrphans flips long-running ids back to pending. Called on boot
// to handle ungraceful shutdowns. The check pairs LRANGE running with
// HGET status + started_at — anything still claiming `running` whose
// started_at is older than `olderThan` ago is considered orphaned.
func (d *Driver) RecoverOrphans(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	ids, err := d.client.LRange(ctx, d.runningKey(), 0, -1).Result()
	if err != nil {
		return 0, fmt.Errorf("queue/redis: recover lrange: %w", err)
	}
	var recovered int64
	for _, id := range ids {
		fields, err := d.client.HMGet(ctx, d.dataKey(id), "status", "started_at").Result()
		if err != nil {
			return recovered, fmt.Errorf("queue/redis: recover hmget: %w", err)
		}
		status, _ := fields[0].(string)
		startedRaw, _ := fields[1].(string)
		if status != queue.StatusRunning {
			// Stale list entry; clean it out so subsequent runs don't
			// see this id again.
			_ = d.client.LRem(ctx, d.runningKey(), 1, id).Err()
			continue
		}
		if startedRaw != "" {
			started, ok := parseTime(startedRaw)
			if ok && !started.Before(cutoff) {
				// Still within the heartbeat window — leave it.
				continue
			}
		}
		// Move id back to the tail of pending and reset its
		// running-state fields. Note we deliberately don't decrement
		// attempts: the previous attempt did happen; the operator
		// view should reflect that.
		pipe := d.client.TxPipeline()
		pipe.LRem(ctx, d.runningKey(), 1, id)
		pipe.HSet(ctx, d.dataKey(id),
			"status", queue.StatusPending,
			"started_at", "",
		)
		pipe.LPush(ctx, d.pendingKey(), id)
		if _, err := pipe.Exec(ctx); err != nil {
			return recovered, fmt.Errorf("queue/redis: recover requeue: %w", err)
		}
		recovered++
	}
	return recovered, nil
}

// ─── promoter ───────────────────────────────────────────────────────

// runPromoter sweeps the scheduled ZSET on a 1s tick and moves due
// entries to the pending list. It exits when ctx is cancelled or
// promoterStop is closed (whichever happens first).
func (d *Driver) runPromoter(ctx context.Context) {
	defer close(d.promoterDone)
	ticker := time.NewTicker(promoterInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.promoterStop:
			return
		case <-ticker.C:
			if err := d.promoteOnce(ctx); err != nil {
				d.logger.Warn("queue/redis: promoter sweep failed",
					slog.String("err", err.Error()))
			}
		}
	}
}

// promoteOnce moves up to promoterBatchSize due ids from the scheduled
// ZSET to the pending LIST. Each move is a small tx (ZRem + LPush)
// so a partial failure leaves the queue consistent — at worst the id
// is processed in the next sweep.
func (d *Driver) promoteOnce(ctx context.Context) error {
	now := time.Now().Unix()
	ids, err := d.client.ZRangeArgs(ctx, goredis.ZRangeArgs{
		Key:     d.scheduledKey(),
		ByScore: true,
		Start:   "-inf",
		Stop:    strconv.FormatInt(now, 10),
		Offset:  0,
		Count:   promoterBatchSize,
	}).Result()
	if err != nil {
		return fmt.Errorf("zrange byscore: %w", err)
	}
	for _, id := range ids {
		pipe := d.client.TxPipeline()
		pipe.ZRem(ctx, d.scheduledKey(), id)
		pipe.HSet(ctx, d.dataKey(id), "not_before", "")
		pipe.LPush(ctx, d.pendingKey(), id)
		if _, err := pipe.Exec(ctx); err != nil {
			return fmt.Errorf("promote %s: %w", id, err)
		}
	}
	return nil
}

// ─── helpers ───────────────────────────────────────────────────────

func (d *Driver) pendingKey() string   { return d.keyPrefix + ":pending" }
func (d *Driver) runningKey() string   { return d.keyPrefix + ":running" }
func (d *Driver) doneKey() string      { return d.keyPrefix + ":done" }
func (d *Driver) failedKey() string    { return d.keyPrefix + ":failed" }
func (d *Driver) cancelledKey() string { return d.keyPrefix + ":cancelled" }
func (d *Driver) scheduledKey() string { return d.keyPrefix + ":scheduled" }
func (d *Driver) dataKey(id string) string {
	return d.keyPrefix + ":data:" + id
}

// listKeyForStatus maps an Op.Status to the LIST that holds ids in
// that state. Returns an error for unknown values rather than silently
// returning an empty result, which would mask typos in caller code.
func (d *Driver) listKeyForStatus(status string) (string, error) {
	switch status {
	case queue.StatusPending:
		return d.pendingKey(), nil
	case queue.StatusRunning:
		return d.runningKey(), nil
	case queue.StatusDone:
		return d.doneKey(), nil
	case queue.StatusFailed:
		return d.failedKey(), nil
	case queue.StatusCancelled:
		return d.cancelledKey(), nil
	default:
		return "", fmt.Errorf("queue/redis: unknown status %q", status)
	}
}

// opExists is a cheap existence probe used by Ack/Fail to surface
// ErrNotFound before mutating the lists. EXISTS-on-hash is the
// idiomatic way to test "does this id have a backing record".
func (d *Driver) opExists(ctx context.Context, id string) bool {
	n, err := d.client.Exists(ctx, d.dataKey(id)).Result()
	return err == nil && n > 0
}

// fetchOp loads a single op from its data hash. Returns ErrNotFound
// when the hash is empty.
func (d *Driver) fetchOp(ctx context.Context, id string) (queue.Op, error) {
	fields, err := d.client.HGetAll(ctx, d.dataKey(id)).Result()
	if err != nil {
		return queue.Op{}, fmt.Errorf("queue/redis: hgetall: %w", err)
	}
	if len(fields) == 0 {
		return queue.Op{}, queue.ErrNotFound
	}
	return decodeOp(id, fields), nil
}

// fetchOps batches HGETALL calls through a single pipeline. The
// returned slice preserves input order; ids whose hashes are missing
// are silently skipped (List() callers only see consistent rows).
func (d *Driver) fetchOps(ctx context.Context, ids []string) ([]queue.Op, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	pipe := d.client.Pipeline()
	cmds := make([]*goredis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGetAll(ctx, d.dataKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("queue/redis: fetchOps pipeline: %w", err)
	}
	out := make([]queue.Op, 0, len(ids))
	for i, cmd := range cmds {
		fields, err := cmd.Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) {
				continue
			}
			return nil, fmt.Errorf("queue/redis: fetchOps result: %w", err)
		}
		if len(fields) == 0 {
			continue
		}
		out = append(out, decodeOp(ids[i], fields))
	}
	return out, nil
}

// decodeOp reconstructs an Op from a hash payload. Missing fields
// degrade gracefully to zero values — a mid-write op observed by a
// concurrent reader still parses, the consumer just sees an
// in-progress snapshot.
func decodeOp(id string, fields map[string]string) queue.Op {
	op := queue.Op{
		ID:        id,
		Type:      fields["type"],
		Status:    fields["status"],
		LastError: fields["last_error"],
	}
	if op.ID == "" {
		op.ID = fields["id"]
	}
	if v := fields["payload"]; v != "" {
		op.Payload = map[string]any{}
		if err := json.Unmarshal([]byte(v), &op.Payload); err != nil {
			// Preserve the raw string under a sentinel key so the
			// admin UI can still show what it was. This shouldn't
			// happen in practice — Enqueue is the only writer of
			// the field — but it keeps us forward-compatible if a
			// future caller writes raw text.
			op.Payload = map[string]any{"_raw": v}
		}
	} else {
		op.Payload = map[string]any{}
	}
	if v, err := strconv.Atoi(fields["priority"]); err == nil {
		op.Priority = v
	}
	if v, err := strconv.Atoi(fields["attempts"]); err == nil {
		op.Attempts = v
	}
	if v, err := strconv.Atoi(fields["max_attempts"]); err == nil {
		op.MaxAttempts = v
	}
	if t, ok := parseTime(fields["enqueued_at"]); ok {
		op.EnqueuedAt = t
	}
	if t, ok := parseTime(fields["started_at"]); ok {
		op.StartedAt = &t
	}
	if t, ok := parseTime(fields["finished_at"]); ok {
		op.FinishedAt = &t
	}
	if t, ok := parseTime(fields["not_before"]); ok {
		op.NotBefore = &t
	}
	return op
}

// typeSet converts the slice argument of Dequeue into a lookup map.
func typeSet(types []string) map[string]struct{} {
	if len(types) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(types))
	for _, t := range types {
		out[t] = struct{}{}
	}
	return out
}

// formatTime renders a UTC RFC3339-nano string. We avoid the empty
// "0001-01-01…" zero value by formatting only non-zero times — the
// hash field stays "" otherwise so parseTime can reverse the trip.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// formatTimePtr is the *time.Time analogue of formatTime.
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTime(*t)
}

// parseTime is the inverse of formatTime. Returns (zero, false) when
// the input is empty or unparseable so callers can leave the target
// pointer nil.
func parseTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Backstop for older formats — RFC3339 without nanos.
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return time.Time{}, false
		}
	}
	return t.UTC(), true
}

// newID returns a 16-byte hex random string. Same shape as the SQL
// drivers so an op id round-trips between them without re-encoding —
// useful when an operator migrates between drivers via mu manual
// export/import.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
