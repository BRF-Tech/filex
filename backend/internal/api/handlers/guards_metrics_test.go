package handlers_test

// Chunk 7 — the guards, the staging GC and the metrics surface.
//
// The disk guard's threshold and the sweeper's two cases (an expired session,
// an orphan directory with no row) are already pinned by
// upload_staged_guard_test.go, which chunk 4 wrote; those are verified rather
// than duplicated here. What this file adds is everything chunk 7 introduced:
//
//   - a guard that fires has to be VISIBLE — a counter and a log line, not
//     just a status code the client swallows;
//   - the sweeper has to log EVERY pass, including the quiet ones. A sweeper
//     that only speaks when it deletes something is indistinguishable from a
//     sweeper that has stopped running, and this repo has already lost 29 GB
//     to temp files nobody was watching (v0.13.4);
//   - /metrics has to exist, be admin-only, and MOVE.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// captureLogs redirects the default slog handler into a buffer for the
// duration of the test and returns a reader for what was written.
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var mu sync.Mutex
	var sb strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{mu: &mu, w: &sb}, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return sb.String()
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func counter(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	return promtestutil.ToFloat64(c)
}

// ── the guards are visible ──────────────────────────────────────────────────

// A refusal the operator cannot see is a support ticket. The disk guard has to
// bump its counter and say, in the log AND in the response, how much room it
// wanted and how much there was — those are the two numbers that decide
// whether to free space or to move FILEX_UPLOAD_STAGING_DIR.
func TestDiskGuard_IsCountedAndLogged(t *testing.T) {
	logs := captureLogs(t)
	before := counter(t, metrics.GuardRefusals.WithLabelValues(metrics.GuardDisk))

	f := newStagedFixtureWith(t, func(d *api.Deps) {
		area := staging.New(filepath.Join(d.Cfg.DataDir, "uploads"))
		area.FreeBytes = func(string) (uint64, error) { return 6 << 20, nil }
		d.Staging = area
	})

	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "huge.bin", "size": 8 << 20,
	})
	require.Equal(t, http.StatusInsufficientStorage, code, "%v", out)
	assert.Equal(t, "NO_DISK_SPACE", out["code"])

	msg := fmt.Sprint(out["error"])
	assert.Contains(t, msg, "8388608", "the message must name the upload that was refused")
	assert.Contains(t, msg, "6291456", "and how much was actually free")

	assert.Equal(t, before+1, counter(t, metrics.GuardRefusals.WithLabelValues(metrics.GuardDisk)))
	assert.Contains(t, logs(), "staged upload refused: staging disk")
}

// The quota ceiling is a guard too, and gets the same treatment. This also
// pins the reservation itself: on 6485c16 nothing was ever counted, so this
// refusal could not happen at all.
func TestQuotaGuard_IsCountedAndLogged(t *testing.T) {
	logs := captureLogs(t)
	before := counter(t, metrics.GuardRefusals.WithLabelValues(metrics.GuardQuota))

	f := newStagedFixture(t)
	require.NoError(t, f.deps.Quota.SetQuota(context.Background(), f.userID, 4096))

	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "over.bin", "size": 8192, "chunk_size": 4096,
	})
	require.Equal(t, http.StatusRequestEntityTooLarge, code, "%v", out)
	assert.Equal(t, "QUOTA_EXCEEDED", out["code"])

	assert.Equal(t, before+1, counter(t, metrics.GuardRefusals.WithLabelValues(metrics.GuardQuota)))
	assert.Contains(t, logs(), "staged upload refused: quota")
}

// And the ceiling has to hold across SEPARATE uploads, not just within one:
// the bytes already stored are what the next upload is measured against. This
// is the whole point of the accounting — a user under the limit today must be
// over it tomorrow if they keep writing.
func TestQuotaGuard_CountsWhatIsAlreadyStored(t *testing.T) {
	f := newStagedFixture(t)
	require.NoError(t, f.deps.Quota.SetQuota(context.Background(), f.userID, 6000))

	f.uploadMultipart(t, "first.bin", make([]byte, 5000))
	require.EqualValues(t, 5000, f.usage(t))

	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "second.bin", "size": 2000, "chunk_size": 4096,
	})
	assert.Equal(t, http.StatusRequestEntityTooLarge, code,
		"5000 stored + 2000 asked > 6000 allowed — on 6485c16 usage was 0 and this was accepted: %v", out)
}

// ── the GC is visible ───────────────────────────────────────────────────────

