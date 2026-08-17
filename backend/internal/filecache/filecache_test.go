package filecache_test

/* The cache, measured in bytes on disk and entries evicted.

   Every assertion here is about something that would be a real incident if it
   were wrong: bytes served that are not the file's bytes, a directory that
   grows past its ceiling, or a file deleted out from under a request that is
   streaming it. */

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/filecache"
)

// pattern is a body whose every window is recognisable, so an off-by-N range
// is a visibly wrong slice rather than plausible-looking bytes.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + (i % 26))
	}
	return b
}

func newCache(t *testing.T, cfg filecache.Config) *filecache.Cache {
	t.Helper()
	if cfg.Dir == "" {
		cfg.Dir = t.TempDir()
	}
	if cfg.PinTTL == 0 {
		// Tests assert on eviction, and the soft pin exists to stop it; a
		// long one would make every eviction case a timing puzzle.
		cfg.PinTTL = time.Millisecond
	}
	return filecache.New(cfg)
}

// fillNow runs a fetch to completion and fails the test if it does not land.
func fillNow(t *testing.T, c *filecache.Cache, key string, body []byte) {
	t.Helper()
	f, err := c.StartOrGet(context.Background(), key, int64(len(body)), func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	})
	require.NoError(t, err)
	require.NoError(t, f.Wait(context.Background()))
}

func readEntry(t *testing.T, e *filecache.Entry) []byte {
	t.Helper()
	rc, err := e.Reader()
	return readAll(t, rc, err)
}

func readRange(t *testing.T, e *filecache.Entry, off, length int64) []byte {
	t.Helper()
	rc, err := e.RangeReader(off, length)
	return readAll(t, rc, err)
}

func readAll(t *testing.T, rc io.ReadCloser, err error) []byte {
	t.Helper()
	require.NoError(t, err)
	defer rc.Close()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return b
}

// ── hit, miss, and the bytes ────────────────────────────────────────────────

// TestMiss_ThenHit — a cold key is a miss, a filled one is a hit, and the hit
// hands back exactly the bytes that went in.
func TestMiss_ThenHit(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	body := pattern(4096)
	key := filecache.Key(7, filecache.Signature("etag-a", int64(len(body)), time.Now()))

	_, ok := c.Acquire(key)
	require.False(t, ok, "a key that was never filled must be a miss")

	fillNow(t, c, key, body)

	e, ok := c.Acquire(key)
	require.True(t, ok, "a filled key must be a hit")
	require.Equal(t, int64(len(body)), e.Size())
	require.Equal(t, body, readEntry(t, e))
}

