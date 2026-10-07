package replica

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/storage"

	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// The test environment of the initial copy and the repairs: a real (SQLite)
// store with every migration, real local disks for the storage and its
// target, and a queue that keeps ops in memory the way the real drivers do
// (a payload that goes through JSON, a dedup key held while an op waits).

var storeSeq atomic.Int64

func openStore(t *testing.T) db.Store {
	t.Helper()
	ctx := context.Background()
	drv := db.MustGet("sqlite")
	dsn := fmt.Sprintf("file:replica_test_%d_%d?mode=memory&cache=shared", time.Now().UnixNano(), storeSeq.Add(1))
	conn, err := drv.Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, db.Migrate(ctx, drv, conn))
	return drv.NewStore(conn)
}

func localDriver(t *testing.T, dir string) storage.Driver {
	t.Helper()
	d, err := storage.Get("local")
	require.NoError(t, err)
	require.NoError(t, d.Init(context.Background(), map[string]any{"path": dir}))
	return d
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

func readFile(t *testing.T, dir, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "", false
	}
	return string(b), true
}

// memQueue is a queue.Driver that honours DedupKey the way the real drivers
// do: while a pending op holds a key, Enqueue refuses the key.
type memQueue struct {
	queue.Driver
	mu      sync.Mutex
	pending []queue.Op
	keys    map[string]bool
}

func newMemQueue() *memQueue { return &memQueue{keys: map[string]bool{}} }

func (q *memQueue) Enqueue(_ context.Context, op queue.Op) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if op.DedupKey != "" {
		if q.keys[op.DedupKey] {
			return "", queue.ErrDuplicate
		}
		q.keys[op.DedupKey] = true
	}
	// The payload comes back from the database as JSON does: numbers as
	// float64.
	raw, _ := json.Marshal(op.Payload)
	var back map[string]any
	_ = json.Unmarshal(raw, &back)
	op.Payload = back
	op.ID = fmt.Sprintf("op-%d", len(q.pending)+1)
	q.pending = append(q.pending, op)
	return op.ID, nil
}

// next takes the oldest waiting op, the way Dequeue does (its key is free
// again from then on).
func (q *memQueue) next() (queue.Op, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return queue.Op{}, false
	}
	op := q.pending[0]
	q.pending = q.pending[1:]
	delete(q.keys, op.DedupKey)
	return op, true
}

func (q *memQueue) waiting() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// staticWrappers answers Wrappers from a fixed set of links.
type staticWrappers map[int64]Link

func (w staticWrappers) Replicated(_ context.Context, id int64) (Link, error) {
	l, ok := w[id]
	if !ok {
		return Link{}, ErrNotLinked
	}
	return l, nil
}

// countingDriver counts the writes that reach a driver, by path.
type countingDriver struct {
	storage.Driver
	mu     sync.Mutex
	writes map[string]int
	down   atomic.Bool
}

func newCounting(d storage.Driver) *countingDriver {
	return &countingDriver{Driver: d, writes: map[string]int{}}
}

var errDown = fmt.Errorf("dial tcp 10.0.0.9:445: connect: connection refused")

func (c *countingDriver) Write(ctx context.Context, p string, r io.Reader, size int64) error {
	if c.down.Load() {
		return errDown
	}
	c.mu.Lock()
	c.writes[p]++
	c.mu.Unlock()
	return c.Driver.(storage.Writer).Write(ctx, p, r, size)
}

func (c *countingDriver) Stat(ctx context.Context, p string) (storage.Object, error) {
	if c.down.Load() {
		return storage.Object{}, errDown
	}
	return c.Driver.Stat(ctx, p)
}

func (c *countingDriver) SetMtime(ctx context.Context, p string, m time.Time) error {
	if c.down.Load() {
		return errDown
	}
	return c.Driver.(storage.Toucher).SetMtime(ctx, p, m)
}

func (c *countingDriver) Delete(ctx context.Context, p string) error {
	if c.down.Load() {
		return errDown
	}
	return c.Driver.(storage.Deleter).Delete(ctx, p)
}

func (c *countingDriver) count(p string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes[p]
}

// linked is a storage linked to a target, both on local disks.
type linked struct {
	store      db.Store
	st         *model.Storage
	target     *model.ReplicationTarget
	primaryDir string
	targetDir  string
	replica    *countingDriver
	rules      storage.RuleEngine
}

// newLinked makes the rows (a storage linked to an enabled target) and the
// directories; files are put in place by the test.
func newLinked(t *testing.T, store db.Store, rules ...storage.RuleSpec) *linked {
	t.Helper()
	ctx := context.Background()
	l := &linked{store: store, primaryDir: t.TempDir(), targetDir: t.TempDir()}
	tcfg, _ := json.Marshal(map[string]any{"path": l.targetDir})
	seq := storeSeq.Add(1)
	target, err := store.CreateReplicationTarget(ctx, &model.ReplicationTarget{
		Name: fmt.Sprintf("storage-box-%d", seq), Driver: "local", ConfigJSON: tcfg, Mode: "async", Enabled: true,
	})
	require.NoError(t, err)
	l.target = target
	scfg, _ := json.Marshal(map[string]any{"path": l.primaryDir})
	tid := target.ID
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: fmt.Sprintf("arsiv-%d", seq), Driver: "local", MountPath: fmt.Sprintf("/arsiv-%d", seq), ConfigJSON: scfg,
		SyncMode: model.SyncModeOnDemand, SyncIntervalS: 900, Enabled: true, ReplicaTargetID: &tid,
	})
	require.NoError(t, err)
	l.st = st
	specs := append([]storage.RuleSpec(nil), rules...)
	l.rules = storage.NewRulesEngine(func() ([]storage.RuleSpec, storage.ReplicaMode) { return specs, storage.ModeMirror })
	return l
}

// wrapper builds the live wrapper the server would hand out for the storage:
// a fresh one each call (a restart builds a new one).
func (l *linked) wrapper(t *testing.T) Link {
	t.Helper()
	if l.replica == nil {
		l.replica = newCounting(localDriver(t, l.targetDir))
	}
	rd := storage.NewReplicated(localDriver(t, l.primaryDir), l.replica, l.rules,
		NewFailureRecorder(l.store, l.st.ID), nil)
	t.Cleanup(rd.Stop)
	return Link{StorageID: l.st.ID, TargetID: l.target.ID, Driver: rd}
}

// drain runs every queued op through its handler, the way the pool does,
// and fails the test if they never run out.
func drain(t *testing.T, svc *Service, q *memQueue) int {
	t.Helper()
	ran := 0
	for ; ran < 500; ran++ {
		op, ok := q.next()
		if !ok {
			return ran
		}
		var err error
		switch op.Type {
		case queue.TypeReplicaInitialCopy:
			err = svc.HandleInitialCopy(context.Background(), op)
		case queue.TypeReplicaRetry:
			err = svc.HandleRetry(context.Background(), op)
		default:
			t.Fatalf("unexpected op %q", op.Type)
		}
		require.NoError(t, err)
	}
	t.Fatal("the queue never ran dry")
	return ran
}
