package ops

// "Repair thumbnails" as an ops job (Admin → Tools → Thumbnail repair).
//
// It follows "empty the trash now" (trash_empty.go) on purpose, point for
// point: a run of a whole storage takes as long as the storage does, so it
// cannot live inside a request; it is in the operations centre and the admin
// tray; it is cancelled the way every op is (POST /api/files/ops/{id}/cancel);
// it is its tenant's alone; and a restart does not forget it. Like the purge
// it runs in its own goroutine, not on the queue's single worker, which would
// otherwise hold every copy and move for the length of the repair.
//
// The walk itself is not here: it is server.BackfillThumbs, the one walk the
// CLI (`filex thumb backfill`) takes too (ThumbRepairer).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// OpThumbRepair is "repair thumbnails" (see the file comment).
const OpThumbRepair = "thumb-repair"

// Repair modes: Fix draws what is missing, failed, skipped or stale; Rebuild
// draws everything in scope.
const (
	ThumbRepairFix     = "fix"
	ThumbRepairRebuild = "rebuild"
)

// ThumbRepairJob is what one run was asked to repair.
type ThumbRepairJob struct {
	// StorageID is the storage; 0 is every storage in Reach.
	StorageID int64
	// Path is a file, or a folder with everything in it, inside StorageID:
	// storage-relative. "" is the whole storage.
	Path string
	// Mode is ThumbRepairFix or ThumbRepairRebuild.
	Mode string
	// Reach is the storages the asker may repair: nil every one, empty none.
	Reach []int64
}

// ThumbRepairCounts is what a run has done so far.
type ThumbRepairCounts struct {
	Processed int
	OK        int
	Failed    int
	Skipped   int
	// Refused are the storages the run would not repair, with why.
	Refused []ThumbRefusal
}

// ThumbRefusal is one storage a run refused, and the reason's code
// (server.Gap*: its catalogue cannot hold its files yet).
type ThumbRefusal struct {
	StorageID int64
	Code      string
}

// ThumbRepairer runs the repair behind an OpThumbRepair row: the server's
// thumbnail walk. Injected with SetThumbRepairer.
type ThumbRepairer interface {
	// CountRepair is how many files the job would draw right now.
	CountRepair(ctx context.Context, job ThumbRepairJob) (int, error)
	// RunRepair draws them, calling progress as it goes.
	RunRepair(ctx context.Context, job ThumbRepairJob, progress func(ThumbRepairCounts)) (ThumbRepairCounts, error)
}

// SetThumbRepairer wires the repair. Without it SubmitThumbRepair refuses.
func (s *Service) SetThumbRepairer(r ThumbRepairer) { s.thumbRepairer = r }

// ErrThumbRepairBusy is SubmitThumbRepair's answer while the caller's tenant
// already has a repair queued or running; the op returned beside it is that one.
var ErrThumbRepairBusy = errors.New("ops: thumbnails are already being repaired")

// ThumbRepairRequest is what an admin asked to repair, and whose ask it is.
type ThumbRepairRequest struct {
	Job ThumbRepairJob
	// Tenant is TenantKey of the caller.
	Tenant string
}

// thumbParams is what an OpThumbRepair row stores in sources_json: the ask,
// and once the run has ended, the storages it refused.
type thumbParams struct {
	mode    string
	path    string
	reach   []int64 // nil: every storage
	refused []ThumbRefusal
}

func (p thumbParams) encode() []string {
	out := []string{"mode=" + p.mode, "path=" + p.path}
	if p.reach != nil {
		ids := make([]string, len(p.reach))
		for i, id := range p.reach {
			ids[i] = strconv.FormatInt(id, 10)
		}
		out = append(out, "reach="+strings.Join(ids, ","))
	}
	if len(p.refused) > 0 {
		parts := make([]string, len(p.refused))
		for i, r := range p.refused {
			parts[i] = strconv.FormatInt(r.StorageID, 10) + ":" + r.Code
		}
		out = append(out, "refused="+strings.Join(parts, ","))
	}
	return out
}