// TestSecondRequestDoesNotRefetch — the whole point. Once a copy exists the
// backend is not touched again, however many requests arrive.
func TestSecondRequestDoesNotRefetch(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	body := pattern(2048)
	key := filecache.Key(3, filecache.Signature("v1", 2048, time.Now()))

	fetches := 0
	open := func(context.Context) (io.ReadCloser, error) {
		fetches++
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	for i := 0; i < 5; i++ {
		f, err := c.StartOrGet(context.Background(), key, int64(len(body)), open)
		require.NoError(t, err)
		require.NoError(t, f.Wait(context.Background()))
	}
	require.Equal(t, 1, fetches, "the backend must be read once, not once per request")
}

// TestRangedReadsAreByteIdentical — every window a client can ask for, served
// from the cache, compared against the source bytes. This is the assertion
// that a cache which serves *plausible* bytes would fail.
func TestRangedReadsAreByteIdentical(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	body := pattern(10000)
	key := filecache.Key(11, filecache.Signature("v1", 10000, time.Now()))
	fillNow(t, c, key, body)

	cases := []struct{ off, length int64 }{
		{0, 100}, {100, 100}, {9900, 100}, {0, -1}, {5000, -1}, {9999, 1},
	}
	for _, tc := range cases {
		e, ok := c.Acquire(key)
		require.True(t, ok)
		got := readRange(t, e, tc.off, tc.length)
		want := body[tc.off:]
		if tc.length >= 0 && tc.length < int64(len(want)) {
			want = want[:tc.length]
		}
		require.Equal(t, want, got, "window off=%d len=%d", tc.off, tc.length)
	}
}

// TestRangeContract_Edges — the three edges storage.RangeReader specifies,
// because the cache is substituted for a driver and must answer the same way.
func TestRangeContract_Edges(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	body := pattern(500)
	key := filecache.Key(12, filecache.Signature("v1", 500, time.Now()))
	fillNow(t, c, key, body)

	e, _ := c.Acquire(key)
	require.Empty(t, readRange(t, e, 0, 0), "length 0 is an immediate EOF")

	e, _ = c.Acquire(key)
	require.Empty(t, readRange(t, e, 500, -1), "an offset at EOF is EOF, not an error")

	e, _ = c.Acquire(key)
	require.Equal(t, body[400:], readRange(t, e, 400, 9999), "a length past EOF is clamped")

	e, _ = c.Acquire(key)
	_, err := e.RangeReader(-1, 10)
	require.Error(t, err, "a negative offset is an error")
}

// ── invalidation ────────────────────────────────────────────────────────────

// TestEtagChangeInvalidates — the file changed on the backend, so the key
// changed, so the old copy cannot be served. This is what keys the cache on
// content rather than on a path.
func TestEtagChangeInvalidates(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	mtime := time.Now()
	v1, v2 := []byte("version one"), []byte("version two")

	k1 := filecache.Key(9, filecache.Signature("etag-1", int64(len(v1)), mtime))
	k2 := filecache.Key(9, filecache.Signature("etag-2", int64(len(v2)), mtime))
	require.NotEqual(t, k1, k2, "a changed ETag must produce a different key")

	fillNow(t, c, k1, v1)
	_, ok := c.Acquire(k2)
	require.False(t, ok, "the new content must not be answered from the old copy")

	fillNow(t, c, k2, v2)
	e, _ := c.Acquire(k2)
	require.Equal(t, v2, readEntry(t, e))
}

// TestSignatureFallsBackToSizeAndMtime — the local driver reports no ETag at
// all, and it is the driver an NFS/SMB mount uses. A signature scheme that
// needed an ETag would never cache the case this package exists for.
func TestSignatureFallsBackToSizeAndMtime(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	require.NotEmpty(t, filecache.Signature("", 1234, at))
	require.NotEqual(t,
		filecache.Signature("", 1234, at),
		filecache.Signature("", 1235, at), "a changed size must change the signature")
	require.NotEqual(t,
		filecache.Signature("", 1234, at),
		filecache.Signature("", 1234, at.Add(time.Second)), "a changed mtime must change the signature")
	require.Empty(t, filecache.Key(0, "sig"), "no node row means no key")
	require.Empty(t, filecache.Key(5, ""), "no signature means no key")
}

// TestSizeChangeDuringFetchIsRejected — the object grew or shrank while it was
// being copied, so the copy does not match the signature the key promises.
// Keeping it would serve a truncated or padded body through http.ServeContent,
// which trusts the size it is given.
func TestSizeChangeDuringFetchIsRejected(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	key := filecache.Key(21, filecache.Signature("v1", 1000, time.Now()))

	f, err := c.StartOrGet(context.Background(), key, 1000, func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(pattern(600))), nil
	})
	require.NoError(t, err)
	require.ErrorIs(t, f.Wait(context.Background()), filecache.ErrSizeChanged)

	_, ok := c.Acquire(key)
	require.False(t, ok, "a short copy must not become a cache entry")
	require.Zero(t, c.Bytes(), "and it must not keep charging the cap for bytes it discarded")
}

// ── the global cap ──────────────────────────────────────────────────────────

