package filebody

// Unit-level contract for the byte-source helper. The HTTP surfaces are
// exercised end-to-end in internal/api/handlers/read_during_transfer_test.go;
// what is pinned here is the part those tests can only observe indirectly —
// the ranged-read contract over staging (which must match storage.RangeReader
// exactly, or http.ServeContent hands out wrong windows) and the three
// decisions the package exists to take.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	_ "github.com/brf-tech/filex/backend/internal/db/drivers/sqlite" // register the sqlite driver
	"github.com/brf-tech/filex/backend/internal/filecache"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/throughput"
)

// newTestStore is testutil.NewTestDB, inlined: internal/testutil imports
// internal/api, which imports THIS package, so borrowing it here would be an
// import cycle. Same sqlite-in-memory + migrate recipe, nothing more.
var testDBCounter atomic.Int64

func newTestStore(t *testing.T) db.Store {
	t.Helper()
	dsn := fmt.Sprintf("file:filebody_test_%d?mode=memory&cache=shared", testDBCounter.Add(1))
	drv := db.MustGet("sqlite")
	conn, err := drv.Open(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, db.Migrate(context.Background(), drv, conn))
	return drv.NewStore(conn)
}

const testUploadID = "unit-upload-0001"

// stagePayload writes body into a fresh staging area as `chunk`-sized parts and
// returns the area plus its manifest. Real parts on real disk: the boundary
// behaviour is the whole point, and a single-part fake would hide it.
func stagePayload(t *testing.T, body []byte, chunk int64) (*staging.Area, *staging.Manifest) {
	t.Helper()
	area := staging.New(t.TempDir())
	_, err := area.Create(testUploadID, int64(len(body)), chunk, "")
	require.NoError(t, err)
	for off, n := int64(0), 1; off < int64(len(body)); off, n = off+chunk, n+1 {
		end := off + chunk
		if end > int64(len(body)) {
			end = int64(len(body))
		}
		_, err := area.WritePart(testUploadID, n, bytes.NewReader(body[off:end]), end-off)
		require.NoError(t, err)
	}
	m, err := area.Manifest(testUploadID)
	require.NoError(t, err)
	require.True(t, m.Complete())
	return area, m
}

func stagedSource(t *testing.T, body []byte, chunk int64) *Source {
	t.Helper()
	area, m := stagePayload(t, body, chunk)
	return &Source{
		Staged:   true,
		UploadID: testUploadID,
		NodeID:   7,
		rel:      "docs/moving.bin",
		area:     area,
		man:      m,
		node: &model.Node{
			ID: 7, Path: "/docs/moving.bin", Mime: "application/octet-stream",
			Size: int64(len(body)), UpdatedAt: time.Unix(1700000000, 0),
		},
	}
}

// ── the ranged contract over staging ────────────────────────────────────────

func TestSource_ReadRange_StagedMatchesTheDriverContract(t *testing.T) {
	body := []byte(strings.Repeat("0123456789", 500)) // 5000 bytes
	src := stagedSource(t, body, 1024)                // 5 parts, last one short

	read := func(off, length int64) []byte {
		rc, err := src.ReadRange(context.Background(), off, length)
		require.NoError(t, err)
		defer rc.Close()
		out, err := io.ReadAll(rc)
		require.NoError(t, err)
		return out
	}

	t.Run("whole object", func(t *testing.T) {
		require.Equal(t, body, read(0, -1))
	})
	t.Run("window inside one part", func(t *testing.T) {
		require.Equal(t, body[100:200], read(100, 100))
	})
	t.Run("window straddling a part boundary", func(t *testing.T) {
		// 1000..1100 crosses the 1024-byte seam — the case a naive
		// concatenation gets wrong.
		require.Equal(t, body[1000:1100], read(1000, 100))
	})
	t.Run("window spanning three parts", func(t *testing.T) {
		require.Equal(t, body[900:3300], read(900, 2400))
	})
	t.Run("open-ended from a later part", func(t *testing.T) {
		require.Equal(t, body[4096:], read(4096, -1))
	})
	t.Run("length past EOF is clamped, not an error", func(t *testing.T) {
		require.Equal(t, body[4900:], read(4900, 10000))
	})
	t.Run("offset at EOF is EOF, not an error", func(t *testing.T) {
		require.Empty(t, read(int64(len(body)), -1))
	})
	t.Run("offset past EOF is EOF, not an error", func(t *testing.T) {
		require.Empty(t, read(int64(len(body))+500, -1))
	})
	t.Run("zero length yields an immediate EOF", func(t *testing.T) {
		require.Empty(t, read(10, 0))
	})
	t.Run("a negative offset is an error", func(t *testing.T) {
		_, err := src.ReadRange(context.Background(), -1, 10)
		require.Error(t, err)
	})
}

func TestSource_Open_StagedReturnsTheWholeAssembledBody(t *testing.T) {
	body := []byte(strings.Repeat("abcdefghij", 999))
	src := stagedSource(t, body, 512)

	rc, err := src.Open(context.Background())
	require.NoError(t, err)
	defer rc.Close()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.True(t, bytes.Equal(body, got))
	require.True(t, src.CanRange(), "staging is always seekable")
	require.EqualValues(t, len(body), src.Size())
}