// decodeThumbParams reads encode's form back. A row it cannot read reaches
// NOTHING: a repair that cannot say what it may touch touches no storage.
func decodeThumbParams(srcs []string) (thumbParams, error) {
	none := thumbParams{mode: ThumbRepairFix, reach: []int64{}}
	var p thumbParams
	for _, kv := range srcs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return none, fmt.Errorf("ops: thumb-repair: bad parameter %q", kv)
		}
		switch k {
		case "mode":
			if v != ThumbRepairFix && v != ThumbRepairRebuild {
				return none, fmt.Errorf("ops: thumb-repair: bad mode %q", v)
			}
			p.mode = v
		case "path":
			p.path = v
		case "reach":
			p.reach = []int64{}
			if v == "" {
				continue
			}
			for _, part := range strings.Split(v, ",") {
				id, err := strconv.ParseInt(part, 10, 64)
				if err != nil || id <= 0 {
					return none, fmt.Errorf("ops: thumb-repair: bad reach %q", v)
				}
				p.reach = append(p.reach, id)
			}
		case "refused":
			for _, part := range strings.Split(v, ",") {
				idS, code, _ := strings.Cut(part, ":")
				id, err := strconv.ParseInt(idS, 10, 64)
				if err == nil {
					p.refused = append(p.refused, ThumbRefusal{StorageID: id, Code: code})
				}
			}
		default:
			return none, fmt.Errorf("ops: thumb-repair: unknown parameter %q", k)
		}
	}
	if p.mode == "" {
		return none, errors.New("ops: thumb-repair: no mode")
	}
	return p, nil
}

// ThumbRepairOf reads an OpThumbRepair row's ask and outcome back. ok is false
// for any other kind of row.
func (op *Op) ThumbRepairOf() (job ThumbRepairJob, refused []ThumbRefusal, tenantKey string, ok bool) {
	if op == nil || op.Kind != OpThumbRepair {
		return ThumbRepairJob{}, nil, "", false
	}
	return ThumbRepairJob{StorageID: op.StorageID, Path: op.thumb.path, Mode: op.thumb.mode, Reach: op.thumb.reach},
		op.thumb.refused, op.tenant, true
}

// SubmitThumbRepair queues a repair and starts it.
//
// While the caller's tenant already has one queued or running, nothing is
// queued: the answer is ErrThumbRepairBusy with that run. Another tenant's
// run does not refuse this one.
func (s *Service) SubmitThumbRepair(ctx context.Context, req ThumbRepairRequest) (*Op, error) {
	if s.thumbRepairer == nil {
		return nil, errors.New("ops: no thumbnail repairer wired")
	}
	if req.Job.Mode != ThumbRepairFix && req.Job.Mode != ThumbRepairRebuild {
		return nil, fmt.Errorf("ops: thumb-repair: bad mode %q", req.Job.Mode)
	}
	s.thumbMu.Lock()
	defer s.thumbMu.Unlock()
	if cur, err := s.latestThumbRepair(ctx, req.Tenant, true); err != nil {
		return nil, err
	} else if cur != nil {
		return cur, ErrThumbRepairBusy
	}
	params := thumbParams{mode: req.Job.Mode, path: req.Job.Path, reach: req.Job.Reach}
	srcJSON, _ := json.Marshal(params.encode())
	id, err := s.insertOp(ctx, OpThumbRepair, req.Job.StorageID, req.Job.StorageID, string(srcJSON), req.Tenant, 0)
	if err != nil {
		return nil, fmt.Errorf("ops: insert: %w", err)
	}
	op, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.startThumbRepair(op)
	return s.Get(ctx, id)
}

// LatestThumbRepair is the tenant's newest repair, in any state, or nil when
// it has asked for none.
func (s *Service) LatestThumbRepair(ctx context.Context, key string) (*Op, error) {
	return s.latestThumbRepair(ctx, key, false)
}

func (s *Service) latestThumbRepair(ctx context.Context, key string, activeOnly bool) (*Op, error) {
	return s.latestOfKind(ctx, OpThumbRepair, key, activeOnly)
}

