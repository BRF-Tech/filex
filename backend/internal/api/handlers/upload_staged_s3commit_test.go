package handlers_test

// Issue #16 — "Files uploaded to s3 storage go to trash bin".
//
// A fresh Garage (S3) deployment: every upload over the client's 8 MiB chunk
// threshold was accepted, acknowledged and listed, never reached the bucket,
// and was then moved to trash by the next storage sync. Files under the
// threshold were fine.
//
// The size split was the whole tell. Under 8 MiB the browser posts the file in
// one request and the commit hands the staging reader — which can seek —
// straight to the driver's single-shot Write. Over 8 MiB it takes the staged
// path, and the commit re-chunks into a driver multipart upload where each part
// used to be cut with io.LimitReader. That wrapper drops the Seek method, and
// SigV4 signs the SHA256 of the payload: the AWS SDK reads a part to hash it
// and then rewinds to send it. With nothing to rewind it failed the request
// before a byte left the process —
//
//	upload part 1: operation error S3: UploadPart, failed to compute payload
//	hash: failed to seek body to start, request stream is not seekable
//
// ⚠ The existing multipart test did not catch this because its fake driver
// reads each part exactly once, with io.ReadAll. Every real S3 SDK reads it
// twice. So the fake below is the SDK's shape, not a convenient one, and that
// difference is the entire regression.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// hashingPartDriver is an S3-shaped backend that signs its parts the way the
// AWS SDK does: read the whole body to hash it, rewind, read it again to send
// it. A body it cannot rewind is refused with the SDK's own words, so this test
// fails against the LimitReader version of streamMultipart for the same reason
// Garage did.
type hashingPartDriver struct {
	writerDriver
	parts     map[int][]byte
	partOrder []int
	hashes    map[int]string
	aborted   bool
}

func newHashingPartDriver() *hashingPartDriver {
	return &hashingPartDriver{parts: map[int][]byte{}, hashes: map[int]string{}}
}

func (d *hashingPartDriver) InitMultipart(context.Context, string, int64, int) (string, []string, error) {
	return "sdk-upload-1", nil, nil
}

func (d *hashingPartDriver) UploadPart(_ context.Context, _, uploadID string, n int, r io.Reader, size int64) (string, error) {
	if uploadID != "sdk-upload-1" {
		return "", fmt.Errorf("unknown upload id %q", uploadID)
	}
	// Pass 1: hash the payload, exactly as the SigV4 signer does.
	seeker, ok := r.(io.Seeker)
	if !ok {
		return "", fmt.Errorf("failed to compute payload hash: failed to seek body to start, request stream is not seekable")
	}
	sum := sha256.New()
	hashed, err := io.Copy(sum, r)
	if err != nil {
		return "", err
	}
	// Pass 2: rewind and send.
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("failed to seek body to start, %w", err)
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	if int64(len(b)) != size || hashed != size {
		return "", fmt.Errorf("part %d: declared %d bytes, hashed %d, sent %d", n, size, hashed, len(b))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.parts[n] = b
	d.hashes[n] = fmt.Sprintf("%x", sum.Sum(nil))
	d.partOrder = append(d.partOrder, n)
	return fmt.Sprintf("etag-%d", n), nil
}

func (d *hashingPartDriver) CompleteMultipart(_ context.Context, _, _ string, parts []storage.PartCompletion) error {
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
		if i < len(nums)-1 && int64(len(b)) < storage.MinBackendPartSize {
			return fmt.Errorf("EntityTooSmall: part %d is %d bytes, minimum is %d",
				n, len(b), storage.MinBackendPartSize)
		}
		// The signer hashed what it sent — anything else means the two passes
		// read different bytes, which is worse than a failed upload.
		if got := fmt.Sprintf("%x", sha256.Sum256(b)); got != d.hashes[n] {
			return fmt.Errorf("part %d: signed %s but stored %s", n, d.hashes[n], got)
		}
		out = append(out, b...)
	}
	d.object = out
	return nil
}

func (d *hashingPartDriver) AbortMultipart(context.Context, string, string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.aborted = true
	d.parts = map[int][]byte{}
	return nil
}

