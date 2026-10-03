package server

import (
	"context"
	"slices"

	"github.com/brf-tech/filex/backend/internal/ops"
)

// thumbRepairer is the ops job "repair thumbnails" (ops/thumb_repair.go)
// running the one thumbnail walk, BackfillThumbs: Admin → Tools → Thumbnail
// repair and `filex thumb backfill` draw the same files for the same ask.
type thumbRepairer struct{ s *Server }

// repairConcurrency is how many files a repair draws at once. Lower than the
// CLI's default: the repair runs beside a live server, and one office file
// is a conversion on the OnlyOffice document server, which its editors share.
const repairConcurrency = 2

// optionsFor turns a job into the walk's options. ok is false when the job
// may reach no storage at all (a tenant with none, or a storage outside its
// reach): nothing is walked.
func optionsFor(job ops.ThumbRepairJob) (opts BackfillOptions, ok bool) {
	opts = BackfillOptions{Path: job.Path, Concurrency: repairConcurrency}
	if job.Mode == ops.ThumbRepairRebuild {
		opts.All = true
	} else {
		opts.Stale, opts.RetryFailed, opts.RetrySkipped = true, true, true
	}
	switch {
	case job.StorageID != 0:
		if job.Reach != nil && !slices.Contains(job.Reach, job.StorageID) {
			return opts, false
		}
		opts.StorageIDs = []int64{job.StorageID}
	case job.Reach != nil:
		if len(job.Reach) == 0 {
			return opts, false
		}
		opts.StorageIDs = append([]int64{}, job.Reach...)
	}
	return opts, true
}

func (r thumbRepairer) CountRepair(ctx context.Context, job ops.ThumbRepairJob) (int, error) {
	opts, ok := optionsFor(job)
	if !ok {
		return 0, nil
	}
	return r.s.CountBackfill(ctx, opts)
}

func (r thumbRepairer) RunRepair(ctx context.Context, job ops.ThumbRepairJob, progress func(ops.ThumbRepairCounts)) (ops.ThumbRepairCounts, error) {
	opts, ok := optionsFor(job)
	if !ok {
		return ops.ThumbRepairCounts{}, nil
	}
	opts.ProgressEvery = 1
	opts.OnProgress = func(st BackfillStats) { progress(countsOf(st)) }
	st, err := r.s.BackfillThumbs(ctx, opts)
	return countsOf(st), err
}

func countsOf(st BackfillStats) ops.ThumbRepairCounts {
	c := ops.ThumbRepairCounts{Processed: st.Processed, OK: st.OK, Failed: st.Failed, Skipped: st.Skipped}
	for _, n := range st.NotIndexed {
		c.Refused = append(c.Refused, ops.ThumbRefusal{StorageID: n.ID, Code: n.Code})
	}
	return c
}
