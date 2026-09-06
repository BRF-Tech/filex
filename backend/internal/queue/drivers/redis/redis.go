// Package redis is the Redis-backed queue driver. Compared to the SQL
// drivers, this one trades a relational source-of-truth for Redis'
// server-side collections: each op id flows from the `pending` SORTED SET
// into the `running` LIST and on to `done` | `failed` | `cancelled`,
// while the canonical fields live in a parallel HASH at
// `<prefix>:data:<id>`.
//
// ⚠ The pending set is a ZSET, not a LIST, and that is the whole point.
// A LIST is positional: consumed with BLMOVE it hands out ops in arrival
// order and there is nowhere for Op.Priority to be expressed, so the
// field was persisted, returned by Get, rendered by the admin UI — and
// ignored by the one operation it exists for. The SQL drivers claim with
// `ORDER BY priority DESC, enqueued_at ASC`; on Redis a scan the storage
// sync discovered still went out ahead of a person's upload, and the 41
// seconds an interactive scan waited behind a 20 000-file first import
// were still being paid. The ZSET score encodes exactly that ordering —
// see pendingScore.
//
// Claiming is one Lua script (dequeueScript). Redis runs it atomically,
// which buys two things a LIST could not:
//
//   - type filtering with no mutation. A LIST cannot be searched, so the
//     old code popped the head, looked at it, and pushed mismatches back
//     at the far end. A ZRANGE walks candidates in priority order and
//     leaves the ones it does not want exactly where they are.
//   - pop and claim in a single step. BLMOVE moved the id to `running`
//     and a separate transaction then flipped the hash to `running`; a
//     crash in between left an op no list owned and no recovery pass
//     could interpret. Now the ZREM, the LPUSH, the status write, the
//     attempt bump and the release of the coalescing claim either all
//     happen or none do.
//
// The cost is that the claim is no longer a blocking Redis command. A
// worker that finds nothing waits on `<prefix>:signal`, a capped LIST
// every push into pending writes one token to, so an arriving op still
// wakes a worker in about a millisecond rather than at the next poll.
// See Dequeue.
//
// Delayed dispatch (Op.NotBefore) lives in a second ZSET keyed by the
// unix epoch deadline. A promoter goroutine, started during Init, sweeps
// it every second and atomically moves due ids into pending.
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

// blockTimeout is how long a worker with nothing to claim waits on the
// doorbell before reporting the queue empty. Five seconds keeps it
// responsive to ctx.Done() without hammering Redis with empty reads —
// Pool's outer loop re-issues immediately if it still has work to do.
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

// dequeueScanPage / dequeueScanLimit bound the Lua claim script's walk
// over the head of the pending set.
//
// The script stops at the first op whose type this worker handles, so on
// a healthy queue it reads one page and usually one entry of it. The
// limit matters only when the head is a run of ops NO worker in the
// fleet has a handler for — a rolling upgrade where a newer node
// enqueues a type the older ones do not know. Rather than walk an
// unbounded set inside a call that blocks the whole Redis server, the
// script gives up after this many and reports the queue empty.
//
// ⚠ The SQL drivers have no such bound: `type IN (…)` skips as far as it
// must. A thousand is far past any real backlog of unhandled types and
// keeps the worst-case script well under a millisecond.
const (
	dequeueScanPage  = 100
	dequeueScanLimit = 1000
)

// signalCap bounds the wake-up doorbell. Each push into pending writes
// one token; a worker with nothing to claim blocks on BLPOP for it.
//
// Capping matters in both directions. Uncapped, a busy queue accumulates
// one stale token per op and every later Dequeue would be woken instantly
// to find nothing. Capped at one, a burst of ops would wake a single
// worker and the rest would sit out their block window while work waited.
// Sixty-four is comfortably more than any pool size and bounds the stale
// tokens a drain can leave behind to one cheap script call each.
const signalCap = 64

// dedupGrace is how long a coalescing claim outlives the moment its op
// becomes runnable. It only has to cover the gap between not_before passing
// and a worker picking the op up; 15 minutes is far more than a healthy queue
// needs and still bounded, so a claim can never be leaked forever by a crash
// between SET NX and the op write.
const dedupGrace = 15 * time.Minute

// ─── pending ordering ───────────────────────────────────────────────

