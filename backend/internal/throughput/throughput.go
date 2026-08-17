// Package throughput is the process-wide answer to one question:
// "how fast is this storage right now?"
//
// It exists because two features need the same number and must not each grow
// their own: the operator metrics (`filex_storage_throughput_bytes_per_second`)
// and internal/filecache, which decides whether an object is worth caching
// because the storage behind it is slow. A cache that measured slowness its own
// way would disagree with the dashboard the operator is looking at, and the
// first "the graph says it is fine but the cache keeps kicking in" bug would
// cost a day.
//
// # The API filecache should use
//
//	throughput.Observe(storageID, throughput.Read, n, elapsed)  // record
//	bps, ok := throughput.Rate(storageID, throughput.Read)      // read back
//	if throughput.IsSlow(storageID, throughput.Read) { … }      // the decision
//
// filecache does not in fact use IsSlow: its policy is more than one
// comparison (it lets a fast measurement overrule an operator's `slow: true`
// flag, and it refuses to act on thin evidence), so it asks StatAbove for the
// rate AND the sample count and applies that policy itself. The division is
// the point: the MEASUREMENT lives here and has one implementation; the POLICY
// lives in filecache, where it is not duplicated.
//
// `ok == false` means "no measurement yet". That is deliberately NOT folded
// into IsSlow's answer: an unmeasured storage is reported as not-slow, because
// treating silence as slowness would make every fresh boot behave as if every
// backend were a NAS over a phone line. A caller that wants to distinguish the
// two asks Rate.
//
// # What it measures
//
// A rolling window (Window, default 2 min / MaxSamples samples) of
// (bytes, duration) observations per storage and direction; the rate is
// sum(bytes)/sum(duration) over the window, i.e. a byte-weighted average, so a
// handful of tiny reads cannot drag the number around the way an
// average-of-rates would.
//
// Samples below MinSample bytes are ignored: the elapsed time of a 200-byte
// read is dominated by round-trip latency and says nothing about throughput.
// A caller that needs a stronger bar than MinSample passes its own floor to
// StatAbove rather than keeping a second set of samples.
//
// A read is timed by the time spent inside Read, not by wall clock across the
// transfer — see CountingReader for why that distinction decides whether the
// number describes the backend or the person downloading from it.
package throughput

import (
	"io"
	"sync"
	"time"
)

// Direction distinguishes reads from writes — a backend can be fast one way
// and slow the other (S3 uploads vs. downloads over an asymmetric link).
type Direction string

// The two directions.
const (
	Read  Direction = "read"
	Write Direction = "write"
)

// Tuning constants. Exported so a caller can reason about them (and so a test
// can state which one it is exercising) rather than guess.
const (
	// Window is how far back a sample still counts.
	Window = 2 * time.Minute
	// MaxSamples caps memory per storage+direction.
	MaxSamples = 64
	// MinSample is the smallest transfer that says anything about throughput.
	MinSample = 64 << 10 // 64 KiB
	// SlowBytesPerSec is the default "this storage is slow" line: 8 MB/s,
	// roughly where a 100 MB file stops being instant and starts being a
	// progress bar. filecache may pass its own threshold to IsSlowerThan.
	SlowBytesPerSec = 8 << 20
)

type sample struct {
	at    time.Time
	bytes int64
	dur   time.Duration
}

type key struct {
	storage int64
	dir     Direction
}

// Meter holds the rolling samples. The zero value is not usable — use New, or
// the package-level Default.
type Meter struct {
	mu sync.Mutex
	s  map[key][]sample
	// now is swappable in tests so a window can be aged without sleeping.
	now func() time.Time
}

// New returns an empty meter.
func New() *Meter { return &Meter{s: map[key][]sample{}, now: time.Now} }

// Default is the process-wide meter. Every caller shares it on purpose.
var Default = New()

// Observe records one transfer. Zero/short/instant samples are dropped.
func (m *Meter) Observe(storageID int64, dir Direction, bytes int64, d time.Duration) {
	if m == nil || storageID <= 0 || bytes < MinSample || d <= 0 {
		return
	}
	k := key{storageID, dir}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	list := append(m.trim(m.s[k], now), sample{at: now, bytes: bytes, dur: d})
	if len(list) > MaxSamples {
		list = list[len(list)-MaxSamples:]
	}
	m.s[k] = list
}

