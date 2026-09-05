package handlers_test

// Staged upload — the parts that need the types this change introduced:
// the injected staging.Area (disk guard, GC), storage.PartUploader
// (re-chunking into a backend multipart upload) and the post-write hook fan-out.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// ── disk guard ──────────────────────────────────────────────────────────────

// The whole object passes through staging, so an upload the staging filesystem
// cannot hold must be refused at `begin` — not discovered at 90 %, with the
// rest of the instance sharing the disk.
func TestStagedUpload_DiskGuardRefusesWhenSpaceIsShort(t *testing.T) {
	var stagingDir string
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		stagingDir = filepath.Join(d.Cfg.DataDir, "uploads")
		area := staging.New(stagingDir)
		// 6 MiB free. The guard needs size × 1.2.
		area.FreeBytes = func(string) (uint64, error) { return 6 << 20, nil }
		d.Staging = area
	})

	// 8 MiB needs 9.6 MiB free — refused.
	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "too-big-for-disk.bin", "size": 8 << 20,
	})
	require.Equal(t, http.StatusInsufficientStorage, code, "%v", out)
	assert.Equal(t, "NO_DISK_SPACE", out["code"])
	assert.Contains(t, fmt.Sprint(out["error"]), "free")

	// 4 MiB needs 4.8 MiB — accepted. The guard is a threshold, not a wall.
	code, out = f.begin(t, map[string]any{
		"path": "main://", "name": "fits.bin", "size": 4 << 20, "chunk_size": 1 << 20,
	})
	assert.Equal(t, http.StatusOK, code, "%v", out)
}

// A probe that cannot answer must not block every upload — an unmeasurable
// disk is a reason to stay quiet, not to refuse the product.
func TestStagedUpload_DiskGuardIsSilentWhenItCannotMeasure(t *testing.T) {
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		area := staging.New(filepath.Join(d.Cfg.DataDir, "uploads"))
		area.FreeBytes = func(string) (uint64, error) { return 0, errors.New("statfs: nope") }
		d.Staging = area
	})
	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "unmeasured.bin", "size": 8 << 20, "chunk_size": 1 << 20,
	})
	assert.Equal(t, http.StatusOK, code, "%v", out)
}

// ── GC ──────────────────────────────────────────────────────────────────────