// ThumbRepairDone is closed when the run behind op id has ended, or nil when
// this process is not running it.
func (s *Service) ThumbRepairDone(id int64) <-chan struct{} { return doneOf(&s.thumbDone, id) }

// startThumbRepair runs op in its own goroutine, once.
func (s *Service) startThumbRepair(op *Op) { s.startOwnRun(&s.thumbDone, op, s.runThumbRepair) }

// runThumbRepair is one run, from its start to its final row.
func (s *Service) runThumbRepair(ctx, life context.Context, op *Op, ch *cancelHandle) {
	wctx := context.WithoutCancel(ctx)
	res, err := s.db.ExecContext(wctx, s.q(
		`UPDATE pending_ops SET status=?, started_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`),
		StatusRunning, op.ID, StatusPending)
	if err != nil {
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // cancelled before it started; Cancel wrote the row
	}
	job, _, _, _ := op.ThumbRepairOf()
	// The total is counted when the run starts, against the catalogue as it
	// is then: a resumed run counts what is still left.
	if total, cerr := s.thumbRepairer.CountRepair(ctx, job); cerr == nil {
		_, _ = s.db.ExecContext(wctx, s.q(`UPDATE pending_ops SET total=? WHERE id=?`), total, op.ID)
	}
	gate := &progressGate{every: time.Second, now: time.Now}
	counts, err := s.thumbRepairer.RunRepair(ctx, job, func(c ThumbRepairCounts) {
		gate.maybe(func() {
			_, _ = s.db.ExecContext(wctx, s.q(`UPDATE pending_ops SET done=?, failed=?, skipped=? WHERE id=?`),
				c.Processed, c.Failed, c.Skipped, op.ID)
		})
	})
	if err != nil && !ch.cancelled.Load() && life.Err() != nil {
		// The server is stopping. The row stays as it is — requeued at the
		// next boot, where the run starts again over what is left.
		_, _ = s.db.ExecContext(wctx, s.q(`UPDATE pending_ops SET done=?, failed=?, skipped=? WHERE id=?`),
			counts.Processed, counts.Failed, counts.Skipped, op.ID)
		return
	}
	status, msg := StatusOK, ""
	switch {
	case ch.cancelled.Load():
		status = StatusCancelled
	case err != nil && len(counts.Refused) == 0:
		status, msg = StatusFailed, err.Error()
		slog.Warn("ops: thumbnail repair failed", slog.Int64("op", op.ID), slog.String("err", msg))
	case len(counts.Refused) > 0 && counts.Processed > 0:
		status = StatusPartial
	case len(counts.Refused) > 0:
		status = StatusFailed
	case counts.Failed > 0 && counts.OK+counts.Skipped > 0:
		status = StatusPartial
	case counts.Failed > 0:
		status = StatusFailed
	}
	params := thumbParams{mode: job.Mode, path: job.Path, reach: job.Reach, refused: counts.Refused}
	srcJSON, _ := json.Marshal(params.encode())
	slog.Info("thumbnail repair finished",
		slog.Int64("op", op.ID),
		slog.String("status", status),
		slog.Int("processed", counts.Processed),
		slog.Int("ok", counts.OK),
		slog.Int("failed", counts.Failed),
		slog.Int("skipped", counts.Skipped),
		slog.Int("refused", len(counts.Refused)))
	_, _ = s.db.ExecContext(wctx, s.q(
		`UPDATE pending_ops SET status=?, error=?, done=?, failed=?, skipped=?, sources_json=?, finished_at=CURRENT_TIMESTAMP WHERE id=?`),
		status, msg, counts.Processed, counts.Failed, counts.Skipped, string(srcJSON), op.ID)
}

// resumeThumbRepairs starts every OpThumbRepair row a previous process left
// behind (Migrate put them back to pending).
func (s *Service) resumeThumbRepairs(ctx context.Context) {
	if s.thumbRepairer == nil {
		return
	}
	s.resumeKind(ctx, OpThumbRepair, "thumbnail repair", s.startThumbRepair)
}