// trim drops samples that have aged out of the window. Caller holds the lock.
func (m *Meter) trim(list []sample, now time.Time) []sample {
	cut := now.Add(-Window)
	i := 0
	for ; i < len(list); i++ {
		if list[i].at.After(cut) {
			break
		}
	}
	return list[i:]
}

// Rate returns the rolling bytes/sec for one storage and direction. ok is false
// when there is nothing in the window — "unknown", never "zero".
func (m *Meter) Rate(storageID int64, dir Direction) (float64, bool) {
	st, ok := m.StatAbove(storageID, dir, 0)
	return st.BytesPerSec, ok
}

// StatAbove is Rate plus the evidence behind it — how many samples, how many
// bytes, when the last one landed — computed from the samples of at least
// minBytes.
//
// The floor is a parameter rather than a constant because a caller may need a
// stronger bar than the meter's own MinSample without growing a second meter
// to get it. internal/filecache is that caller: it will not let a measurement
// overrule an operator, or promote an unflagged storage, on the strength of a
// handful of small reads, so it asks with its own (much larger) floor. Same
// samples, same window, same arithmetic — one source, parametrised.
//
// minBytes <= 0 means "every sample the meter kept". ok is false when nothing
// clears the floor: unknown, never zero.
func (m *Meter) StatAbove(storageID int64, dir Direction, minBytes int64) (Stat, bool) {
	if m == nil {
		return Stat{}, false
	}
	k := key{storageID, dir}
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.trim(m.s[k], m.now())
	m.s[k] = list
	return aggregate(k, list, minBytes)
}

// aggregate folds a trimmed sample list into one Stat, counting only samples
// of at least minBytes. The rate is sum(bytes)/sum(duration) — byte-weighted,
// so a handful of tiny reads cannot drag it around.
func aggregate(k key, list []sample, minBytes int64) (Stat, bool) {
	var (
		bytes int64
		dur   time.Duration
		n     int
		last  time.Time
	)
	for _, s := range list {
		if s.bytes < minBytes {
			continue
		}
		bytes += s.bytes
		dur += s.dur
		n++
		last = s.at
	}
	if n == 0 || dur <= 0 {
		return Stat{}, false
	}
	return Stat{
		StorageID:    k.storage,
		Direction:    k.dir,
		BytesPerSec:  float64(bytes) / dur.Seconds(),
		Samples:      n,
		WindowBytes:  bytes,
		LastObserved: last,
	}, true
}

// IsSlow reports whether the storage is below SlowBytesPerSec. An unmeasured
// storage is NOT slow — see the package comment.
func (m *Meter) IsSlow(storageID int64, dir Direction) bool {
	return m.IsSlowerThan(storageID, dir, SlowBytesPerSec)
}

// IsSlowerThan is IsSlow with the caller's own threshold in bytes/sec.
func (m *Meter) IsSlowerThan(storageID int64, dir Direction, bytesPerSec float64) bool {
	bps, ok := m.Rate(storageID, dir)
	if !ok {
		return false
	}
	return bps < bytesPerSec
}

// Stat is one row of Snapshot.
type Stat struct {
	StorageID    int64
	Direction    Direction
	BytesPerSec  float64
	Samples      int
	WindowBytes  int64
	LastObserved time.Time
}

// Snapshot returns every measured storage+direction — the metrics exporter's
// input, and a readable thing to log when someone asks why a copy is crawling.
func (m *Meter) Snapshot() []Stat {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	out := make([]Stat, 0, len(m.s))
	for k, list := range m.s {
		list = m.trim(list, now)
		m.s[k] = list
		st, ok := aggregate(k, list, 0)
		if !ok {
			continue
		}
		out = append(out, st)
	}
	return out
}

// ── package-level shorthands over Default ───────────────────────────────────

// Observer is notified of every transfer recorded on the process-wide meter.
type Observer func(storageID int64, dir Direction, bytes int64, d time.Duration)

