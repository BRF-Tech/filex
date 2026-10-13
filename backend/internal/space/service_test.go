package space

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// bucket is a driver with no idea how big its backend is.
type bucket struct{}

func (bucket) Init(context.Context, map[string]any) error             { return nil }
func (bucket) Name() string                                           { return "bucket" }
func (bucket) List(context.Context, string) ([]storage.Object, error) { return nil, nil }
func (bucket) Stat(context.Context, string) (storage.Object, error) {
	return storage.Object{}, storage.ErrNotFound
}
func (bucket) Read(context.Context, string) (io.ReadCloser, error) { return nil, storage.ErrNotFound }
func (bucket) Capabilities() storage.Capabilities                  { return storage.Capabilities{Read: true} }

// disk is a driver that knows its room, and counts how often it was asked.
type disk struct {
	bucket
	room storage.Space
	asks atomic.Int32
	// hang makes Space wait for the caller's context.
	hang bool
}

func (d *disk) Space(ctx context.Context, _ string) (storage.Space, error) {
	d.asks.Add(1)
	if d.hang {
		<-ctx.Done()
		return storage.Space{}, ctx.Err()
	}
	return d.room, nil
}

// fixture is a service over the drivers given, storage ids 1..n.
func fixture(t *testing.T, q *quota.Service, drivers ...storage.Driver) (*Service, []*model.Storage) {
	t.Helper()
	sts := make([]*model.Storage, len(drivers))
	for i := range drivers {
		sts[i] = &model.Storage{ID: int64(i + 1), Name: "s" + string(rune('a'+i))}
	}
	svc := New(func(id int64) (storage.Driver, error) { return drivers[id-1], nil }, q)
	return svc, sts
}