// Every pass logs, and the counters move — including the pass that found
// nothing, which is the one that proves the sweeper is alive.
func TestStagingGC_LogsEveryPassAndMovesTheCounters(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out a TTL")
	}
	logs := captureLogs(t)
	sweepsBefore := counter(t, metrics.SweepRuns)
	rowsBefore := counter(t, metrics.Swept.WithLabelValues("row"))
	orphansBefore := counter(t, metrics.Swept.WithLabelValues("orphan"))

	f := newStagedFixture(t)
	f.deps.StagedUploads.TTL = time.Second

	// (a) an expired session with a row…
	src := randomBytes(8192)
	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "abandoned.bin", "size": int64(len(src)), "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	staleID := begun["id"].(string)
	code, _ = f.putChunk(t, staleID, 0, 4096, int64(len(src)), src[:4096])
	require.Equal(t, http.StatusOK, code)

	// (b) …and a directory with no row at all, backdated on disk.
	area := staging.New(filepath.Join(f.dataDir, "uploads"))
	orphanID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	_, err := area.Create(orphanID, 100, 4096, "")
	require.NoError(t, err)
	old := time.Now().Add(-24 * time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(f.dataDir, "uploads", orphanID, staging.ManifestName), old, old))

	// SQLite's CURRENT_TIMESTAMP has one-second resolution, so clearing a whole
	// second past the TTL is what makes the assertion honest. (The comparison
	// itself is bound as `YYYY-MM-DD HH:MM:SS` rather than RFC3339 — a raw
	// time.Time would sort ' ' before 'T' and call every row of the current
	// second idle, sweeping uploads that are in flight right now.)
	time.Sleep(2100 * time.Millisecond)

	rows, orphans := f.deps.StagedUploads.Sweep(context.Background())
	require.Equal(t, 1, rows)
	require.Equal(t, 1, orphans)

	assert.Equal(t, sweepsBefore+1, counter(t, metrics.SweepRuns))
	assert.Equal(t, rowsBefore+1, counter(t, metrics.Swept.WithLabelValues("row")))
	assert.Equal(t, orphansBefore+1, counter(t, metrics.Swept.WithLabelValues("orphan")))

	out := logs()
	assert.Contains(t, out, "staged upload swept", "the removed session must be named in the log")
	assert.Contains(t, out, "staged upload swept orphan directory")
	assert.Contains(t, out, "rows_removed=1")

	// A pass that removes nothing still speaks.
	f.deps.StagedUploads.Sweep(context.Background())
	assert.Equal(t, sweepsBefore+2, counter(t, metrics.SweepRuns))
	assert.Contains(t, logs(), "rows_removed=0",
		"a silent sweeper is indistinguishable from a stopped one")
}

// The staged-bytes gauge is moved by events for freshness and RECONCILED
// against the disk on every sweep — so a restart, or a crash that left parts
// behind, cannot leave the dashboard lying.
func TestStagingGC_ReconcilesTheGaugesAgainstTheDisk(t *testing.T) {
	f := newStagedFixture(t)
	f.deps.StagedUploads.TTL = time.Hour

	src := randomBytes(8192)
	code, begun := f.begin(t, map[string]any{
		"path": "main://", "name": "live.bin", "size": int64(len(src)), "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", begun)
	code, _ = f.putChunk(t, begun["id"].(string), 0, 4096, int64(len(src)), src[:4096])
	require.Equal(t, http.StatusOK, code)

	// Pretend the process restarted and the gauges were lost.
	metrics.StagedBytes.Set(0)
	metrics.StagedInFlight.Set(0)
	f.deps.StagedUploads.Sweep(context.Background())

	assert.EqualValues(t, 4096, promtestutil.ToFloat64(metrics.StagedBytes),
		"the sweep must re-measure the staging directory, not trust a counter")
	assert.EqualValues(t, 1, promtestutil.ToFloat64(metrics.StagedInFlight))
}

// ── /metrics ────────────────────────────────────────────────────────────────

// filex is routinely on the public internet and its exposition names storages,
// counts users and shows traffic shape, so /metrics sits behind the same admin
// gate as every other operator endpoint.
func TestMetrics_IsAdminOnly(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)

	resp, err := (&http.Client{}).Get(srv.URL + "/metrics")
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, resp.StatusCode,
		"an open /metrics leaks storage names, user counts and traffic shape")

	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	resp, err = client.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), "filex_staged_uploads_begun_total")
	assert.Contains(t, string(body), "filex_quota_usage_bytes")
	assert.Contains(t, string(body), "filex_guard_refusals_total")
	assert.Contains(t, string(body), "go_goroutines", "the runtime metrics come along for free")
}