// TestGlobalCapEvictsUnderPressure is the assertion the sharezip cache would
// fail: filling past the ceiling evicts, and the directory never exceeds it.
//
// sharezip prunes per node, which bounds nothing — N nodes cost N archives.
// This repo has already filled a host's disk once this month.
func TestGlobalCapEvictsUnderPressure(t *testing.T) {
	dir := t.TempDir()
	c := newCache(t, filecache.Config{Dir: dir, MinSize: 1, MaxBytes: 3000})

	for i := 1; i <= 6; i++ {
		key := filecache.Key(int64(i), filecache.Signature(fmt.Sprintf("v%d", i), 1000, time.Now()))
		fillNow(t, c, key, pattern(1000))
		require.LessOrEqual(t, c.Bytes(), int64(3000), "the cap must hold after entry %d", i)
		require.LessOrEqual(t, dirBytes(t, dir), int64(3000), "and it must hold ON DISK, not just in the counter")
	}
	require.Equal(t, 3, c.Len(), "a 3000-byte cap holds three 1000-byte files")
}

// TestEvictionIsLRU — the entry nobody has touched goes first.
func TestEvictionIsLRU(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, MaxBytes: 2000})
	k1 := filecache.Key(1, "a")
	k2 := filecache.Key(2, "b")
	fillNow(t, c, k1, pattern(1000))
	time.Sleep(5 * time.Millisecond)
	fillNow(t, c, k2, pattern(1000))

	// Touch the OLDER one, so the younger is now the least recently used.
	time.Sleep(5 * time.Millisecond)
	e, ok := c.Acquire(k1)
	require.True(t, ok)
	e.Release()
	time.Sleep(5 * time.Millisecond) // let the soft pins expire (PinTTL=1ms)

	fillNow(t, c, filecache.Key(3, "c"), pattern(1000))

	_, ok = c.Acquire(k1)
	require.True(t, ok, "the recently used entry must survive")
	_, ok = c.Acquire(k2)
	require.False(t, ok, "the least recently used entry is the one that goes")
}

// TestEvictionRefusesAnEntryInUse — a request streaming from a cached file
// must not have it deleted underneath. The pin is held by the open body, so
// the cache would rather refuse to cache a new file than break a live one.
func TestEvictionRefusesAnEntryInUse(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, MaxBytes: 1000})
	held := filecache.Key(1, "held")
	fillNow(t, c, held, pattern(1000))

	e, ok := c.Acquire(held)
	require.True(t, ok)
	rc, err := e.Reader()
	require.NoError(t, err)
	defer rc.Close()

	// The cache is full and its only entry is being read. A new fill must be
	// refused, NOT satisfied by evicting the file under the open reader.
	_, err = c.StartOrGet(context.Background(), filecache.Key(2, "new"), 1000,
		func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(pattern(1000))), nil })
	require.ErrorIs(t, err, filecache.ErrNoRoom)

	// And the in-use entry still reads correctly, from byte one to the last.
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, pattern(1000), got)

	// Once the reader closes, the same fill succeeds.
	require.NoError(t, rc.Close())
	time.Sleep(5 * time.Millisecond) // PinTTL
	f, err := c.StartOrGet(context.Background(), filecache.Key(2, "new"), 1000,
		func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(pattern(1000))), nil })
	require.NoError(t, err)
	require.NoError(t, f.Wait(context.Background()))
}

// TestCapCountsFillsThatAreStillBeingWritten — the accounting hole a naive
// implementation has: bytes are only charged when a file lands, so ten
// concurrent fills of 1 GB each pass a 20 GB cap check ten times over and then
// all land. Here the declared size is reserved the moment the fill starts.
func TestCapCountsFillsThatAreStillBeingWritten(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, MaxBytes: 2000})

	release := make(chan struct{})
	slow := func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(&blockingReader{data: pattern(1000), gate: release}), nil
	}
	f1, err := c.StartOrGet(context.Background(), filecache.Key(1, "a"), 1000, slow)
	require.NoError(t, err)
	f2, err := c.StartOrGet(context.Background(), filecache.Key(2, "b"), 1000, slow)
	require.NoError(t, err)

	require.Equal(t, int64(2000), c.Bytes(), "two in-flight fills must already occupy the cap")

	_, err = c.StartOrGet(context.Background(), filecache.Key(3, "c"), 1000, slow)
	require.ErrorIs(t, err, filecache.ErrNoRoom, "a third fill must not be admitted over the cap")

	close(release)
	require.NoError(t, f1.Wait(context.Background()))
	require.NoError(t, f2.Wait(context.Background()))
}

