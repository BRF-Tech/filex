package filecache_test

/* Which storages are slow, and how we decide.

   The rule that matters most is the one that says NO: a fast backend must
   never be put behind a preparing screen, because that turns an instant
   stream into a wait. So the meter is allowed to overrule the operator, and
   an unmeasured storage nobody flagged keeps today's behaviour.

   ⚠ filecache no longer measures anything — internal/throughput does, once,
   for both this decision and the operator's dashboard. So these tests drive
   the policy the way production does: they feed the real meter and then ask
   filecache what it concluded. A test that injected a rate straight into
   filecache would keep passing even if the two packages had drifted apart,
   which is the exact failure this merge existed to remove.

   Each test uses a storage id of its own, because the meter is process-wide
   (that is the point of it) and its window outlives a single test. */

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/filecache"
	"github.com/brf-tech/filex/backend/internal/throughput"
)

const mib = 1 << 20

// observe feeds `times` reads of `mibs` MiB, each taking d, into the
// process-wide meter filecache reads.
func observe(storageID int64, mibs int, d time.Duration, times int) {
	for i := 0; i < times; i++ {
		throughput.Observe(storageID, throughput.Read, int64(mibs)*mib, d)
	}
}

// TestSmallFileNeverQualifies — the size gate comes first, whatever anyone
// says about the storage. A 40 KiB file on the slowest NAS in the world is one
// round trip; "preparing… 0 %" would be a worse answer than the file.
func TestSmallFileNeverQualifies(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 64 * mib})
	require.False(t, c.Qualifies(101, 40*1024, true), "small + operator-slow must still stream")
	require.False(t, c.Qualifies(101, 64*mib-1, true), "one byte under the threshold is still under it")
	require.True(t, c.Qualifies(101, 64*mib, true), "at the threshold it qualifies")
}

// TestUnmeasuredUnflaggedStorageIsNotSlow — a fresh install must not put its
// first big download behind a preparing screen on a guess. "Unknown" is not
// "zero": throughput reports it as unmeasured, and that must read as not-slow.
func TestUnmeasuredUnflaggedStorageIsNotSlow(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	_, known := throughput.Rate(102, throughput.Read)
	require.False(t, known, "precondition: nothing has been measured for this storage")
	require.False(t, c.Slow(102, false))
	require.False(t, c.Qualifies(102, 1<<30, false))
}

// TestOperatorFlagIsEnoughWhenNothingIsMeasured — the case the meter cannot
// know about yet: a NAS nobody has read from.
func TestOperatorFlagIsEnoughWhenNothingIsMeasured(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1})
	require.True(t, c.Slow(103, true))
}

// TestMeasuredSlowPromotesAStorageNobodyFlagged — the automatic half of the
// rule: enough slow reads and the storage qualifies without configuration.
func TestMeasuredSlowPromotesAStorageNobodyFlagged(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, SlowBytesPerSec: 10 * mib})

	observe(107, 8, time.Second, 1) // 8 MiB/s — slow, but one sample
	require.False(t, c.Slow(107, false), "one sample is an anecdote, not a measurement")

	observe(107, 8, time.Second, 2)
	require.True(t, c.Slow(107, false), "three agreeing samples are a measurement")

	bps, ok := throughput.Rate(107, throughput.Read)
	require.True(t, ok)
	require.InDelta(t, 8*mib, bps, float64(mib), "the shared meter must reflect what was fed in")
}

// TestMeasuredFastOverridesTheOperator is the "must not make things worse"
// rule. An operator who marked a storage slow — or who moved it onto a fast
// link afterwards and forgot — must not cost every user a full prefetch before
// the first byte. The margin (2x the threshold) means the meter only overrules
// when the evidence is not close.
func TestMeasuredFastOverridesTheOperator(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, SlowBytesPerSec: 10 * mib})

	observe(104, 200, time.Second, 4) // 200 MiB/s
	require.False(t, c.Slow(104, true), "a storage measured at 200 MiB/s is not slow, whatever the flag says")
	require.False(t, c.Qualifies(104, 1<<30, true))
}