// Present is not the same as working. An upload has to move the numbers.
func TestMetrics_MoveOnAnUpload(t *testing.T) {
	begunBefore := counter(t, metrics.StagedBegun)
	committedBefore := counter(t, metrics.StagedCommitted)
	stagedBefore := counter(t, metrics.StagedBytesStaged)

	f := newStagedFixture(t)
	body := randomBytes(6000)
	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "measured.bin", "size": len(body), "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", out)
	id := out["id"].(string)
	code, _ = f.putChunk(t, id, 0, 4096, int64(len(body)), body[:4096])
	require.Equal(t, http.StatusOK, code)
	code, _ = f.putChunk(t, id, 4096, int64(len(body)-4096), int64(len(body)), body[4096:])
	require.Equal(t, http.StatusOK, code)
	code, out = f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	require.Equal(t, "ok", f.waitForOp(t, int64(out["op_id"].(float64))))

	assert.Equal(t, begunBefore+1, counter(t, metrics.StagedBegun))
	assert.Equal(t, committedBefore+1, counter(t, metrics.StagedCommitted))
	assert.Equal(t, stagedBefore+float64(len(body)), counter(t, metrics.StagedBytesStaged))
}

// A re-sent chunk is a retry, not a failure. Counting them together would make
// a flaky link look like a broken server.
func TestMetrics_ResentChunkCountsAsARetry(t *testing.T) {
	retriesBefore := counter(t, metrics.StagedChunkRetries)

	f := newStagedFixture(t)
	body := randomBytes(4096)
	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "resent.bin", "size": len(body), "chunk_size": 4096,
	})
	require.Equal(t, http.StatusOK, code, "%v", out)
	id := out["id"].(string)

	code, _ = f.putChunk(t, id, 0, int64(len(body)), int64(len(body)), body)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, retriesBefore, counter(t, metrics.StagedChunkRetries), "the first send is not a retry")

	code, _ = f.putChunk(t, id, 0, int64(len(body)), int64(len(body)), body)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, retriesBefore+1, counter(t, metrics.StagedChunkRetries))
}

// Transfers feed the per-storage throughput signal that internal/filecache
// (chunk 3) reads to decide whether a storage is slow. One measurement, two
// consumers — if the cache measured its own, the two would disagree and the
// first "the graph says fine but the cache keeps kicking in" bug would cost a
// day.
func TestMetrics_TransferIsObservedPerStorage(t *testing.T) {
	f := newStagedFixture(t)
	lbl := metrics.StorageLabel(f.storage.ID)
	before := promtestutil.ToFloat64(metrics.TransferBytes.WithLabelValues(lbl, "write"))

	body := randomBytes(5000)
	code, out := f.begin(t, map[string]any{
		"path": "main://", "name": "timed.bin", "size": len(body), "chunk_size": 8192,
	})
	require.Equal(t, http.StatusOK, code, "%v", out)
	id := out["id"].(string)
	code, _ = f.putChunk(t, id, 0, int64(len(body)), int64(len(body)), body)
	require.Equal(t, http.StatusOK, code)
	code, out = f.commit(t, id)
	require.Equal(t, http.StatusAccepted, code, "%v", out)
	require.Equal(t, "ok", f.waitForOp(t, int64(out["op_id"].(float64))))

	assert.Equal(t, before+float64(len(body)),
		promtestutil.ToFloat64(metrics.TransferBytes.WithLabelValues(lbl, "write")))
}

var _ = errors.New

// The ceiling has to hold on the SYNCHRONOUS path too. Large uploads reach the
// staged path and are checked at `begin`; the classic multipart handler is the
// small-file path, and with no check there a user sails past their quota a few
// megabytes at a time — which, now that usage is actually counted, is the
// difference between a ceiling and a decoration.
func TestQuotaGuard_HoldsOnTheClassicUploadPath(t *testing.T) {
	before := counter(t, metrics.GuardRefusals.WithLabelValues(metrics.GuardQuota))

	f := newStagedFixture(t)
	require.NoError(t, f.deps.Quota.SetQuota(context.Background(), f.userID, 4096))
	f.uploadMultipart(t, "fits.bin", make([]byte, 3000))
	require.EqualValues(t, 3000, f.usage(t))

	code, body := f.uploadMultipartCode(t, "over.bin", make([]byte, 3000))
	assert.Equal(t, http.StatusRequestEntityTooLarge, code, body)
	assert.Contains(t, body, "QUOTA_EXCEEDED")
	assert.Equal(t, before+1, counter(t, metrics.GuardRefusals.WithLabelValues(metrics.GuardQuota)))
	assert.EqualValues(t, 3000, f.usage(t), "a refused upload must not have been written")
}