// Decision 1: what Stat answers while staged — the committed values.
func TestSource_Stat_StagedAnswersTheCommittedValues(t *testing.T) {
	body := []byte(strings.Repeat("x", 3000))
	src := stagedSource(t, body, 1024)

	obj, err := src.Stat(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, len(body), obj.Size)
	require.Equal(t, storage.KindFile, obj.Kind)
	require.Equal(t, "moving.bin", obj.Name)
	require.Equal(t, "application/octet-stream", obj.Mime)
	require.Equal(t, src.man.CompositeETag(), obj.Etag,
		"the ETag must describe the staged bytes, so an overwrite changes it")
	require.NotEmpty(t, obj.Etag)
	require.False(t, obj.Mtime.IsZero())
}

// ── resolution against a real catalogue ─────────────────────────────────────

// resolverFixture: a store, a local driver and a node, wired the way a request
// path would have them.
type resolverFixture struct {
	store    db.Store
	drv      *local.Driver
	root     string
	resolver *Resolver
	storage  *model.Storage
}

func newResolverFixture(t *testing.T, area *staging.Area) *resolverFixture {
	t.Helper()
	store := newTestStore(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	st, err := store.CreateStorage(context.Background(), &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true,
		ConfigJSON: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	return &resolverFixture{
		store: store, drv: drv, root: root,
		resolver: New(store, area), storage: st,
	}
}

// A path that is not in the catalogue resolves to the driver — this is what
// keeps freshly-written, not-yet-synced files readable.
func TestResolve_UnknownPathFallsBackToTheDriver(t *testing.T) {
	f := newResolverFixture(t, staging.New(t.TempDir()))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "plain.txt"), []byte("hello"), 0o644))

	src, err := f.resolver.Resolve(context.Background(), f.drv, f.storage.ID, "plain.txt", nil)
	require.NoError(t, err)
	require.False(t, src.Staged)

	rc, err := src.Open(context.Background())
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	require.Equal(t, "hello", string(got))
}

// Decision 3: staged, but staging is gone. Reachable in production — the
// sweeper removes a `failed` session after a full idle TTL and the node keeps
// saying "staged" forever. It must fail loudly, and it must NOT quietly fall
// back to the driver, which on an overwrite holds the version being replaced.
func TestResolve_StagedWithNoSessionIsAnError(t *testing.T) {
	f := newResolverFixture(t, staging.New(t.TempDir()))
	// The driver deliberately DOES hold a file at that path: a fallback would
	// look like it worked.
	require.NoError(t, os.WriteFile(filepath.Join(f.root, "ghost.bin"), []byte("the old version"), 0o644))
	_, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.storage.ID, Name: "ghost.bin", Path: "/ghost.bin",
		PathHash: pathkey.Hash(f.storage.ID, "/ghost.bin"), Type: model.NodeTypeFile,
		TransferState: model.TransferStateStaged, Size: 15,
	})
	require.NoError(t, err)

	src, err := f.resolver.Resolve(context.Background(), f.drv, f.storage.ID, "ghost.bin", nil)
	require.Nil(t, src)
	require.ErrorIs(t, err, ErrStagingGone)
	require.Contains(t, err.Error(), "ghost.bin", "the message must name the file")
}

// Same, one step further along: the session row survives but its manifest does
// not. Assembling what is left would hand out a silently truncated file.
func TestResolve_StagedWithNoManifestIsAnError(t *testing.T) {
	area := staging.New(t.TempDir())
	f := newResolverFixture(t, area)
	node, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID: f.storage.ID, Name: "half.bin", Path: "/half.bin",
		PathHash: pathkey.Hash(f.storage.ID, "/half.bin"), Type: model.NodeTypeFile,
		TransferState: model.TransferStateStaged, Size: 10,
	})
	require.NoError(t, err)
	nodeID := node.ID
	require.NoError(t, f.store.CreateStagedUpload(context.Background(), &model.StagedUpload{
		ID: testUploadID, StorageID: f.storage.ID, StorageKey: "/half.bin",
		TotalSize: 10, ChunkSize: 4096, State: model.StagedUploadFailed,
		NodeID: &nodeID, ExpiresAt: time.Now().Add(time.Hour),
	}))

	src, err := f.resolver.Resolve(context.Background(), f.drv, f.storage.ID, "half.bin", nil)
	require.Nil(t, src)
	require.ErrorIs(t, err, ErrStagingGone)
	require.True(t, errors.Is(err, ErrStagingGone))
}

// A nil resolver is usable and means "the driver" — every construction site
// that never wires staging (tests, embedders, list-only environments) keeps
// working rather than failing closed.
func TestResolve_NilResolverIsDriverOnly(t *testing.T) {
	var r *Resolver
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	require.NoError(t, os.WriteFile(filepath.Join(root, "x.txt"), []byte("ok"), 0o644))

	src, err := r.Resolve(context.Background(), drv, 1, "x.txt", nil)
	require.NoError(t, err)
	require.False(t, src.Staged)
	rc, err := src.Open(context.Background())
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	require.Equal(t, "ok", string(got))
}