// The headline regression: a staged upload above the chunk threshold reaches an
// S3-shaped backend whose signer has to read every part twice.
func TestStagedUpload_PartBodiesSurviveASigningRewind(t *testing.T) {
	fake := newHashingPartDriver()
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return fake, nil }
	})

	const clientChunk = 8 << 20 // what the browser uses
	src := randomBytes(12 << 20)
	total := int64(len(src))

	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "sled.mp3", "size": total, "chunk_size": clientChunk,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	for off := int64(0); off < total; off += clientChunk {
		end := min(off+clientChunk, total)
		code, put := f.putChunk(t, id, off, end-off, total, src[off:end])
		require.Equal(t, http.StatusOK, code, "%v", put)
	}

	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])),
		"the commit must not fail: every part body has to be rewindable for a SigV4 signer")

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.False(t, fake.aborted)
	require.Len(t, fake.parts, 3, "12 MiB re-chunked at the 5 MiB floor is 5+5+2")
	assert.Equal(t, []int{1, 2, 3}, fake.partOrder)
	assert.Equal(t, sha256Hex(src), sha256Hex(fake.object),
		"reading each part twice must yield the same bytes both times")
}

// A driver that reads a part only once still works: hardening the body for
// signers must not require every driver to become one.
func TestStagedUpload_SinglePassPartDriverStillWorks(t *testing.T) {
	fake := newFakePartDriver()
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return fake, nil }
	})
	src := randomBytes(6 << 20)
	total := int64(len(src))
	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "single-pass.bin", "size": total, "chunk_size": 1 << 20,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	for off := int64(0); off < total; off += 1 << 20 {
		end := min(off+(1<<20), total)
		code, _ = f.putChunk(t, id, off, end-off, total, src[off:end])
		require.Equal(t, http.StatusOK, code)
	}
	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])))

	fake.mu.Lock()
	defer fake.mu.Unlock()
	assert.Equal(t, sha256Hex(src), sha256Hex(fake.object))
}

// ── the failure has to be audible ───────────────────────────────────────────

// failingPartDriver refuses every part, the way Garage's client did.
type failingPartDriver struct {
	writerDriver
	err error
}

func (d *failingPartDriver) InitMultipart(context.Context, string, int64, int) (string, []string, error) {
	return "doomed-1", nil, nil
}

func (d *failingPartDriver) UploadPart(context.Context, string, string, int, io.Reader, int64) (string, error) {
	return "", d.err
}

func (d *failingPartDriver) CompleteMultipart(context.Context, string, string, []storage.PartCompletion) error {
	return errors.New("never reached")
}

func (d *failingPartDriver) AbortMultipart(context.Context, string, string) error { return nil }

// ⚠ The client is answered 202 the moment the last chunk lands, so by the time
// the transfer fails the browser has already drawn a finished upload. Before
// this, a failed transfer produced one WARN line in the server log and nothing
// else: the node stayed at "staged" — indistinguishable from still in flight —
// and no notification fired. The user's only clue was that the file would not
// download.
func TestStagedUpload_FailedTransferIsVisibleToTheUser(t *testing.T) {
	fake := &failingPartDriver{err: errors.New(
		"operation error S3: UploadPart, failed to compute payload hash: failed to seek body to start, request stream is not seekable")}
	f := newStagedFixtureWith(t, func(d *api.Deps) {
		d.StorageResolver = func(int64) (storage.Driver, error) { return fake, nil }
	})
	sink := &whFakeSink{ch: make(chan notify.Event, 8)}
	writehook.Configure(nil, sink)
	t.Cleanup(func() { writehook.Configure(nil, nil) })

	src := randomBytes(6 << 20)
	total := int64(len(src))
	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "doomed.mp3", "size": total, "chunk_size": 1 << 20,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	id := begun["id"].(string)
	for off := int64(0); off < total; off += 1 << 20 {
		end := min(off+(1<<20), total)
		code, _ = f.putChunk(t, id, off, end-off, total, src[off:end])
		require.Equal(t, http.StatusOK, code)
	}
	code, committed := f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", committed)

	// 1. The op ends as failed, so the client polling it learns the truth.
	assert.Equal(t, "failed", f.waitForOp(t, num(committed["op_id"])))

	// 2. The node says the bytes are NOT on the storage, rather than sitting at
	//    "staged" for ever.
	nodeID := num(committed["node_id"])
	require.NotZero(t, nodeID)
	fresh, err := f.store.GetNode(context.Background(), nodeID)
	require.NoError(t, err)
	assert.Equal(t, model.TransferStateFailed, fresh.TransferState)
	assert.NotEqual(t, model.TransferStateStaged, fresh.TransferState,
		"a dead upload must not be indistinguishable from one still in flight")

	// 3. The user is told.
	e := sink.wait(t)
	assert.Equal(t, notify.EventFileUploadFailed, e.Event)
	assert.Equal(t, notify.SeverityError, e.Severity)
	require.NotNil(t, e.Node)
	assert.Equal(t, "doomed.mp3", e.Node.Name)
	assert.Contains(t, fmt.Sprint(e.Meta["reason"]), "not seekable",
		"the notification must carry why, not just that")
	require.NotNil(t, e.UserID, "the bell entry belongs to whoever uploaded it")
}