// Staging with no activity for longer than the TTL is swept, and the sweep is
// logged. An upload area with no GC is a disk incident waiting: this repo has
// already lost 29 GB to temp files nobody was cleaning up.
func TestStagedUpload_GCSweepsExpiredStaging(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out a TTL")
	}
	f := newStagedFixture(t)
	f.deps.StagedUploads.TTL = time.Second

	src := randomBytes(8192)
	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "abandoned.bin", "size": int64(len(src)), "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	stale := begun["id"].(string)
	code, _ = f.putChunk(t, stale, 0, 4096, int64(len(src)), src[:4096])
	require.Equal(t, http.StatusOK, code)

	staleDir := filepath.Join(f.dataDir, "uploads", stale)
	require.DirExists(t, staleDir)

	// SQLite's CURRENT_TIMESTAMP has one-second resolution, so the wait has to
	// clear a whole second past the TTL to be honest about what it proves.
	time.Sleep(2100 * time.Millisecond)

	// A second upload started now must survive the same sweep.
	code, fresh := f.begin(t, map[string]any{
		"path": "main://", "name": "in-flight.bin", "size": 4096, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", fresh)
	freshID := fresh["id"].(string)

	rows, orphans := f.deps.StagedUploads.Sweep(context.Background())
	assert.Equal(t, 1, rows, "exactly the abandoned upload should be swept")
	assert.Equal(t, 0, orphans)

	assert.NoDirExists(t, staleDir, "expired staging must be removed from disk")
	_, err := f.store.GetStagedUpload(context.Background(), stale)
	assert.Error(t, err, "expired session row must be removed")

	assert.DirExists(t, filepath.Join(f.dataDir, "uploads", freshID), "a live upload must survive the sweep")
	code, _ = f.status(t, freshID)
	assert.Equal(t, http.StatusOK, code)
}

// A staging directory with no row at all is debris from a crash between mkdir
// and the INSERT. It ages out by its own mtime.
func TestStagedUpload_GCSweepsOrphanDirectories(t *testing.T) {
	f := newStagedFixture(t)
	f.deps.StagedUploads.TTL = time.Hour

	area := staging.New(filepath.Join(f.dataDir, "uploads"))
	orphanID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	_, err := area.Create(orphanID, 100, 4096, "")
	require.NoError(t, err)
	orphanDir := filepath.Join(f.dataDir, "uploads", orphanID)
	require.DirExists(t, orphanDir)

	// Backdate the manifest — the file mtime is what "idle" means on disk.
	old := time.Now().Add(-24 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(orphanDir, staging.ManifestName), old, old))

	// A live upload with a row must not be touched by the orphan pass.
	code, live := f.begin(t, map[string]any{
		"path": "main://", "name": "live.bin", "size": 4096, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", live)

	rows, orphans := f.deps.StagedUploads.Sweep(context.Background())
	assert.Equal(t, 0, rows)
	assert.Equal(t, 1, orphans)
	assert.NoDirExists(t, orphanDir)
	assert.DirExists(t, filepath.Join(f.dataDir, "uploads", live["id"].(string)))
}

// ── re-chunking into a backend multipart upload ─────────────────────────────

// writerDriver is a plain storage.Driver + storage.Writer and NOTHING else:
// it does not implement multipart, which is exactly the situation this whole
// protocol exists for. Kept as its own type rather than "a part driver with
// the methods overridden" — a Go interface is satisfied by the method set, so
// an override would still satisfy PartUploader and the test would prove
// nothing.
type writerDriver struct {
	mu        sync.Mutex
	object    []byte
	written   bool
	failWrite bool
	// hold, when non-nil, blocks Write until the test closes it. Without a
	// gate, "is the node still staged right after commit?" is a race against
	// the worker that commits it — one this test lost on CI while passing
	// locally, which is the worst way for a test to be wrong.
	hold chan struct{}
}

func (d *writerDriver) Init(context.Context, map[string]any) error { return nil }
func (d *writerDriver) Name() string                               { return "fakewriter" }
func (d *writerDriver) List(context.Context, string) ([]storage.Object, error) {
	return nil, nil
}

func (d *writerDriver) Stat(_ context.Context, p string) (storage.Object, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.object == nil {
		return storage.Object{}, storage.ErrNotFound
	}
	return storage.Object{Path: p, Name: p, Size: int64(len(d.object)), Kind: storage.KindFile, Mtime: time.Now()}, nil
}

func (d *writerDriver) Read(_ context.Context, _ string) (io.ReadCloser, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return io.NopCloser(bytes.NewReader(d.object)), nil
}

func (d *writerDriver) Capabilities() storage.Capabilities {
	return storage.Capabilities{Write: true}
}

func (d *writerDriver) Write(_ context.Context, _ string, r io.Reader, _ int64) error {
	d.mu.Lock()
	failing, hold := d.failWrite, d.hold
	d.mu.Unlock()
	if hold != nil {
		<-hold
	}
	if failing {
		return errors.New("disk on fire")
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.written = true
	d.object = b
	return nil
}

// fakePartDriver is an S3-shaped backend: it implements storage.PartUploader
// and it enforces the rule that actually bites — every non-final part must be
// at least 5 MiB, and the complaint arrives at CompleteMultipartUpload, i.e.
// after all the bytes have been sent.
type fakePartDriver struct {
	writerDriver
	parts     map[int][]byte
	partOrder []int
	aborted   bool
}

func newFakePartDriver() *fakePartDriver {
	return &fakePartDriver{parts: map[int][]byte{}}
}

func (d *fakePartDriver) InitMultipart(_ context.Context, _ string, _ int64, _ int) (string, []string, error) {
	return "fake-upload-1", nil, nil
}

func (d *fakePartDriver) UploadPart(_ context.Context, _, uploadID string, n int, r io.Reader, size int64) (string, error) {
	if uploadID != "fake-upload-1" {
		return "", fmt.Errorf("unknown upload id %q", uploadID)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if int64(len(b)) != size {
		return "", fmt.Errorf("part %d: declared %d bytes, got %d", n, size, len(b))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.parts[n] = b
	d.partOrder = append(d.partOrder, n)
	return fmt.Sprintf("etag-%d", n), nil
}

func (d *fakePartDriver) CompleteMultipart(_ context.Context, _, _ string, parts []storage.PartCompletion) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		nums = append(nums, p.PartNumber)
	}
	sort.Ints(nums)
	var out []byte
	for i, n := range nums {
		b, ok := d.parts[n]
		if !ok {
			return fmt.Errorf("part %d never uploaded", n)
		}
		// This is S3's rule, and it is deliberately enforced here: a client
		// that staged 1 MiB chunks must not be able to break the backend.
		if i < len(nums)-1 && int64(len(b)) < storage.MinBackendPartSize {
			return fmt.Errorf("EntityTooSmall: part %d is %d bytes, minimum is %d",
				n, len(b), storage.MinBackendPartSize)
		}
		out = append(out, b...)
	}
	d.object = out
	return nil
}

func (d *fakePartDriver) AbortMultipart(context.Context, string, string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.aborted = true
	d.parts = map[int][]byte{}
	return nil
}

// Staging part boundaries belong to the client; backend part boundaries belong
// to the driver. A client sending 1 MiB chunks must produce ≥5 MiB backend
// parts, or S3 rejects the whole upload after every byte has been sent.
func TestStagedUpload_RechunksSmallClientChunksForMultipart(t *testing.T) {
	fake := newFakePartDriver()
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return fake, nil }
	})

	const clientChunk = 1 << 20 // 1 MiB — well under the backend's 5 MiB floor
	src := randomBytes(12 << 20)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "rechunk.bin", "size": total, "chunk_size": clientChunk,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	require.EqualValues(t, clientChunk, num(begun["chunk_size"]), "the client's chunk size is respected in staging")
	id := begun["id"].(string)

	for off := int64(0); off < total; off += clientChunk {
		end := off + clientChunk
		if end > total {
			end = total
		}
		code, put := f.putChunk(t, id, off, end-off, total, src[off:end])
		require.Equal(t, http.StatusOK, code, "%v", put)
	}

	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.False(t, fake.written, "a PartUploader driver must get a multipart upload, not a single PUT")
	assert.False(t, fake.aborted)
	require.Equal(t, 3, len(fake.parts), "12 MiB re-chunked at the 5 MiB floor is 5+5+2, not 12 parts of 1 MiB")
	assert.Len(t, fake.parts[1], storage.MinBackendPartSize)
	assert.Len(t, fake.parts[2], storage.MinBackendPartSize)
	assert.Len(t, fake.parts[3], int(total)-2*storage.MinBackendPartSize)
	assert.Equal(t, []int{1, 2, 3}, fake.partOrder, "parts go out in order")
	assert.Equal(t, sha256Hex(src), sha256Hex(fake.object), "the reassembled object must match the source")
}