const (
	// priorityBand is how much score one step of priority is worth. Every
	// arrival number fits below it, so a higher priority always sorts
	// ahead of a lower one no matter when either was enqueued.
	priorityBand = int64(1) << 41

	// priorityClamp bounds Op.Priority so the encoded score stays inside
	// the 2^53 range a float64 represents EXACTLY. Redis scores are IEEE
	// 754 doubles; past that two different orderings round to the same
	// score and the tie is broken by op id — arbitrarily. ±2048 is three
	// orders of magnitude past anything filex uses (the sync sweep is -1);
	// a value outside it still sorts at the right extreme, it just stops
	// being distinguishable from its neighbours.
	priorityClamp = 2048

	// maxSeq is the largest arrival number the band holds. Reaching it
	// takes 2.2e12 enqueues — seventy years at a thousand a second — and
	// past it ordering degrades to "everything ties", never to a wrong
	// claim.
	maxSeq = priorityBand - 1
)

// pendingScore encodes `ORDER BY priority DESC, enqueued_at ASC` — the SQL
// drivers' claim order — into the single float64 a ZSET sorts on, lowest
// first.
//
// ⚠ Arrival is a Redis counter (`<prefix>:seq`), not a timestamp. A
// timestamp would have to be rounded to fit beside the priority, and
// SQLite already records `enqueued_at` to the second, so a burst of ops
// would tie and Redis would break the tie on the op id — which is
// random. A counter is exact, needs no epoch, and cannot go backwards
// when a clock does.
//
// ⚠ The number is the op's OWN arrival, stamped once at Enqueue and kept
// in its hash, so an op that returns to pending (a retry, a recovered
// orphan, a promotion off the schedule) keeps the position its age earns
// it — exactly what `ORDER BY enqueued_at ASC` gives it on SQLite and
// Postgres. The LIST this replaced sent every one of those to the back.
func pendingScore(priority int, seq int64) float64 {
	if priority > priorityClamp {
		priority = priorityClamp
	}
	if priority < -priorityClamp {
		priority = -priorityClamp
	}
	if seq < 0 {
		seq = 0
	}
	if seq > maxSeq {
		seq = maxSeq
	}
	return float64(int64(-priority)*priorityBand + seq)
}

// enqueueScript writes one op. See Enqueue for why this is a script.
//
//	KEYS[1] pending zset      KEYS[2] scheduled zset
//	KEYS[3] this op's hash    KEYS[4] arrival counter
//	KEYS[5] signal list
//	ARGV[1] dedup key, '' when the op does not coalesce
//	ARGV[2] dedup TTL in milliseconds
//	ARGV[3] scheduled score, '' when the op is runnable now
//	ARGV[4] clamped priority  ARGV[5] priority band  ARGV[6] max arrival
//	ARGV[7] signal cap        ARGV[8] op id
//	ARGV[9…] hash field/value pairs
//
// Returns 1 when the op was written, 0 when a pending op already holds
// the coalescing key.
var enqueueScript = goredis.NewScript(`
if ARGV[1] ~= '' then
  local got = redis.call('SET', ARGV[1], ARGV[8], 'NX', 'PX', tonumber(ARGV[2]))
  if not got then return 0 end
end
local seq = redis.call('INCR', KEYS[4])
local maxseq = tonumber(ARGV[6])
if seq > maxseq then seq = maxseq end
local fields = {}
for i = 9, #ARGV do fields[#fields + 1] = ARGV[i] end
fields[#fields + 1] = 'seq'
fields[#fields + 1] = tostring(seq)
redis.call('HSET', KEYS[3], unpack(fields))
if ARGV[3] ~= '' then
  redis.call('ZADD', KEYS[2], tonumber(ARGV[3]), ARGV[8])
else
  local score = -tonumber(ARGV[4]) * tonumber(ARGV[5]) + seq
  redis.call('ZADD', KEYS[1], score, ARGV[8])
  redis.call('LPUSH', KEYS[5], '1')
  redis.call('LTRIM', KEYS[5], 0, tonumber(ARGV[7]) - 1)
end
return 1
`)