func TestAStorageReportsItsDisk(t *testing.T) {
	d := &disk{room: storage.Space{Total: 1000, Used: 300, Free: 650, Volume: "dev:1"}}
	svc, sts := fixture(t, nil, d)
	got := svc.Storage(context.Background(), sts[0], "photos")
	want := Figures{Total: 1000, Used: 300, Free: 650, Known: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The case issue #116 was filed about: the root lists the storages, and a
// client that maps the server as a drive asks about the root.
func TestTheRootAddsUpTheStoragesAndCountsASharedDiskOnce(t *testing.T) {
	raid := storage.Space{Total: 20_000, Used: 5_000, Free: 15_000, Volume: "dev:8"}
	usb := storage.Space{Total: 1_000, Used: 100, Free: 900, Volume: "dev:9"}
	svc, sts := fixture(t, nil, &disk{room: raid}, &disk{room: raid}, &disk{room: usb})
	got := svc.Root(context.Background(), sts)
	want := Figures{Total: 21_000, Used: 5_100, Free: 15_900, Known: true}
	if got != want {
		t.Errorf("got %+v, want %+v (two storages on the RAID are one disk)", got, want)
	}
}

func TestTheRootLeavesOutAStorageThatCannotSay(t *testing.T) {
	svc, sts := fixture(t, nil, &disk{room: storage.Space{Total: 1000, Used: 400, Free: 600}}, bucket{})
	got := svc.Root(context.Background(), sts)
	want := Figures{Total: 1000, Used: 400, Free: 600, Known: true}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestNothingMeasurableIsUnknownAndStandsInLarge(t *testing.T) {
	svc, sts := fixture(t, nil, bucket{})
	got := svc.Root(context.Background(), sts)
	if got.Known {
		t.Fatalf("an object store alone was measured: %+v", got)
	}
	in := got.OrStandIn()
	// ⚠ Never zero: WinSCP and Explorer read zero free blocks as a full disk
	// and refuse to start a transfer.
	if in.Total == 0 || in.Free == 0 || in.Used != 0 {
		t.Errorf("stand-in = %+v, want a large empty disk", in)
	}
}

func quotaFixture(t *testing.T, limit, used int64) (*quota.Service, context.Context) {
	t.Helper()
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	u, err := store.CreateUser(ctx, "space@example.com", "x", model.RoleUser, "en", "UTC")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	q := quota.New(store)
	if err := q.SetQuota(ctx, u.ID, limit); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	if err := q.AddUsage(ctx, u.ID, used); err != nil {
		t.Fatalf("usage: %v", err)
	}
	return q, auth.WithUser(ctx, u)
}

func TestAQuotaIsACeilingOfItsOwn(t *testing.T) {
	q, ctx := quotaFixture(t, 100, 30)

	// A big disk: the quota is the tighter limit.
	big := &disk{room: storage.Space{Total: 10_000, Used: 2_000, Free: 8_000}}
	svc, sts := fixture(t, q, big)
	if got, want := svc.Storage(ctx, sts[0], ""), (Figures{Total: 100, Used: 30, Free: 70, Known: true}); got != want {
		t.Errorf("big disk: got %+v, want %+v", got, want)
	}

	// A nearly full disk: it is.
	full := &disk{room: storage.Space{Total: 10_000, Used: 9_950, Free: 50}}
	svc, sts = fixture(t, q, full)
	if got, want := svc.Storage(ctx, sts[0], ""), (Figures{Total: 100, Used: 30, Free: 50, Known: true}); got != want {
		t.Errorf("full disk: got %+v, want %+v", got, want)
	}

	// Nothing measurable: the quota alone, as before.
	svc, sts = fixture(t, q, bucket{})
	if got, want := svc.Root(ctx, sts), (Figures{Total: 100, Used: 30, Free: 70, Known: true}); got != want {
		t.Errorf("object store: got %+v, want %+v", got, want)
	}
}

func TestAnUnlimitedAccountSeesTheDisk(t *testing.T) {
	q, ctx := quotaFixture(t, 0, 30)
	d := &disk{room: storage.Space{Total: 10_000, Used: 2_000, Free: 8_000}}
	svc, sts := fixture(t, q, d)
	if got, want := svc.Storage(ctx, sts[0], ""), (Figures{Total: 10_000, Used: 2_000, Free: 8_000, Known: true}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestAnAnswerIsReusedForAWhile(t *testing.T) {
	d := &disk{room: storage.Space{Total: 1000, Free: 1000}}
	svc, sts := fixture(t, nil, d)
	now := time.Unix(1_800_000_000, 0)
	svc.now = func() time.Time { return now }

	for range 5 {
		svc.Storage(context.Background(), sts[0], "")
	}
	if n := d.asks.Load(); n != 1 {
		t.Fatalf("asked %d times within the cache window, want 1", n)
	}
	now = now.Add(ttl + time.Second)
	svc.Storage(context.Background(), sts[0], "")
	if n := d.asks.Load(); n != 2 {
		t.Errorf("asked %d times after the window, want 2", n)
	}
}

// A share that does not answer must not hang `df`, and must not be asked again
// on every folder the client opens.
func TestAStorageThatDoesNotAnswerIsUnknownAndRemembered(t *testing.T) {
	d := &disk{hang: true}
	svc, sts := fixture(t, nil, d)
	svc.timeout = 50 * time.Millisecond

	start := time.Now()
	if got := svc.Storage(context.Background(), sts[0], ""); got.Known {
		t.Fatalf("a storage that never answered was measured: %+v", got)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("waited %v for a storage that does not answer", took)
	}
	svc.Storage(context.Background(), sts[0], "")
	if n := d.asks.Load(); n != 1 {
		t.Errorf("asked %d times, want 1 (the silence is remembered)", n)
	}
}

// A caller that hung up has not learnt anything about the storage.
func TestACallersOwnCancellationIsNotRemembered(t *testing.T) {
	d := &disk{room: storage.Space{Total: 1000, Free: 1000}}
	svc, sts := fixture(t, nil, d)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc.Storage(ctx, sts[0], "")
	if got := svc.Storage(context.Background(), sts[0], ""); !got.Known {
		t.Errorf("the next caller got %+v: a cancelled request was cached as the answer", got)
	}
}

func TestBlocksSpeakStatvfs(t *testing.T) {
	f := Figures{Total: 4096 * 10, Used: 4096 * 3, Free: 4096 * 5, Known: true}
	blocks, bfree, bavail := f.Blocks(4096)
	if blocks != 10 || bfree != 7 || bavail != 5 {
		t.Errorf("blocks=%d bfree=%d bavail=%d, want 10 7 5 (df: size 10, used 3, avail 5)", blocks, bfree, bavail)
	}
}