// TestMeasurementCloseToTheThresholdKeepsTheOperatorsWord — between 1x and 2x
// the evidence is not strong enough to overrule a human, so the flag stands.
func TestMeasurementCloseToTheThresholdKeepsTheOperatorsWord(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, SlowBytesPerSec: 10 * mib})
	observe(105, 14, time.Second, 4) // 14 MiB/s: above slow, below 2x
	require.True(t, c.Slow(105, true))
	require.False(t, c.Slow(105, false), "…and without the flag it is not slow either")
}

// TestOneFastSampleDoesNotOverruleTheOperator — the sample bar guards the NO
// rule as well as the YES one. A single fast read (a warm object on the
// backend, an edge hit) must not switch caching off for a storage a human
// flagged.
func TestOneFastSampleDoesNotOverruleTheOperator(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, SlowBytesPerSec: 10 * mib})
	observe(106, 200, time.Second, 2) // fast, but only two samples
	require.True(t, c.Slow(106, true), "two samples are not enough to overrule a human")
	observe(106, 200, time.Second, 1)
	require.False(t, c.Slow(106, true), "the third one is")
}

// TestReadsTooSmallToBeEvidenceDoNotDecide — a thumbnail probe or a range poke
// is dominated by round-trip latency. The meter keeps such reads (the operator
// asked how many bytes moved, and they did move), but the POLICY refuses to
// decide on them: filecache asks throughput only for the samples above its own,
// much larger, floor.
//
// This is the seam the merge introduced, so it is asserted from both sides: a
// rate the dashboard can see, and a decision that will not act on it.
func TestReadsTooSmallToBeEvidenceDoNotDecide(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, SlowBytesPerSec: 10 * mib})
	for i := 0; i < 10; i++ {
		// 1 MiB in 500 ms = 2 MiB/s. Well over throughput.MinSample, well
		// under filecache's 4 MiB evidence floor.
		throughput.Observe(109, throughput.Read, mib, 500*time.Millisecond)
	}

	bps, ok := throughput.Rate(109, throughput.Read)
	require.True(t, ok, "the meter must still report these to the dashboard")
	require.InDelta(t, 2*mib, bps, float64(mib)/2)

	require.False(t, c.Slow(109, false),
		"reads too small to mean anything must not promote a storage to slow")
}

// TestClientPacedDownloadDoesNotMakeTheStorageLookSlow is the measurement's
// validity, as an assertion, stated where the consequence lands.
//
// A body being copied to a client is paced by the client. Wall clock across
// the transfer would measure the slower of {backend, browser}, so one user on
// a bad connection would mark a fast bucket slow for everybody — and the next
// person would be told "preparing…" for a file that would have streamed
// instantly. throughput.CountingReader therefore times the READ CALLS.
//
// Here the reads are instant and the CONSUMER sleeps between them. The pacing
// is chosen so a wall-clock meter would land BELOW the threshold and mark the
// storage slow: 256 KiB per 40 ms is 6.4 MiB/s against a 10 MiB/s bar. If this
// test is ever weakened, check that number first — a slow consumer that still
// computes to a fast rate proves nothing.
func TestClientPacedDownloadDoesNotMakeTheStorageLookSlow(t *testing.T) {
	c := newCache(t, filecache.Config{MinSize: 1, SlowBytesPerSec: 10 * mib})
	body := pattern(5 * mib)

	for i := 0; i < 3; i++ { // three, so the samples clear the policy's bar
		rc := throughput.CountingReader(io.NopCloser(bytes.NewReader(body)), 110, throughput.Read)
		buf := make([]byte, 256*1024)
		for {
			n, err := rc.Read(buf)
			if n > 0 {
				time.Sleep(40 * time.Millisecond) // the "client" is slow
			}
			if err != nil {
				break
			}
		}
		require.NoError(t, rc.Close())
	}

	bps, ok := throughput.Rate(110, throughput.Read)
	require.True(t, ok)
	require.Greater(t, bps, float64(20*mib),
		"the meter must measure the backend, not the client draining it (got %.0f B/s)", bps)
	require.False(t, c.Slow(110, false),
		"a storage nobody flagged must not be promoted to slow by a slow downloader")
}