// ── the throughput meter is fed from exactly one place ──────────────────────

// countObservations subscribes to the process-wide meter and returns a reader
// of what it saw for one storage.
//
// ⚠ throughput.Subscribe has no unsubscribe (the real subscriber is
// internal/metrics, registered once at init), so the closure outlives the test
// — which is why it filters on a storage id nothing else in this package uses.
func countObservations(storageID int64) (calls *atomic.Int64, bytes *atomic.Int64) {
	calls, bytes = &atomic.Int64{}, &atomic.Int64{}
	throughput.Subscribe(func(id int64, dir throughput.Direction, n int64, _ time.Duration) {
		if id != storageID || dir != throughput.Read {
			return
		}
		calls.Add(1)
		bytes.Add(n)
	})
	return calls, bytes
}

// TestCacheFillIsObservedExactlyOnce.
//
// Two packages instrument driver reads and they must not both count the same
// bytes: internal/throughput wraps the reader Open/ReadRange hand out, and
// internal/filecache fetches the whole object to build a local copy. The fill
// runs through the callback this package supplies, so it is the one place
// where "who measures this?" could be answered twice — or, before the merge,
// not at all (the fill callback returned a bare driver read, so the single
// fastest, cleanest sample the meter could ever get was thrown away).
//
// What is pinned, in order:
//
//	1 observation for the fill  — the read that really comes off the driver,
//	                              paced by nothing but the backend
//	+0 for a cache hit          — local disk; counting it would tell the meter
//	                              a NAS runs at NVMe speed and the storage
//	                              would never be classified slow again
func TestCacheFillIsObservedExactlyOnce(t *testing.T) {
	const storageID = 9101

	// Make the storage qualify the way production does — measured slow, three
	// samples at 2 MiB/s against the 10 MiB/s default — rather than by forcing
	// a flag. Seeded BEFORE subscribing, so the counter below sees only what
	// the fill and the read that follows it do.
	for i := 0; i < 3; i++ {
		throughput.Observe(storageID, throughput.Read, 8<<20, 4*time.Second)
	}
	calls, bytesSeen := countObservations(storageID)

	ctx := context.Background()
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	body := bytes.Repeat([]byte("z"), 5<<20) // over throughput.MinSample
	require.NoError(t, os.WriteFile(filepath.Join(root, "big.bin"), body, 0o644))

	cache := filecache.New(filecache.Config{
		Dir: t.TempDir(), MinSize: 1, MaxBytes: 64 << 20, PinTTL: time.Minute,
	})
	r := New(nil, nil).WithCache(cache)

	// The node is handed in rather than looked up: what is under test is the
	// read path, not the catalogue.
	node := &model.Node{ID: 7701, StorageID: storageID, Path: "/big.bin",
		Name: "big.bin", Type: model.NodeTypeFile, Size: int64(len(body))}
	src, err := r.Resolve(ctx, drv, storageID, "big.bin", node)
	require.NoError(t, err)
	stat, err := drv.Stat(ctx, "big.bin")
	require.NoError(t, err)

	prep := src.Prepare(ctx, stat)
	require.NotNil(t, prep, "a big file on a slow storage must qualify")
	if fill, ok := cache.Filling(src.cacheKey); ok {
		require.NoError(t, fill.Wait(ctx))
	}
	require.True(t, cache.Ready(src.cacheKey), "the fill must have landed")

	require.EqualValues(t, 1, calls.Load(), "the fill is measured once, not twice and not zero times")
	require.EqualValues(t, len(body), bytesSeen.Load(), "and it is measured for the whole object")

	// Now serve it. This comes off the local copy and must add nothing.
	rc, err := src.Open(ctx)
	require.NoError(t, err)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.Equal(t, body, got)

	require.EqualValues(t, 1, calls.Load(), "a cache hit is local disk and must not be measured")
	require.EqualValues(t, len(body), bytesSeen.Load())
}

// A driver read that is NOT a fill is measured too, and once — otherwise the
// test above would also pass on a build that had stopped measuring downloads.
func TestDriverReadIsObservedOnce(t *testing.T) {
	const storageID = 9102
	calls, bytesSeen := countObservations(storageID)

	ctx := context.Background()
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	body := bytes.Repeat([]byte("q"), 5<<20)
	require.NoError(t, os.WriteFile(filepath.Join(root, "plain.bin"), body, 0o644))

	var r *Resolver
	src, err := r.Resolve(ctx, drv, storageID, "plain.bin", nil)
	require.NoError(t, err)

	rc, err := src.Open(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 0, calls.Load(), "nothing is recorded until the transfer finishes")
	_, err = io.Copy(io.Discard, rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())

	require.EqualValues(t, 1, calls.Load())
	require.EqualValues(t, len(body), bytesSeen.Load())
}
