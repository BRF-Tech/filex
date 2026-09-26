package handlers

import (
	"io"
	"sync"
)

// How an archive job spends its 100 units (issue #27's lesson, applied to
// archives). Each phase moves in BYTES while it runs:
//
//	0 … archiveStagedAt                    reading every member off its storage
//	archiveStagedAt … archiveCompressedAt  compressing (7-Zip's own percentage,
//	                                       or the bytes the built-in ZIP has read)
//	archiveCompressedAt … archiveWrittenAt writing the archive to its destination
//	100                                    catalogued and announced
//
// ⚠ The split is not a measurement of any one storage. On a remote store the
// reading and the writing are most of the time — production, 2026-09-25: 175
// members off S3 were ~100 of the job's 118 seconds — and on a local disk the
// compressing is. What the split guarantees is what the person watching needs:
// no phase is squeezed into a sliver of the bar, so the bar moves for as long
// as the job runs. Reading used to be worth 0–10 counted per member, and the
// built-in ZIP and the write reported nothing: the operations centre read "0%"
// beside an empty ring for the whole run, which looks like a job that never
// started.
const (
	archiveStagedAt     = 45
	archiveCompressedAt = 90
	archiveWrittenAt    = 99
)

// archiveProgress turns a phase's progress into the job's units. It reports a
// unit once and never goes back — a retried read rewinds, and 7-Zip's two
// streams report from two goroutines at once — so the queue row is written at
// most once per unit, not once per read buffer.
type archiveProgress struct {
	mu     sync.Mutex
	last   int
	report func(done int)
}

func newArchiveProgress(report func(done int)) *archiveProgress {
	return &archiveProgress{report: report}
}

// at reports units once they are past the last reported.
func (p *archiveProgress) at(units int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if units > p.last {
		p.last = units
		p.report(units)
	}
}

// span reports done of total as the matching point of the band from…to. A
// phase with nothing to count, or more than it announced, is at its end.
func (p *archiveProgress) span(from, to int, done, total int64) {
	if total <= 0 || done >= total {
		p.at(to)
		return
	}
	if done < 0 {
		done = 0
	}
	p.at(from + int(int64(to-from)*done/total))
}

// progressReader tells how many bytes have come through it so far.
type progressReader struct {
	r    io.Reader
	n    int64
	tell func(n int64)
}

func (r *progressReader) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	if n > 0 {
		r.n += int64(n)
		r.tell(r.n)
	}
	return n, err
}
