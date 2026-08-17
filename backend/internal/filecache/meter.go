package filecache

// Which storages are slow enough that preparing a local copy beats streaming
// from them.
//
// ⚠ This file holds the POLICY only. The measurement lives in
// internal/throughput and there is exactly one of it, because the operator's
// dashboard (`filex_storage_throughput_bytes_per_second`) and this decision
// have to be reading the same number — a cache that measured slowness its own
// way would kick in while the graph said the backend was fine, and that
// disagreement costs a day the first time it happens. Nothing here records
// anything: it asks throughput what the storage is doing, and decides.

import (
	"github.com/brf-tech/filex/backend/internal/throughput"
)

// DefaultSlowBytesPerSec is the measured throughput below which a storage is
// treated as slow. 10 MiB/s is roughly a 100 Mbit link, and at that rate the
// smallest qualifying file (64 MiB) already takes seven seconds — long enough
// that "preparing…" is information rather than noise, and short enough that a
// healthy S3 or a local disk (both an order of magnitude faster) never trips
// it.
const DefaultSlowBytesPerSec = 10 << 20

// meterMinSamples is how many reads must agree before a measurement is
// allowed to decide anything. One sample is an anecdote: a single slow read
// can be a cold connection, a retried request or a busy moment.
const meterMinSamples = 3

// meterMinBytes is the smallest read this policy accepts as evidence. A
// thumbnail probe or a range poke is dominated by round-trip latency, not
// throughput, and letting three of them promote a storage to "slow" would put
// a healthy backend behind a preparing screen.
//
// ⚠ Deliberately far above throughput.MinSample (64 KiB). The meter's own
// floor keeps the RATE honest for a graph; this one keeps a DECISION from
// being taken on thin evidence, and the two want different numbers. That is
// why it is handed to throughput.StatAbove rather than enforced by a second
// meter: same samples, same window, stricter bar.
const meterMinBytes = 4 << 20 // 4 MiB

// measured is the read rate for a storage and how many samples large enough to
// mean something are behind it. samples == 0 is "never measured", which is
// never slow — see Slow, rule 4.
func measured(storageID int64) (bps float64, samples int) {
	st, ok := throughput.StatAbove(storageID, throughput.Read, meterMinBytes)
	if !ok {
		return 0, 0
	}
	return st.BytesPerSec, st.Samples
}

// Slow reports whether a storage is slow enough that preparing a local copy
// beats streaming from it.
//
// The policy, in order, and the reason for each step:
//
//  1. Measured fast overrides the operator. A storage flagged `slow: true`
//     that measures at twice the threshold is NOT cached. This is the
//     "must not make things worse" rule: on a fast backend, preparing turns
//     an instant stream into a wait for a full prefetch, and the operator's
//     flag is a guess about the network while the meter is a measurement of
//     it. The margin (2x) means we only override when the evidence is not
//     close.
//  2. The operator's flag. A NAS that has never been read is the case the
//     meter cannot know about yet, and the operator can.
//  3. Measured slow. Once there are enough samples, the meter can promote a
//     storage nobody flagged.
//  4. Otherwise no. An unmeasured, unflagged storage keeps today's behaviour.
//     Defaulting to "cache it" would put every fresh install behind a
//     preparing screen on its first big download.
func (c *Cache) Slow(storageID int64, operatorSlow bool) bool {
	if c == nil {
		return false
	}
	bps, samples := measured(storageID)
	if samples >= meterMinSamples && bps >= float64(2*c.slowBPS) {
		return false
	}
	if operatorSlow {
		return true
	}
	return samples >= meterMinSamples && bps < float64(c.slowBPS)
}

// Qualifies is the whole gate in one place: caching is on, the file is big
// enough to be worth it, and the storage is slow enough to need it.
//
// Small files never qualify, whatever the operator says about the storage —
// a 40 KiB PNG on the slowest NAS in the world is one round trip, and
// answering it with "preparing… 0 %" would be a worse product than answering
// it with the file.
func (c *Cache) Qualifies(storageID int64, size int64, operatorSlow bool) bool {
	if !c.Enabled() || size < c.MinSize() {
		return false
	}
	return c.Slow(storageID, operatorSlow)
}
