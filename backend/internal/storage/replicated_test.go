package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDriver implements Driver + Writer + Deleter + Mover + Copier +
// Mkdirer with in-memory state. Errors can be injected per-method via
// the *Err fields.
type fakeDriver struct {
	name string

	mu      sync.Mutex
	files   map[string][]byte
	stats   map[string]Object
	deleted map[string]bool

	readErr   error
	writeErr  error
	statErr   error
	deleteErr error
	moveErr   error
	copyErr   error
	mkdirErr  error
	listErr   error

	writeCount  atomic.Int32
	deleteCount atomic.Int32
	moveCount   atomic.Int32
	copyCount   atomic.Int32
	readCount   atomic.Int32
}

func newFakeDriver(name string) *fakeDriver {
	return &fakeDriver{
		name:    name,
		files:   map[string][]byte{},
		stats:   map[string]Object{},
		deleted: map[string]bool{},
	}
}

func (f *fakeDriver) Name() string                                   { return f.name }
func (f *fakeDriver) Init(_ context.Context, _ map[string]any) error { return nil }
func (f *fakeDriver) Capabilities() Capabilities {
	return Capabilities{Read: true, Write: true, Move: true, Copy: true, Delete: true, Mkdir: true}
}
func (f *fakeDriver) List(_ context.Context, _ string) ([]Object, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return nil, nil
}
func (f *fakeDriver) Stat(_ context.Context, p string) (Object, error) {
	if f.statErr != nil {
		return Object{}, f.statErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.stats[p]
	if !ok {
		return Object{}, ErrNotFound
	}
	return o, nil
}
func (f *fakeDriver) Read(_ context.Context, p string) (io.ReadCloser, error) {
	f.readCount.Add(1)
	if f.readErr != nil {
		return nil, f.readErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[p]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(strings.NewReader(string(b))), nil
}
func (f *fakeDriver) Write(_ context.Context, p string, r io.Reader, size int64) error {
	f.writeCount.Add(1)
	if f.writeErr != nil {
		return f.writeErr
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[p] = body
	f.stats[p] = Object{Path: p, Size: int64(len(body))}
	return nil
}
func (f *fakeDriver) Delete(_ context.Context, p string) error {
	f.deleteCount.Add(1)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, p)
	delete(f.stats, p)
	f.deleted[p] = true
	return nil
}
func (f *fakeDriver) Move(_ context.Context, src, dst string) error {
	f.moveCount.Add(1)
	if f.moveErr != nil {
		return f.moveErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[src]
	if !ok {
		return ErrNotFound
	}
	delete(f.files, src)
	delete(f.stats, src)
	f.files[dst] = b
	f.stats[dst] = Object{Path: dst, Size: int64(len(b))}
	return nil
}
func (f *fakeDriver) Copy(_ context.Context, src, dst string) error {
	f.copyCount.Add(1)
	if f.copyErr != nil {
		return f.copyErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.files[src]
	if !ok {
		return ErrNotFound
	}
	bb := make([]byte, len(b))
	copy(bb, b)
	f.files[dst] = bb
	f.stats[dst] = Object{Path: dst, Size: int64(len(bb))}
	return nil
}
func (f *fakeDriver) Mkdir(_ context.Context, _ string) error {
	if f.mkdirErr != nil {
		return f.mkdirErr
	}
	return nil
}

// fakeRecorder is an in-memory FailureRecorder for the wrapper test.
type fakeRecorder struct {
	mu       sync.Mutex
	recorded []recordedFailure
	resolved []string
}

type recordedFailure struct{ Path, Op, Code, Msg string }

func (r *fakeRecorder) Record(_ context.Context, path, op, code, msg string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recorded = append(r.recorded, recordedFailure{path, op, code, msg})
	return nil
}
func (r *fakeRecorder) Resolve(_ context.Context, path, op string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolved = append(r.resolved, path+":"+op)
	return nil
}

// fakeNotifier captures emitted events.
type fakeNotifier struct {
	mu   sync.Mutex
	fail int
	read int
}

func (n *fakeNotifier) NotifyReplicaFail(_ context.Context, _, _ string, _ error, _ int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fail++
}
func (n *fakeNotifier) NotifyPrimaryReadFail(_ context.Context, _ string, _ error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.read++
}

// TestReplicated_Write_FansOutAsync — primary write succeeds, replica
// write happens in a goroutine.
func TestReplicated_Write_FansOutAsync(t *testing.T) {
	primary := newFakeDriver("primary")
	replica := newFakeDriver("replica")
	rec := &fakeRecorder{}
	notif := &fakeNotifier{}
	r := NewReplicated(primary, replica, DefaultRules(), rec, notif)
	defer r.Stop()

	require.NoError(t, r.Write(context.Background(), "foo.txt", strings.NewReader("hello"), 5))
	r.Stop() // wait for the replica goroutine

	assert.Equal(t, int32(1), primary.writeCount.Load())
	assert.Equal(t, int32(1), replica.writeCount.Load())
	assert.Equal(t, 0, len(rec.recorded), "no failures expected")
}

// TestReplicated_Write_ReplicaFailRecorded — primary OK, replica
// errors; the failure is recorded + notification sent.
func TestReplicated_Write_ReplicaFailRecorded(t *testing.T) {
	primary := newFakeDriver("primary")
	replica := newFakeDriver("replica")
	replica.writeErr = errors.New("disk full")
	rec := &fakeRecorder{}
	notif := &fakeNotifier{}
	r := NewReplicated(primary, replica, DefaultRules(), rec, notif)

	require.NoError(t, r.Write(context.Background(), "foo.txt", strings.NewReader("hello"), 5))
	r.Stop()

	assert.Equal(t, 1, len(rec.recorded))
	assert.Equal(t, "foo.txt", rec.recorded[0].Path)
	assert.Equal(t, "REPLICA_WRITE_FAIL", rec.recorded[0].Code)
	assert.Equal(t, 1, notif.fail)
}

// TestReplicated_Read_PrimaryFailsReplicaServes — read fallback hits
// replica + emits primary_read_fail.
func TestReplicated_Read_PrimaryFailsReplicaServes(t *testing.T) {
	primary := newFakeDriver("primary")
	replica := newFakeDriver("replica")
	primary.readErr = errors.New("primary down")
	replica.files["foo.txt"] = []byte("from replica")
	rec := &fakeRecorder{}
	notif := &fakeNotifier{}
	r := NewReplicated(primary, replica, DefaultRules(), rec, notif)
	defer r.Stop()

	rc, err := r.Read(context.Background(), "foo.txt")
	require.NoError(t, err)
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	assert.Equal(t, "from replica", string(body))
	assert.Equal(t, 1, notif.read)
}

// TestReplicated_Delete_MirrorMode — primary deletes + replica
// deletes asynchronously.
func TestReplicated_Delete_MirrorMode(t *testing.T) {
	primary := newFakeDriver("primary")
	replica := newFakeDriver("replica")
	primary.files["foo.txt"] = []byte("x")
	replica.files["foo.txt"] = []byte("x")
	rec := &fakeRecorder{}
	notif := &fakeNotifier{}
	r := NewReplicated(primary, replica, DefaultRules(), rec, notif)

	require.NoError(t, r.Delete(context.Background(), "foo.txt"))
	r.Stop()

	assert.Equal(t, int32(1), primary.deleteCount.Load())
	assert.Equal(t, int32(1), replica.deleteCount.Load())
	assert.Equal(t, 0, len(rec.recorded))
}

// TestReplicated_Delete_AppendOnlyKeepsReplica — primary deletes but
// replica keeps the row (append-only audit trail).
func TestReplicated_Delete_AppendOnlyKeepsReplica(t *testing.T) {
	primary := newFakeDriver("primary")
	replica := newFakeDriver("replica")
	primary.files["audit.log"] = []byte("x")
	replica.files["audit.log"] = []byte("x")
	rules := NewRulesEngine(func() ([]RuleSpec, ReplicaMode) {
		return []RuleSpec{{ID: 1, Pattern: "audit.log", Mode: ModeAppendOnly, Priority: 1, Enabled: true}}, ModeMirror
	})
	r := NewReplicated(primary, replica, rules, &fakeRecorder{}, &fakeNotifier{})

	require.NoError(t, r.Delete(context.Background(), "audit.log"))
	r.Stop()

	assert.Equal(t, int32(1), primary.deleteCount.Load())
	assert.Equal(t, int32(0), replica.deleteCount.Load(), "append_only must not delete on replica")
}

// TestReplicated_Skip_AllOpsBypassReplica — skip mode means the
// replica never sees the path.
func TestReplicated_Skip_AllOpsBypassReplica(t *testing.T) {
	primary := newFakeDriver("primary")
	replica := newFakeDriver("replica")
	primary.files["temp/x.tmp"] = []byte("garbage")
	rules := NewRulesEngine(func() ([]RuleSpec, ReplicaMode) {
		return []RuleSpec{{ID: 1, Pattern: "temp/*", Mode: ModeSkip, Priority: 1, Enabled: true}}, ModeMirror
	})
	r := NewReplicated(primary, replica, rules, &fakeRecorder{}, &fakeNotifier{})

	require.NoError(t, r.Write(context.Background(), "temp/x.tmp", strings.NewReader("garbage"), 7))
	require.NoError(t, r.Delete(context.Background(), "temp/x.tmp"))
	r.Stop()

	assert.Equal(t, int32(0), replica.writeCount.Load())
	assert.Equal(t, int32(0), replica.deleteCount.Load())
}

// TestReplicated_NoReplica — wrapper acts as passthrough when no
// replica is configured.
func TestReplicated_NoReplica(t *testing.T) {
	primary := newFakeDriver("primary")
	r := NewReplicated(primary, nil, DefaultRules(), &fakeRecorder{}, &fakeNotifier{})
	defer r.Stop()

	require.NoError(t, r.Write(context.Background(), "foo.txt", strings.NewReader("x"), 1))
	require.NoError(t, r.Delete(context.Background(), "foo.txt"))
	assert.Equal(t, int32(1), primary.writeCount.Load())
	assert.Equal(t, int32(1), primary.deleteCount.Load())
}

// TestReplicated_Resolves_OnSecondaryWriteSuccess — successful replica
// write should clear any prior failure record (idempotency).
func TestReplicated_Resolves_OnSecondaryWriteSuccess(t *testing.T) {
	primary := newFakeDriver("primary")
	replica := newFakeDriver("replica")
	rec := &fakeRecorder{}
	r := NewReplicated(primary, replica, DefaultRules(), rec, &fakeNotifier{})

	require.NoError(t, r.Write(context.Background(), "foo.txt", strings.NewReader("x"), 1))
	r.Stop()

	assert.Contains(t, rec.resolved, "foo.txt:write")
}

// makeWaitGroupForReplica is a tiny helper that gives tests a way to
// wait for at least one replica fan-out to complete before asserting.
// (Kept here to document the pattern; today's tests rely on Stop().)
var _ = func() time.Duration { return 50 * time.Millisecond }()