// TestFileLargerThanTheCapIsRefused — no amount of eviction makes room, and
// pretending otherwise would blow the ceiling by design.
func TestFileLargerThanTheCapIsRefused(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, MaxBytes: 1000})
	_, err := c.StartOrGet(context.Background(), filecache.Key(1, "big"), 5000,
		func(context.Context) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(pattern(5000))), nil })
	require.ErrorIs(t, err, filecache.ErrNoRoom)
}

// ── the fill itself ─────────────────────────────────────────────────────────

// TestPercentMovesAndStopsBelow100 — progress is real, and 100 is reserved for
// a file that is actually on disk so a poller never sees "done" too early.
func TestPercentMovesAndStopsBelow100(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	gate := make(chan struct{})
	body := pattern(100000)

	f, err := c.StartOrGet(context.Background(), filecache.Key(1, "p"), int64(len(body)),
		func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(&halfThenBlockReader{data: body, gate: gate}), nil
		})
	require.NoError(t, err)

	require.Eventually(t, func() bool { return f.Percent() >= 40 && f.Percent() <= 99 },
		2*time.Second, 5*time.Millisecond, "percent must move while the fetch runs")
	require.False(t, f.Done())

	close(gate)
	require.NoError(t, f.Wait(context.Background()))
	require.True(t, f.Done())
}

// TestConcurrentRequestsShareOneFetch — five simultaneous downloads of the
// same cold file must cost one transfer, not five.
func TestConcurrentRequestsShareOneFetch(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	key := filecache.Key(1, "shared")
	body := pattern(50000)

	var mu sync.Mutex
	fetches := 0
	open := func(context.Context) (io.ReadCloser, error) {
		mu.Lock()
		fetches++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		return io.NopCloser(bytes.NewReader(body)), nil
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := c.StartOrGet(context.Background(), key, int64(len(body)), open)
			if err == nil {
				_ = f.Wait(context.Background())
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 1, fetches)

	e, ok := c.Acquire(key)
	require.True(t, ok)
	require.Equal(t, body, readEntry(t, e))
}

// TestFetchSurvivesTheCallerHangingUp — the trap this repo has already paid
// for once (v0.18.0, the share download counter): work that outlives the
// response must not run on the request context. Several requests wait on one
// fetch, so the first client pressing Escape cannot be allowed to cancel it.
func TestFetchSurvivesTheCallerHangingUp(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	key := filecache.Key(1, "hangup")
	body := pattern(20000)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	f, err := c.StartOrGet(ctx, key, int64(len(body)), func(fctx context.Context) (io.ReadCloser, error) {
		close(started)
		<-time.After(30 * time.Millisecond)
		if err := fctx.Err(); err != nil {
			return nil, err // the fetch saw the caller's cancellation: wrong
		}
		return io.NopCloser(bytes.NewReader(body)), nil
	})
	require.NoError(t, err)

	<-started
	cancel() // the client hung up

	require.NoError(t, f.Wait(context.Background()), "a disconnect must not kill the fetch")
	e, ok := c.Acquire(key)
	require.True(t, ok)
	require.Equal(t, body, readEntry(t, e))
}

// TestFailedFetchLeavesNothingBehind — no half file, no charged bytes, and a
// cooldown so a broken object is not re-fetched once per click.
func TestFailedFetchLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	c := newCache(t, filecache.Config{Dir: dir, MinSize: 1})
	key := filecache.Key(1, "broken")
	boom := errors.New("backend on fire")

	attempts := 0
	open := func(context.Context) (io.ReadCloser, error) { attempts++; return nil, boom }

	f, err := c.StartOrGet(context.Background(), key, 1000, open)
	require.NoError(t, err)
	require.ErrorIs(t, f.Wait(context.Background()), boom)

	require.Zero(t, c.Bytes())
	require.Zero(t, c.Len())
	require.Zero(t, dirBytes(t, dir), "no temp file may survive a failed fetch")

	_, err = c.StartOrGet(context.Background(), key, 1000, open)
	require.ErrorIs(t, err, filecache.ErrNoRoom, "a failing key is on cooldown, not retried per request")
	require.Equal(t, 1, attempts)
}