// dequeueScript claims one op. See Dequeue for why this is a script.
//
//	KEYS[1] pending zset      KEYS[2] running list
//	ARGV[1] data key prefix   ARGV[2] dedup key prefix
//	ARGV[3] started_at        ARGV[4] page size   ARGV[5] scan limit
//	ARGV[6…] allowed types (absent = any type)
//
// Returns the claimed id, or false when nothing at the head of the queue
// is eligible.
var dequeueScript = goredis.NewScript(`
local allowed = {}
local nallowed = 0
for i = 6, #ARGV do
  allowed[ARGV[i]] = true
  nallowed = nallowed + 1
end
local page = tonumber(ARGV[4])
local limit = tonumber(ARGV[5])
local seen = 0
local from = 0
while seen < limit do
  local ids = redis.call('ZRANGE', KEYS[1], from, from + page - 1)
  if #ids == 0 then break end
  local removed = 0
  for i = 1, #ids do
    local id = ids[i]
    local key = ARGV[1] .. id
    local t = redis.call('HGET', key, 'type')
    if not t then
      redis.call('ZREM', KEYS[1], id)
      removed = removed + 1
    elseif nallowed == 0 or allowed[t] then
      redis.call('ZREM', KEYS[1], id)
      redis.call('LPUSH', KEYS[2], id)
      redis.call('HSET', key,
        'status', 'running',
        'started_at', ARGV[3],
        'finished_at', '',
        'last_error', '')
      redis.call('HINCRBY', key, 'attempts', 1)
      local dk = redis.call('HGET', key, 'dedup_key')
      if dk and dk ~= '' then
        redis.call('DEL', ARGV[2] .. dk)
      end
      return id
    end
  end
  seen = seen + #ids
  if #ids < page then break end
  from = from + #ids - removed
end
return false
`)

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

	// An install upgrading from a build whose pending set was a LIST has
	// one sitting at this key. ZADD would fail WRONGTYPE against it and
	// every queued op would be stranded, so convert it before anything
	// else touches the key.
	if err := d.migrateLegacyPendingList(ctx); err != nil {
		if d.ownedClient {
			_ = d.client.Close()
		}
		return err
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

// migrateLegacyPendingList converts a pre-ZSET pending LIST in place.
//
// An install upgrading from the BLMOVE build has a LIST at this key, and
// ZADD against a LIST is a WRONGTYPE error — every queued op would be
// stranded and every new one refused. So the list is converted before
// anything else touches the key.
//
// Order is preserved as the old build would have served it: the list was
// pushed on the LEFT and claimed from the RIGHT, so its rightmost entry
// was next. Reading it back-to-front and allocating arrival numbers in
// that order puts the queue back in the order it was in — and, because
// the score also carries the priority those ops were enqueued with, in a
// BETTER one: the sweep ops the old driver was about to serve ahead of a
// person's upload now sort behind it.
//
// An id whose hash is gone is dropped, the same thing the claim path does
// with an orphan.
//
// ⚠ Not reversible: a downgrade to a build that expects a LIST would find
// a ZSET and fail the same WRONGTYPE way. The alternative — keeping both
// shapes live — would mean two sources of truth for what is pending and a
// claim path that has to merge them.
func (d *Driver) migrateLegacyPendingList(ctx context.Context) error {
	typ, err := d.client.Type(ctx, d.pendingKey()).Result()
	if err != nil {
		return fmt.Errorf("queue/redis: pending type: %w", err)
	}
	if typ != "list" {
		return nil
	}
	ids, err := d.client.LRange(ctx, d.pendingKey(), 0, -1).Result()
	if err != nil {
		return fmt.Errorf("queue/redis: pending migrate lrange: %w", err)
	}

	type legacy struct {
		id       string
		priority int
	}
	ordered := make([]legacy, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- { // claim order: right to left
		id := ids[i]
		fields, err := d.client.HMGet(ctx, d.dataKey(id), "priority", "type").Result()
		if err != nil {
			return fmt.Errorf("queue/redis: pending migrate hmget: %w", err)
		}
		if fields[1] == nil {
			continue // orphaned id, no op behind it
		}
		priorityRaw, _ := fields[0].(string)
		priority, _ := strconv.Atoi(priorityRaw)
		ordered = append(ordered, legacy{id: id, priority: priority})
	}

	pipe := d.client.TxPipeline()
	pipe.Del(ctx, d.pendingKey())
	if len(ordered) > 0 {
		// One contiguous block of arrival numbers, below every number a
		// later Enqueue can draw, so the backlog stays ahead of new work.
		last, err := d.client.IncrBy(ctx, d.seqKey(), int64(len(ordered))).Result()
		if err != nil {
			return fmt.Errorf("queue/redis: pending migrate seq: %w", err)
		}
		first := last - int64(len(ordered)) + 1
		members := make([]goredis.Z, 0, len(ordered))
		for i, op := range ordered {
			members = append(members, goredis.Z{
				Score:  pendingScore(op.priority, first+int64(i)),
				Member: op.id,
			})
		}
		pipe.ZAdd(ctx, d.pendingKey(), members...)
		d.ringDoorbell(ctx, pipe)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("queue/redis: pending migrate: %w", err)
	}
	d.logger.Info("queue/redis: converted the pending list to a priority-ordered set",
		slog.Int("ops", len(ordered)))
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
// future routes the id into the scheduled ZSET; otherwise it lands in the
// pending set immediately, at the position its priority and arrival earn.
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

	// The whole enqueue is one script, so it is one round trip and one
	// indivisible step: the coalescing claim, the arrival number, the hash,
	// the placement and the doorbell either all happen or none do.
	//
	// ⚠ That atomicity is not a nicety. Before, a SET NX took the claim and
	// a second transaction wrote the op; if the write failed, the claim had
	// to be deleted by hand, and a process that died in between left a key
	// that suppressed every later request for it until the TTL ran out.
	// There is no in-between to clean up now.
	scheduled := op.NotBefore != nil && op.NotBefore.After(now)
	dedup := ""
	if op.DedupKey != "" {
		dedup = d.dedupKey(op.DedupKey)
	}
	scheduledScore := ""
	if scheduled {
		scheduledScore = strconv.FormatInt(op.NotBefore.Unix(), 10)
	}

	argv := []any{
		dedup,
		strconv.FormatInt(dedupTTL(op.NotBefore, now).Milliseconds(), 10),
		scheduledScore,
		strconv.Itoa(clampPriority(op.Priority)),
		strconv.FormatInt(priorityBand, 10),
		strconv.FormatInt(maxSeq, 10),
		strconv.Itoa(signalCap),
		id,
	}
	for _, kv := range [][2]string{
		{"id", id},
		{"type", op.Type},
		{"payload", string(body)},
		{"status", op.Status},
		{"priority", strconv.Itoa(op.Priority)},
		{"attempts", strconv.Itoa(op.Attempts)},
		{"max_attempts", strconv.Itoa(op.MaxAttempts)},
		{"last_error", op.LastError},
		{"enqueued_at", formatTime(op.EnqueuedAt)},
		{"started_at", formatTimePtr(op.StartedAt)},
		{"finished_at", formatTimePtr(op.FinishedAt)},
		{"not_before", formatTimePtr(op.NotBefore)},
		{"dedup_key", op.DedupKey},
	} {
		argv = append(argv, kv[0], kv[1])
	}

	res, err := enqueueScript.Run(ctx, d.client, []string{
		d.pendingKey(), d.scheduledKey(), d.dataKey(id),
		d.seqKey(), d.signalKey(),
	}, argv...).Result()
	if err != nil {
		return "", fmt.Errorf("queue/redis: enqueue: %w", err)
	}
	if n, _ := res.(int64); n == 0 {
		return "", queue.ErrDuplicate
	}
	return id, nil
}

// clampPriority folds Op.Priority into the range pendingScore can encode
// exactly. See priorityClamp.
func clampPriority(p int) int {
	if p > priorityClamp {
		return priorityClamp
	}
	if p < -priorityClamp {
		return -priorityClamp
	}
	return p
}

// dedupTTL bounds how long a coalescing claim survives. It covers the wait
// until the op becomes runnable plus a grace period for it to be picked up.
//
// ⚠ A claim that expires while its op is still pending (a badly backed-up
// queue) lets the next request enqueue a second op: a wasted scan, never a
// missed one. The SQL drivers have no equivalent lapse — their claim IS the
// pending row — so this is the one place the three drivers differ, and it
// differs in the safe direction.
func dedupTTL(notBefore *time.Time, now time.Time) time.Duration {
	ttl := dedupGrace
	if notBefore != nil {
		if d := notBefore.Sub(now); d > 0 {
			ttl = d + dedupGrace
		}
	}
	return ttl
}

// Dequeue claims the highest-priority runnable op whose type the caller
// handles, atomically, and blocks for up to blockTimeout when there is
// nothing to claim.
//
// One Lua script does the claim (see dequeueScript). It walks the head of
// the pending ZSET — which is priority order, then arrival order within a
// priority — skipping ops of types this worker has no handler for and
// leaving them exactly where they are, and on the first match performs
// the whole claim in one indivisible step: out of pending, into running,
// hash flipped to `running` with started_at stamped, attempts bumped, and
// the coalescing claim released.
//
// ⚠ The atomicity is not decoration. The BLMOVE this replaced moved the
// id to `running` and then a SECOND round trip flipped the hash; a crash
// in the gap left an id sitting in the running list whose hash still said
// `pending`, and RecoverOrphans reads exactly that as a stale list entry
// and drops it — the op was gone from every list, permanently pending in
// its own hash, and nothing would ever run it. There is no such window
// now, and none in the other direction either: the ZREM is inside the
// same script as the claim, so no two workers can be handed the same op.
//
// ⚠⚠ What this costs: the claim is no longer a blocking Redis command.
// A script cannot block, so "wait for work" moved to a doorbell — every
// push into pending writes a token to a capped LIST and a worker with
// nothing to do waits on BLPOP for one. The observable behaviour is the
// same (an arriving op wakes a worker in about a millisecond, not at the
// next poll), with two honest differences: a token may be consumed by a
// worker whose type filter does not match the op that produced it, and a
// drained burst can leave up to signalCap stale tokens behind. Both cost
// one extra script call and then the worker's own poll interval — never a
// missed op, because the script is re-run after the wait and the pool
// re-enters Dequeue immediately after every successful claim.
func (d *Driver) Dequeue(ctx context.Context, types []string) (queue.Op, error) {
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case <-ctx.Done():
			return queue.Op{}, queue.ErrEmpty
		default:
		}

		id, err := d.claim(ctx, types)
		if err != nil {
			return queue.Op{}, err
		}
		if id != "" {
			op, err := d.fetchOp(ctx, id)
			if err != nil {
				if errors.Is(err, queue.ErrNotFound) {
					// The hash vanished between the claim and the read —
					// an out-of-band cleanup. Drop the list entry and let
					// the caller come back round.
					_ = d.client.LRem(ctx, d.runningKey(), 1, id).Err()
					return queue.Op{}, queue.ErrEmpty
				}
				return queue.Op{}, err
			}
			return op, nil
		}

		if attempt == 0 {
			// Nothing to claim. Wait on the doorbell rather than returning
			// straight away, so an op that arrives a moment from now is
			// picked up at once instead of at the pool's next poll.
			if err := d.waitForSignal(ctx); err != nil {
				return queue.Op{}, err
			}
		}
	}
	return queue.Op{}, queue.ErrEmpty
}

