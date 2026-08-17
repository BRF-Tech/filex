package throughput

// The per-storage rate signal. internal/filecache (chunk 3) reads this to
// decide whether a storage is slow enough to be worth caching, and the
// Prometheus exporter publishes the same numbers — so the properties pinned
// here are the contract between those two, not implementation detail.

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newAt returns a meter with a clock the test drives.
func newAt(now *time.Time) *Meter {
	m := New()
	m.now = func() time.Time { return *now }
	return m
}

// The rate is byte-weighted: sum(bytes)/sum(duration), so one tiny sample
// cannot drag the number around the way an average-of-rates would.
func TestRate_IsByteWeighted(t *testing.T) {
	now := time.Now()
	m := newAt(&now)

	m.Observe(1, Read, 10<<20, time.Second)  // 10 MB/s
	m.Observe(1, Read, 100<<10, time.Second) // 0.1 MB/s over a tenth the data
	bps, ok := m.Rate(1, Read)
	require.True(t, ok)

	// (10 MiB + 100 KiB) / 2 s
	assert.InDelta(t, float64((10<<20)+(100<<10))/2.0, bps, 1)
}

// "No measurement" is not "zero". A caller that cannot tell the difference
// would treat a fresh boot as a stalled backend.
func TestRate_UnknownIsNotZero(t *testing.T) {
	m := New()
	bps, ok := m.Rate(42, Read)
	assert.False(t, ok)
	assert.Zero(t, bps)
	assert.False(t, m.IsSlow(42, Read), "an unmeasured storage is not slow")
}

// Directions are measured apart: a link can be fast one way and slow the
// other, and an S3 upload behaves nothing like an S3 download.
func TestRate_DirectionsAreSeparate(t *testing.T) {
	now := time.Now()
	m := newAt(&now)
	m.Observe(1, Read, 100<<20, time.Second)
	m.Observe(1, Write, 1<<20, time.Second)

	assert.False(t, m.IsSlow(1, Read))
	assert.True(t, m.IsSlow(1, Write))
}

// Samples age out of the window, so a storage that was slow an hour ago is not
// still slow now.
func TestRate_SamplesAgeOut(t *testing.T) {
	now := time.Now()
	m := newAt(&now)
	m.Observe(1, Read, 1<<20, time.Second)
	require.True(t, m.IsSlow(1, Read))

	now = now.Add(Window + time.Second)
	_, ok := m.Rate(1, Read)
	assert.False(t, ok, "an expired window reports unknown, not the old number")
}

// A read too small to say anything about throughput is dominated by
// round-trip latency, and would make every backend look terrible.
func TestObserve_IgnoresSamplesTooSmallToMeanAnything(t *testing.T) {
	now := time.Now()
	m := newAt(&now)
	m.Observe(1, Read, 200, time.Second)
	_, ok := m.Rate(1, Read)
	assert.False(t, ok)
}

// IsSlowerThan is the knob filecache can turn without inventing its own meter.
func TestIsSlowerThan_UsesTheCallersThreshold(t *testing.T) {
	now := time.Now()
	m := newAt(&now)
	m.Observe(1, Read, 4<<20, time.Second) // 4 MB/s

	assert.True(t, m.IsSlow(1, Read), "below the shared 8 MB/s default")
	assert.False(t, m.IsSlowerThan(1, Read, 1<<20), "not below a 1 MB/s bar")
}

// Snapshot is what the exporter renders; it must carry the label pair.
func TestSnapshot_ReportsEveryMeasuredPair(t *testing.T) {
	now := time.Now()
	m := newAt(&now)
	m.Observe(7, Read, 8<<20, time.Second)
	m.Observe(7, Write, 8<<20, 2*time.Second)

	stats := m.Snapshot()
	require.Len(t, stats, 2)
	for _, s := range stats {
		assert.EqualValues(t, 7, s.StorageID)
		assert.Positive(t, s.BytesPerSec)
		assert.Equal(t, 1, s.Samples)
	}
}

// CountingReader is how the download path is measured: one observation per
// transfer, recorded at Close, so the rate stays byte-weighted rather than
// being re-averaged per Read call.
func TestCountingReader_ObservesOnceOnClose(t *testing.T) {
	Default = New() // this test uses the package-level meter
	payload := bytes.Repeat([]byte("x"), MinSample+10)

	rc := CountingReader(io.NopCloser(bytes.NewReader(payload)), 3, Read)
	_, ok := Rate(3, Read)
	require.False(t, ok, "nothing is recorded until the transfer finishes")

	n, err := io.Copy(io.Discard, rc)
	require.NoError(t, err)
	require.EqualValues(t, len(payload), n)
	require.NoError(t, rc.Close())

	stats := Snapshot()
	require.Len(t, stats, 1)
	assert.EqualValues(t, len(payload), stats[0].WindowBytes)

	// Close is idempotent — a double close must not double-count.
	_ = rc.Close()
	assert.EqualValues(t, len(payload), Snapshot()[0].WindowBytes)
}