// A driver without PartUploader takes the plain Writer path — that is what
// makes this protocol driver-agnostic in the first place.
func TestStagedUpload_PlainWriterDriverTakesTheWritePath(t *testing.T) {
	fake := &writerDriver{}
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return fake, nil }
	})
	src := randomBytes(9000)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "plain.bin", "size": total, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	for off := int64(0); off < total; off += 4096 {
		end := off + 4096
		if end > total {
			end = total
		}
		code, _ := f.putChunk(t, id, off, end-off, total, src[off:end])
		require.Equal(t, http.StatusOK, code)
	}
	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.True(t, fake.written, "a driver with no multipart support must still receive the bytes")
	assert.Equal(t, sha256Hex(src), sha256Hex(fake.object))
}

// ── the DB half of a successful commit ──────────────────────────────────────

// The row is deleted and the node moves staged → stored. (The HTTP half is
// TestStagedUpload_ResumeAfterInterruption_BytesMatch, which is deliberately
// kept free of post-change symbols so it can be run against the pre-change
// tree as red evidence.)
func TestStagedUpload_SuccessfulCommitClearsRowAndMarksStored(t *testing.T) {
	// ⚠ The driver is held shut on purpose. `staged` is a state the node
	// passes THROUGH, so sampling it after an async commit is a race: on a
	// loaded runner the worker wins, the node reads `stored`, and the test
	// fails while the code is right. Blocking the write makes the window
	// last as long as the assertion needs.
	gate := make(chan struct{})
	held := &writerDriver{hold: gate}
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return held, nil }
	})
	src := randomBytes(5000)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "stored.bin", "size": total, "chunk_size": 8192,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	code, _ = f.putChunk(t, id, 0, total, total, src)
	require.Equal(t, http.StatusOK, code)

	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	nodeID := num(committed["node_id"])

	staged, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	assert.Equal(t, model.TransferStateStaged, staged.TransferState,
		"the node is listed while its bytes are still in staging")

	close(gate)
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])))

	fresh, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	assert.Equal(t, model.TransferStateStored, fresh.TransferState)
	_, err = f.store.GetStagedUpload(context.Background(), id)
	assert.Error(t, err, "the session row is deleted once the bytes are stored")
}

