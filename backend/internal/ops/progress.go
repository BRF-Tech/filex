package ops

import (
	"context"
	"io"
	"path"
	"sync/atomic"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// Byte progress for queued cross-storage transfers (issue #27).
//
// A queued op counts SOURCES: one selected file or folder is one step. Moving a
// single large file, or one folder of a thousand files, is therefore `0 of 1`
// until the very end — the tray showed a bar frozen at 0% that vanished on
// completion, and the reporter read it as stuck. Cross-storage transfers are
// the ones that stream bytes (a same-storage move is a rename or a server-side
// copy), so those are measured in bytes while they run.
//
// The counters live in memory beside the worker and are merged into Get/List
// answers. They are not written to pending_ops: they change on every read
// buffer, and a restart ends the transfer they describe anyway.

// liveProgress is one running op's byte counters. total 0 means "not known
// yet" (the measuring walk has not finished, or gave up on a huge tree).
type liveProgress struct {
	done  atomic.Int64
	total atomic.Int64
	// objects is a same-storage job's object count (storage.Tally).
	objects storage.Tally
}

func (s *Service) liveFor(id int64) *liveProgress {
	if v, ok := s.live.Load(id); ok {
		return v.(*liveProgress)
	}
	return nil
}

// attachLive copies a running op's byte counters onto the row being returned.
func (s *Service) attachLive(op *Op) {
	if op == nil {
		return
	}
	if lp := s.liveFor(op.ID); lp != nil {
		op.BytesDone = lp.done.Load()
		op.BytesTotal = lp.total.Load()
		op.ObjectsDone, op.ObjectsTotal = lp.objects.Load()
	}
}

// measureBudget caps how many objects the measuring walk may list before it
// gives up and leaves the total unknown: listing a vast tree twice is a cost
// the transfer should not pay just to draw a percentage.
const measureBudget = 200_000

// measureSources sums the bytes a transfer of srcs will stream, skipping the
// same bookkeeping folders transferDir skips. ok=false means the walk failed or
// ran out of budget; the caller then shows progress without a total.
func measureSources(ctx context.Context, drv storage.Driver, srcs []string) (int64, bool) {
	budget := measureBudget
	var total int64
	for _, src := range srcs {
		stat, err := drv.Stat(ctx, src)
		if err != nil {
			return 0, false
		}
		if stat.Kind != storage.KindDirectory {
			total += stat.Size
			continue
		}
		n, ok := measureDir(ctx, drv, src, &budget, storage.NewCycleGuard(), 0)
		if !ok {
			return 0, false
		}
		total += n
	}
	return total, true
}

// ⚠ The guard is not belt-and-braces here the way the budget is. The budget
// bounds a cyclic walk at 200 000 listings and then reports ok=false, so the
// operator loses the progress bar and the transfer runs blind; the guard stops
// the loop at the link instead, and the total stays real.
func measureDir(ctx context.Context, drv storage.Driver, dir string, budget *int, guard *storage.CycleGuard, depth int) (int64, bool) {
	objs, err := drv.List(ctx, dir)
	if err != nil {
		return 0, false
	}
	var total int64
	for _, o := range objs {
		if ctx.Err() != nil {
			return 0, false
		}
		*budget--
		if *budget < 0 {
			return 0, false
		}
		if skipName(o.Name) {
			continue
		}
		if o.Kind == storage.KindDirectory {
			if !guard.Enter(o, depth+1) {
				continue
			}
			n, ok := measureDir(ctx, drv, path.Join(dir, o.Name), budget, guard, depth+1)
			if !ok {
				return 0, false
			}
			total += n
			continue
		}
		total += o.Size
	}
	return total, true
}

// countBytes wraps a transfer's source reader so every byte read the first
// time is reported. A seekable source stays seekable — the S3 driver rewinds a
// seekable body when a signed request is retried — and a rewind is not counted
// twice: only reads past the furthest position already reached report bytes.
func countBytes(rc io.ReadCloser, report func(int64)) io.ReadCloser {
	if report == nil {
		return rc
	}
	c := &byteCounter{ReadCloser: rc, report: report}
	if sk, ok := rc.(io.Seeker); ok {
		return &seekableByteCounter{byteCounter: c, seeker: sk}
	}
	return c
}

type byteCounter struct {
	io.ReadCloser
	report func(int64)
	pos    int64
	high   int64
}

func (c *byteCounter) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	if n > 0 {
		c.pos += int64(n)
		if c.pos > c.high {
			c.report(c.pos - c.high)
			c.high = c.pos
		}
	}
	return n, err
}

type seekableByteCounter struct {
	*byteCounter
	seeker io.Seeker
}

func (c *seekableByteCounter) Seek(offset int64, whence int) (int64, error) {
	at, err := c.seeker.Seek(offset, whence)
	if err == nil {
		c.pos = at
	}
	return at, err
}