// ⚠ The duration recorded is time spent INSIDE Read, not wall clock across
// the transfer. A body copied to a client is paced by the client; wall clock
// would measure the slower of {backend, browser}, and internal/filecache would
// then answer "preparing…" for a file that would have streamed instantly.
//
// Reads here are instant and the consumer sleeps between them, so wall clock
// would read ~2 MiB/s against a true rate orders of magnitude higher.
func TestCountingReader_TimesTheReadCallsNotTheClient(t *testing.T) {
	Default = New()
	payload := bytes.Repeat([]byte("x"), 4*MinSample)

	rc := CountingReader(io.NopCloser(bytes.NewReader(payload)), 11, Read)
	buf := make([]byte, MinSample)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			time.Sleep(20 * time.Millisecond) // the "client" is slow
		}
		if err != nil {
			break
		}
	}
	require.NoError(t, rc.Close())

	bps, ok := Rate(11, Read)
	require.True(t, ok)
	// Wall clock over this transfer is ~80 ms for 256 KiB ≈ 3.2 MB/s; the
	// read-time measure is orders of magnitude above it.
	assert.Greater(t, bps, float64(100*MinSample),
		"the meter measured the consumer, not the source (got %.0f B/s)", bps)
}

// Instrumentation must not change what a reader returns.
func TestCountingReader_PassesTheBytesThrough(t *testing.T) {
	Default = New()
	payload := bytes.Repeat([]byte("abcdefghij"), 500)
	rc := CountingReader(io.NopCloser(bytes.NewReader(payload)), 12, Read)
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	assert.Equal(t, payload, got)
}

// StatAbove is how a caller gets a stronger evidence bar than MinSample
// without keeping a second set of samples — internal/filecache is that caller,
// and it will not take a decision on reads too small to mean anything.
func TestStatAbove_AppliesTheCallersEvidenceFloor(t *testing.T) {
	now := time.Now()
	m := newAt(&now)
	m.Observe(1, Read, 100<<10, time.Second) // above MinSample, below 4 MiB
	m.Observe(1, Read, 100<<10, time.Second)
	m.Observe(1, Read, 8<<20, time.Second) // the only real sample

	all, ok := m.StatAbove(1, Read, 0)
	require.True(t, ok)
	assert.Equal(t, 3, all.Samples, "the rate the dashboard sees keeps every sample")

	big, ok := m.StatAbove(1, Read, 4<<20)
	require.True(t, ok)
	assert.Equal(t, 1, big.Samples)
	assert.InDelta(t, float64(8<<20), big.BytesPerSec, 1)

	_, ok = m.StatAbove(1, Read, 1<<30)
	assert.False(t, ok, "nothing clears the floor: unknown, not zero")
}

// One meter, many transfers, two consumers reading it — that is the shape in
// production (every download goroutine observes; filecache asks on every
// qualifying request; Prometheus snapshots on every scrape). Run under -race.
func TestMeter_IsSafeUnderConcurrentUse(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				m.Observe(int64(g%3)+1, Read, MinSample*2, time.Millisecond)
				m.Observe(int64(g%3)+1, Write, MinSample*2, time.Millisecond)
			}
		}(g)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = m.Rate(1, Read)
				_, _ = m.StatAbove(2, Read, MinSample)
				m.IsSlow(3, Write)
				_ = m.Snapshot()
			}
		}()
	}
	wg.Wait()

	st, ok := m.StatAbove(1, Read, 0)
	require.True(t, ok)
	assert.Positive(t, st.Samples)
	assert.LessOrEqual(t, st.Samples, MaxSamples, "the window is capped")
}

// A nil reader or an unknown storage passes straight through: instrumentation
// must never be the reason a download fails.
func TestCountingReader_IsInert_WhenThereIsNothingToLabel(t *testing.T) {
	assert.Nil(t, CountingReader(nil, 1, Read))
	rc := io.NopCloser(bytes.NewReader([]byte("hi")))
	assert.Equal(t, rc, CountingReader(rc, 0, Read))
}

// Subscribe is the seam internal/metrics uses so ONE call at the transfer site
// feeds both the rolling rate and the Prometheus counters. It also has to
// forward the small transfers the RATE deliberately ignores — a bytes-total
// that skipped every small file would simply be wrong.
func TestSubscribe_ForwardsEveryTransfer_IncludingTheOnesTheRateIgnores(t *testing.T) {
	Default = New()
	type seen struct {
		storage int64
		dir     Direction
		bytes   int64
	}
	var got []seen
	Subscribe(func(id int64, d Direction, b int64, _ time.Duration) {
		got = append(got, seen{id, d, b})
	})
	t.Cleanup(func() { observers = nil })

	Observe(9, Write, MinSample*2, time.Second) // counted by both
	Observe(9, Write, 500, time.Second)         // counted by the subscriber only

	require.Len(t, got, 2, "the counter must see the small transfer too")
	assert.EqualValues(t, MinSample*2, got[0].bytes)
	assert.EqualValues(t, 500, got[1].bytes)

	stats := Snapshot()
	require.Len(t, stats, 1)
	assert.EqualValues(t, MinSample*2, stats[0].WindowBytes,
		"the RATE still ignores the 500-byte sample: that duration is latency, not throughput")
}