// ── a failed transfer keeps its staging ─────────────────────────────────────

// On failure the op is `failed`, the staging directory is KEPT so a retry costs
// no bytes, and the node stays `staged`.
func TestStagedUpload_FailedTransferKeepsStagingForRetry(t *testing.T) {
	fake := &writerDriver{failWrite: true}
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return fake, nil }
	})
	src := randomBytes(6000)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "retry.bin", "size": total, "chunk_size": 8192,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	code, _ = f.putChunk(t, id, 0, total, total, src)
	require.Equal(t, http.StatusOK, code)

	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	nodeID := num(committed["node_id"])
	require.Equal(t, "failed", f.waitForOp(t, num(committed["op_id"])))

	assert.DirExists(t, filepath.Join(f.dataDir, "uploads", id), "staging must survive a failed transfer")
	row, err := f.store.GetStagedUpload(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, model.StagedUploadFailed, row.State)
	assert.Contains(t, row.Error, "disk on fire")

	node, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	assert.Equal(t, model.TransferStateFailed, node.TransferState,
		"a failed transfer says so; the staging is kept either way, so the retry below is still free")

	// Retry: same bytes, no re-upload, and this time the driver cooperates.
	fake.mu.Lock()
	fake.failWrite = false
	fake.mu.Unlock()

	code, retried := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "a failed upload must be committable again: %v", retried)
	require.Equal(t, "ok", f.waitForOp(t, num(retried["op_id"])))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Equal(t, sha256Hex(src), sha256Hex(fake.object))
	assert.NoDirExists(t, filepath.Join(f.dataDir, "uploads", id))
	fresh, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	assert.Equal(t, model.TransferStateStored, fresh.TransferState)
}

// ── hooks ───────────────────────────────────────────────────────────────────

// Every hook the synchronous path fires must still fire on the staged path:
// the writehook gate (antivirus enqueue + the canonical file.uploaded event)
// and the search index. A commit path that skips one is a silent regression
// across five features.
func TestStagedUpload_FiresEveryPostWriteHook(t *testing.T) {
	f := newStagedFixture(t)

	sink := &whFakeSink{ch: make(chan notify.Event, 8)}
	var (
		scanMu  sync.Mutex
		scanned []*model.Node
	)
	writehook.Configure(func(_ context.Context, n *model.Node) {
		scanMu.Lock()
		scanned = append(scanned, n)
		scanMu.Unlock()
	}, sink)
	t.Cleanup(func() { writehook.Configure(nil, nil) })

	src := []byte("staged bytes go through the same door")
	total := int64(len(src))
	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "hooked.txt", "size": total, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	code, _ = f.putChunk(t, id, 0, total, total, src)
	require.Equal(t, http.StatusOK, code)
	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])))

	e := sink.wait(t)
	assert.Equal(t, notify.EventFileUploaded, e.Event)
	require.NotNil(t, e.Node)
	assert.Equal(t, "/hooked.txt", e.Node.Path)
	assert.Equal(t, "hooked.txt", e.Node.Name)
	assert.Equal(t, writehook.OriginManager, e.Meta["origin"],
		"the staged path is the manager surface, same as vfUpload")
	assert.Equal(t, true, e.Meta["staged"])
	assert.EqualValues(t, total, e.Node.Size)

	scanMu.Lock()
	defer scanMu.Unlock()
	require.Len(t, scanned, 1, "one antivirus scan for one uploaded file")
	assert.NotZero(t, scanned[0].ID, "the scan must reference the persisted node")
	assert.Equal(t, "/hooked.txt", scanned[0].Path)

	// The mime came from the bytes, not from what the client claimed — the same
	// rule vfUpload applies.
	node, err := f.store.GetNode(context.Background(), num(committed["node_id"]))
	require.NoError(t, err)
	assert.Contains(t, node.Mime, "text/plain")
	assert.EqualValues(t, total, node.Size)
}
