package wasmplugin

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ⚠⚠ A warm instance calls its module without building the call again.
//
// extism go-sdk v1.7.1 asked wazero for a NEW function object on every call
// - the export, and each of the kernel functions a call goes through (input,
// output, alloc, length, error) - and wazero gave each one a fresh 10 KiB
// call stack that the Go guest then grew. Measured 2026-11-11 (#313 H2): a
// fifth of a short link's visit went there. backend/third_party/extism-go-sdk
// keeps those functions per instance (third_party/extism-go-sdk/FILEX.md).
// This test is what notices the patch gone - an extism upgrade that drops the
// `replace`, or a copy without the change: the bytes a warm visit allocates
// jump from a few KiB to tens of KiB.
func TestPool_AWarmVisitDoesNotRebuildTheCall(t *testing.T) {
	h := newRoutesHarness(t, nil)
	h.setUp(t)
	for i := 0; i < 50; i++ {
		_, err := h.visit(t, "hit-w", false)
		require.NoError(t, err)
	}
	const n = 400
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for i := 0; i < n; i++ {
		out, err := h.visit(t, "hit-w", false)
		require.NoError(t, err)
		require.Equal(t, "https://example.com/w", out.Redirect, "the answer stays right")
	}
	runtime.ReadMemStats(&after)
	perVisit := float64(after.TotalAlloc-before.TotalAlloc) / n
	t.Logf("bytes allocated per warm visit: %.0f", perVisit)
	assert.Less(t, perVisit, float64(warmVisitBytesCeiling), "a warm visit rebuilds its calls: is backend/third_party/extism-go-sdk still the module in use?")
}

// warmVisitBytesCeiling is above what a warm visit allocates with the
// patched extism and far below what it allocates without it. Measured
// 2026-11-11 on the echo fixture: 5,697 bytes patched, 262,268 without.
const warmVisitBytesCeiling = 32 << 10

// One instance is never called by two visits at once, and many visits at
// once all get their own right answer from the pool's instances.
func TestPool_ConcurrentVisitsGetTheirOwnAnswers(t *testing.T) {
	h := newRoutesHarness(t, func(o *Options) { o.RoutePool = 3 })
	h.setUp(t)
	c, err := h.p.running()
	require.NoError(t, err)
	pool := c.hot(poolSize(h.reg.opts.RoutePool))

	var wg sync.WaitGroup
	var wrong, ok atomic.Int32
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				x := string(rune('a'+g)) + string(rune('a'+i%26))
				out, err := h.visit(t, "hit-"+x, false)
				if err != nil {
					continue
				}
				ok.Add(1)
				if out.Redirect != "https://example.com/"+x {
					wrong.Add(1)
				}
			}
		}(g)
	}
	// An instance handed to two visits at once shows at once (overlaps), or
	// as visits that never finish: either fails here, not at the package's
	// ten-minute timeout.
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	deadline := time.Now().Add(60 * time.Second)
wait:
	for {
		select {
		case <-finished:
			break wait
		case <-time.After(50 * time.Millisecond):
			require.Zero(t, pool.overlaps.Load(), "an instance was handed to two visits at once")
			require.True(t, time.Now().Before(deadline), "the visits did not finish")
		}
	}
	assert.Zero(t, wrong.Load(), "every visit gets its own answer")
	assert.Greater(t, ok.Load(), int32(300))
	assert.LessOrEqual(t, int(pool.peak.Load()), pool.size, "never more calls than instances")
	assert.Zero(t, pool.overlaps.Load(), "an instance answers one visit at a time")
}