var (
	obsMu     sync.Mutex
	observers []Observer
)

// Subscribe registers a sink for every transfer recorded through the
// package-level Observe (and therefore through CountingReader).
//
// This exists so ONE call at the transfer site feeds both consumers: the
// rolling rate here, and the Prometheus counters in internal/metrics — which
// subscribes at init. Two calls at every site would eventually be one call at
// some site, and then the graph and the cache would be measuring different
// things. The dependency runs metrics → throughput; the hook is what keeps it
// from having to run both ways.
func Subscribe(o Observer) {
	if o == nil {
		return
	}
	obsMu.Lock()
	observers = append(observers, o)
	obsMu.Unlock()
}

// Observe records one transfer on the process-wide meter and notifies every
// subscriber.
//
// ⚠ The MinSample rule applies to the RATE, not to the notification. The meter
// drops a small transfer because its elapsed time is round-trip latency rather
// than throughput; a bytes-total that quietly ignored every small file would
// simply be wrong, and "we moved 4 GB today" is a question the counter has to
// be able to answer.
func Observe(storageID int64, dir Direction, bytes int64, d time.Duration) {
	if storageID <= 0 || bytes <= 0 || d <= 0 {
		return
	}
	Default.Observe(storageID, dir, bytes, d) // applies MinSample itself
	obsMu.Lock()
	subs := observers
	obsMu.Unlock()
	for _, o := range subs {
		o(storageID, dir, bytes, d)
	}
}

// Rate reads the process-wide meter.
func Rate(storageID int64, dir Direction) (float64, bool) { return Default.Rate(storageID, dir) }

// StatAbove reads the process-wide meter and reports the evidence with the
// rate. This is the call internal/filecache makes; do not re-measure.
func StatAbove(storageID int64, dir Direction, minBytes int64) (Stat, bool) {
	return Default.StatAbove(storageID, dir, minBytes)
}

// IsSlow asks the process-wide meter. This is the call internal/filecache
// should make; do not re-measure.
func IsSlow(storageID int64, dir Direction) bool { return Default.IsSlow(storageID, dir) }

// IsSlowerThan asks the process-wide meter with a caller-chosen threshold.
func IsSlowerThan(storageID int64, dir Direction, bytesPerSec float64) bool {
	return Default.IsSlowerThan(storageID, dir, bytesPerSec)
}

// Snapshot reads every rate off the process-wide meter.
func Snapshot() []Stat { return Default.Snapshot() }

// ── instrumentation helpers ─────────────────────────────────────────────────

// CountingReader wraps rc so every byte read is observed against storageID.
// The sample is recorded on Close — one observation per transfer rather than
// one per Read call, which is what makes the rate byte-weighted.
//
// This is how the download path is measured: filebody.Source hands back the
// wrapped reader, so every surface that streams a file (browser, WebDAV,
// share link, ShareX, the desktop app) feeds the same meter without knowing.
//
// ⚠ The duration recorded is the time spent INSIDE rc.Read, not wall-clock
// across the transfer, and that difference is the whole validity of the
// measurement. A body being copied to a client is paced by the client: wall
// clock would measure the slower of {backend, browser}, so one user on hotel
// wifi would mark a fast bucket slow for everybody — and internal/filecache
// would then answer the next person "preparing…" for a file that would have
// streamed instantly. Where read-time is still wrong it is wrong in the safe
// direction: buffered bytes return instantly, so the estimate reads FAST,
// which means fewer files qualify for the cache, never more.
func CountingReader(rc io.ReadCloser, storageID int64, dir Direction) io.ReadCloser {
	if rc == nil || storageID <= 0 {
		return rc
	}
	return &countingReader{rc: rc, storage: storageID, dir: dir}
}

type countingReader struct {
	rc      io.ReadCloser
	storage int64
	dir     Direction
	n       int64
	inRead  time.Duration
	done    bool
}

func (c *countingReader) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := c.rc.Read(p)
	c.inRead += time.Since(start)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) Close() error {
	if !c.done {
		c.done = true
		Observe(c.storage, c.dir, c.n, c.inRead)
	}
	return c.rc.Close()
}