// ── restarts ────────────────────────────────────────────────────────────────

// TestExistingDirectoryIsAdoptedAndSwept — a restart keeps the prepared
// copies (the key carries the signature, so they are still valid) and removes
// the temp file an interrupted fill left behind.
func TestExistingDirectoryIsAdoptedAndSwept(t *testing.T) {
	dir := t.TempDir()
	c := newCache(t, filecache.Config{Dir: dir, MinSize: 1, MaxBytes: 10000})
	key := filecache.Key(1, "kept")
	body := pattern(1000)
	fillNow(t, c, key, body)

	stray := filepath.Join(dir, ".tmp-interrupted")
	require.NoError(t, os.WriteFile(stray, pattern(4000), 0o644))

	restarted := filecache.New(filecache.Config{Dir: dir, MinSize: 1, MaxBytes: 10000})
	require.Equal(t, 1, restarted.Len())
	require.Equal(t, int64(1000), restarted.Bytes(), "an interrupted fill must not be charged to the cap")
	require.NoFileExists(t, stray)

	e, ok := restarted.Acquire(key)
	require.True(t, ok)
	require.Equal(t, body, readEntry(t, e))
}

// TestDisabledCacheIsInert — no directory, no behaviour. Every entry point
// answers "not cached" rather than panicking or writing somewhere unexpected.
func TestDisabledCacheIsInert(t *testing.T) {
	c := filecache.New(filecache.Config{})
	require.False(t, c.Enabled())
	_, ok := c.Acquire("1-abc")
	require.False(t, ok)
	require.False(t, c.Ready("1-abc"))
	_, err := c.StartOrGet(context.Background(), "1-abc", 10,
		func(context.Context) (io.ReadCloser, error) { return nil, nil })
	require.ErrorIs(t, err, filecache.ErrNoRoom)
	require.False(t, c.Qualifies(1, 1<<30, true))
}

// ── helpers ─────────────────────────────────────────────────────────────────

func dirBytes(t *testing.T, dir string) int64 {
	t.Helper()
	ents, err := os.ReadDir(dir)
	require.NoError(t, err)
	var total int64
	for _, e := range ents {
		fi, err := e.Info()
		require.NoError(t, err)
		total += fi.Size()
	}
	return total
}

// blockingReader yields nothing until its gate is closed, then the whole body.
type blockingReader struct {
	data []byte
	gate chan struct{}
	off  int
	once bool
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if !b.once {
		<-b.gate
		b.once = true
	}
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

// halfThenBlockReader delivers half the body, then waits — the shape of a
// transfer caught mid-flight, which is what a percentage is for.
type halfThenBlockReader struct {
	data []byte
	gate chan struct{}
	off  int
	held bool
}

func (h *halfThenBlockReader) Read(p []byte) (int, error) {
	if h.off >= len(h.data)/2 && !h.held {
		<-h.gate
		h.held = true
	}
	if h.off >= len(h.data) {
		return 0, io.EOF
	}
	end := h.off + 1024
	if end > len(h.data) {
		end = len(h.data)
	}
	n := copy(p, h.data[h.off:end])
	h.off += n
	return n, nil
}