// claim runs the Lua script and returns the claimed op id, or "" when
// nothing eligible is at the head of the queue.
func (d *Driver) claim(ctx context.Context, types []string) (string, error) {
	argv := make([]any, 0, 5+len(types))
	argv = append(argv,
		d.dataKey(""),
		d.dedupKey(""),
		formatTime(time.Now().UTC()),
		dequeueScanPage,
		dequeueScanLimit,
	)
	for t := range typeSet(types) {
		argv = append(argv, t)
	}

	res, err := dequeueScript.Run(ctx, d.client,
		[]string{d.pendingKey(), d.runningKey()}, argv...).Result()
	switch {
	case err == nil:
	case errors.Is(err, goredis.Nil):
		return "", nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "", nil
	default:
		return "", fmt.Errorf("queue/redis: claim: %w", err)
	}
	id, _ := res.(string)
	return id, nil
}

// waitForSignal blocks until a push into pending rings the doorbell, the
// block window expires, or ctx is cancelled. It never reports a failure
// the caller can act on — the worst outcome is one wasted script call.
func (d *Driver) waitForSignal(ctx context.Context) error {
	_, err := d.client.BLPop(ctx, blockTimeout, d.signalKey()).Result()
	switch {
	case err == nil, errors.Is(err, goredis.Nil):
		return nil
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return nil
	default:
		return fmt.Errorf("queue/redis: wait: %w", err)
	}
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
		pendingIDs, err := d.client.ZRange(ctx, d.pendingKey(), 0, -1).Result()
		if err != nil {
			return nil, 0, fmt.Errorf("queue/redis: list zrange: %w", err)
		}
		allIDs := append([]string(nil), pendingIDs...)
		total := int64(len(pendingIDs))
		listKeys := []string{
			d.runningKey(),
			d.failedKey(), d.cancelledKey(), d.doneKey(),
		}
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

	if status == queue.StatusPending {
		// The pending set is ordered by the claim order itself, so a page
		// of it is what the queue would hand out next — which is exactly
		// what the admin UI is asking for.
		total, err := d.client.ZCard(ctx, d.pendingKey()).Result()
		if err != nil {
			return nil, 0, fmt.Errorf("queue/redis: zcard: %w", err)
		}
		if total == 0 {
			return nil, 0, nil
		}
		ids, err := d.client.ZRange(ctx, d.pendingKey(),
			int64(offset), int64(offset+limit-1)).Result()
		if err != nil {
			return nil, 0, fmt.Errorf("queue/redis: zrange: %w", err)
		}
		ops, err := d.fetchOps(ctx, ids)
		if err != nil {
			return nil, 0, err
		}
		return ops, total, nil
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
	pendingCmd := pipe.ZCard(ctx, d.pendingKey())
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
	pipe.ZRem(ctx, d.pendingKey(), id)
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
	d.releaseDedup(ctx, id)
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
	seqRaw, _ := d.client.HGet(ctx, d.dataKey(id), "seq").Result()
	pipe.ZAdd(ctx, d.pendingKey(), goredis.Z{
		Score:  scoreFromFields(strconv.Itoa(op.Priority), seqRaw),
		Member: id,
	})
	d.ringDoorbell(ctx, pipe)
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
		fields, err := d.client.HMGet(ctx, d.dataKey(id),
			"status", "started_at", "priority", "seq").Result()
		if err != nil {
			return recovered, fmt.Errorf("queue/redis: recover hmget: %w", err)
		}
		status, _ := fields[0].(string)
		startedRaw, _ := fields[1].(string)
		priorityRaw, _ := fields[2].(string)
		seqRaw, _ := fields[3].(string)
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
		pipe.ZAdd(ctx, d.pendingKey(), goredis.Z{
			Score:  scoreFromFields(priorityRaw, seqRaw),
			Member: id,
		})
		d.ringDoorbell(ctx, pipe)
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
		fields, err := d.client.HMGet(ctx, d.dataKey(id), "priority", "seq").Result()
		if err != nil {
			return fmt.Errorf("promote %s: %w", id, err)
		}
		priorityRaw, _ := fields[0].(string)
		seqRaw, _ := fields[1].(string)
		pipe := d.client.TxPipeline()
		pipe.ZRem(ctx, d.scheduledKey(), id)
		pipe.HSet(ctx, d.dataKey(id), "not_before", "")
		pipe.ZAdd(ctx, d.pendingKey(), goredis.Z{
			Score:  scoreFromFields(priorityRaw, seqRaw),
			Member: id,
		})
		d.ringDoorbell(ctx, pipe)
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
func (d *Driver) signalKey() string    { return d.keyPrefix + ":signal" }
func (d *Driver) seqKey() string       { return d.keyPrefix + ":seq" }

// ringDoorbell queues the wake-up token for a push into pending. It is
// always issued inside the same transaction as the push, so a worker can
// never be woken for an op that is not there yet — and never miss one
// that is, because BLPOP takes the token whether it arrives before or
// after the worker starts waiting.
func (d *Driver) ringDoorbell(ctx context.Context, pipe goredis.Pipeliner) {
	pipe.LPush(ctx, d.signalKey(), "1")
	pipe.LTrim(ctx, d.signalKey(), 0, signalCap-1)
}

// scoreFromFields rebuilds a pending score from the raw hash fields, for
// every path that puts an op BACK into pending (promotion off the
// schedule, orphan recovery, an operator's retry) without having decoded
// the whole op.
//
// An op written by a build that had no arrival counter scores 0, which
// puts it at the front of its priority band. That is the right end: those
// ops are, by definition, the oldest ones in the queue.
func scoreFromFields(priorityRaw, seqRaw string) float64 {
	priority, _ := strconv.Atoi(priorityRaw)
	seq, _ := strconv.ParseInt(seqRaw, 10, 64)
	return pendingScore(priority, seq)
}

// dedupKey namespaces one coalescing claim (queue.Op.DedupKey).
func (d *Driver) dedupKey(k string) string { return d.keyPrefix + ":dedup:" + k }

// releaseDedup drops the coalescing claim an op holds, if any. Called the
// moment the op stops being pending — dequeued or cancelled — so the key is
// free again exactly when the SQL drivers' partial index would free it.
func (d *Driver) releaseDedup(ctx context.Context, id string) {
	k, err := d.client.HGet(ctx, d.dataKey(id), "dedup_key").Result()
	if err != nil || k == "" {
		return
	}
	_ = d.client.Del(ctx, d.dedupKey(k)).Err()
}
func (d *Driver) dataKey(id string) string {
	return d.keyPrefix + ":data:" + id
}

// listKeyForStatus maps an Op.Status to the LIST that holds ids in
// that state. Returns an error for unknown values rather than silently
// returning an empty result, which would mask typos in caller code.
func (d *Driver) listKeyForStatus(status string) (string, error) {
	switch status {
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
