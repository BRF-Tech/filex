package ops

// "Empty the trash now" as an ops job.
//
// ⚠⚠ Why here (v0.43.0, PR #47). The purge used to run inside
// POST /api/admin/trash/empty, and a large trash cannot be purged inside any
// request: 61,844 files needed the better part of an hour, nginx cut the
// request at sixty seconds, and the admin — who saw nothing — pressed again,
// three purges over the same rows. The contributor moved the run into the
// background with its own in-memory progress; it lives here instead, as a row
// of this queue like every other long operation, so it is in the explorer's
// operations centre and the admin tray, it is cancelled the way every op is
// (POST /api/files/ops/{id}/cancel), it is scoped to the tenant that asked for
// it, and a restart does not forget it — the row is requeued at boot and the
// run carries on from where it stopped.
//
// Two things make it unlike the other kinds:
//
//   - It does not take the queue's single worker. A purge of a large trash
//     takes as long as the trash does; run in the serial drain it would hold
//     every copy, move, delete and staged-upload commit on the instance for
//     that long. Each run has its own goroutine, and the trash package's sweep
//     lock (not this queue) is what keeps two purges off the same rows.
//   - It names no files. Its row stores what it was asked (the day count and
//     the storages it may reach) instead of sources, and the tenant it belongs
//     to instead of a destination; none of that is shown to anyone — the
//     answers carry the counts only.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// OpTrashEmpty is "empty the trash now" (see the file comment).
const OpTrashEmpty = "trash-empty"

// TrashEmptier runs the purge behind an OpTrashEmpty row. *trash.Service
// implements it; injected with SetTrashEmptier.
type TrashEmptier interface {
	Tally(ctx context.Context, job trash.EmptyJob) (count int, bytes int64, err error)
	RunEmpty(ctx context.Context, job trash.EmptyJob, started func() bool, progress func(trash.PurgeResult)) (trash.PurgeResult, error)
}

// SetTrashEmptier wires the purge. Without it SubmitTrashEmpty refuses.
func (s *Service) SetTrashEmptier(e TrashEmptier) { s.trashEmptier = e }

// ErrTrashBusy is SubmitTrashEmpty's answer while the caller's tenant already
// has an empty queued or running; the op returned beside it is that one.
var ErrTrashBusy = errors.New("ops: the trash is already being emptied")

// TrashEmptyRequest is what an admin asked to empty.
type TrashEmptyRequest struct {
	// StorageID narrows the run to one storage; 0 is every storage in Reach.
	StorageID int64
	// OlderThanDays: 0 is everything that is in the trash when it is asked.
	OlderThanDays int
	// Reach is the storages the caller may empty (trash.Reach): nil every one.
	Reach []int64
	// Tenant is TenantKey of the caller: whose run it is.
	Tenant string
}

// TenantKey names whose "empty the trash" a caller's is: its tenant, or ""
// for a caller that reaches every storage (single-tenant, the supertenant).
// A run is shown to, followed by and refused to the callers with the same key.
func TenantKey(ctx context.Context) string {
	if sc, ok := tenant.FromContext(ctx); ok && sc != nil && !sc.IsSupertenant {
		return "tenant:" + strconv.FormatInt(sc.ProviderID, 10)
	}
	return ""
}

// trashParams is what an OpTrashEmpty row stores in sources_json.
type trashParams struct {
	days  int
	reach []int64 // nil: every storage
}

func (p trashParams) encode() []string {
	out := []string{"days=" + strconv.Itoa(p.days)}
	if p.reach != nil {
		ids := make([]string, len(p.reach))
		for i, id := range p.reach {
			ids[i] = strconv.FormatInt(id, 10)
		}
		out = append(out, "reach="+strings.Join(ids, ","))
	}
	return out
}

// decodeTrashParams reads encode's form back. A row it cannot read reaches
// NOTHING: the failure mode of a purge must be too narrow, never too wide.
func decodeTrashParams(srcs []string) (trashParams, error) {
	var p trashParams
	seenDays := false
	for _, kv := range srcs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return trashParams{reach: []int64{}}, fmt.Errorf("ops: trash-empty: bad parameter %q", kv)
		}
		switch k {
		case "days":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return trashParams{reach: []int64{}}, fmt.Errorf("ops: trash-empty: bad days %q", v)
			}
			p.days, seenDays = n, true
		case "reach":
			p.reach = []int64{}
			if v == "" {
				continue
			}
			for _, part := range strings.Split(v, ",") {
				id, err := strconv.ParseInt(part, 10, 64)
				if err != nil || id <= 0 {
					return trashParams{reach: []int64{}}, fmt.Errorf("ops: trash-empty: bad reach %q", v)
				}
				p.reach = append(p.reach, id)
			}
		default:
			return trashParams{reach: []int64{}}, fmt.Errorf("ops: trash-empty: unknown parameter %q", k)
		}
	}
	if !seenDays {
		return trashParams{reach: []int64{}}, errors.New("ops: trash-empty: no day count")
	}
	return p, nil
}

// TrashEmptyOf reads an OpTrashEmpty row's request back: the day count, the
// storage it names (0 = all in reach), and whose it is. ok is false for any
// other kind of row.
func (op *Op) TrashEmptyOf() (days int, storageID int64, tenantKey string, ok bool) {
	if op == nil || op.Kind != OpTrashEmpty {
		return 0, 0, "", false
	}
	return op.trash.days, op.StorageID, op.tenant, true
}

// trashJob is the purge an OpTrashEmpty row stands for.
//
// ⚠⚠ The cutoff is the row's created_at — the moment the admin asked, on the
// DATABASE's clock, the same clock that stamped every deleted_at it is
// compared with (and the same spelling: the value comes back from the column
// through the same driver). Nothing deleted after the ask is purged, however
// long the run takes and whether or not a restart interrupts it.
func trashJob(op *Op) trash.EmptyJob {
	return trash.EmptyJob{
		Before:    trash.EmptyCutoff(op.CreatedAt, op.trash.days),
		StorageID: op.StorageID,
		Reach:     op.trash.reach,
	}
}

// SubmitTrashEmpty queues "empty the trash now" and starts it.
//
// While the caller's tenant already has one queued or running, nothing is
// queued: the answer is ErrTrashBusy with that run. A run of ANOTHER tenant
// (or of the platform, or the nightly retention sweep) does not refuse this
// one — it waits its turn behind it, queued, and says so.
func (s *Service) SubmitTrashEmpty(ctx context.Context, req TrashEmptyRequest) (*Op, error) {
	if s.trashEmptier == nil {
		return nil, errors.New("ops: no trash emptier wired")
	}
	if req.OlderThanDays < 0 {
		return nil, errors.New("ops: trash-empty: negative day count")
	}
	s.trashMu.Lock()
	defer s.trashMu.Unlock()
	if cur, err := s.activeTrashEmpty(ctx, req.Tenant); err != nil {
		return nil, err
	} else if cur != nil {
		return cur, ErrTrashBusy
	}
	params := trashParams{days: req.OlderThanDays, reach: req.Reach}
	srcJSON, _ := json.Marshal(params.encode())
	id, err := s.insertOp(ctx, OpTrashEmpty, req.StorageID, req.StorageID, string(srcJSON), req.Tenant, 0)
	if err != nil {
		return nil, fmt.Errorf("ops: insert: %w", err)
	}
	op, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// Counted against the row's own cutoff, so the total and the purge agree
	// on what "in the trash" meant.
	n, b, err := s.trashEmptier.Tally(ctx, trashJob(op))
	if err != nil {
		s.fail(context.WithoutCancel(ctx), op, err.Error())
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, s.q(`UPDATE pending_ops SET total=? WHERE id=?`), n, id); err != nil {
		return nil, err
	}
	op.Total = n
	lp := &liveProgress{}
	lp.total.Store(b)
	s.live.Store(id, lp)
	s.startTrashEmpty(op)
	return s.Get(ctx, id)
}

// activeTrashEmpty is the tenant's queued or running empty, or nil.
func (s *Service) activeTrashEmpty(ctx context.Context, key string) (*Op, error) {
	return s.latestTrashEmpty(ctx, key, true)
}

// LatestTrashEmpty is the tenant's newest "empty the trash", in any state, or
// nil when it has asked for none.
func (s *Service) LatestTrashEmpty(ctx context.Context, key string) (*Op, error) {
	return s.latestTrashEmpty(ctx, key, false)
}

func (s *Service) latestTrashEmpty(ctx context.Context, key string, activeOnly bool) (*Op, error) {
	q := `SELECT id FROM pending_ops WHERE kind=? AND COALESCE(dest,'')=?`
	if activeOnly {
		q += ` AND status IN ('pending','running')`
	}
	q += ` ORDER BY id DESC LIMIT 1`
	var id int64
	if err := s.db.QueryRowContext(ctx, s.q(q), OpTrashEmpty, key).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return s.Get(ctx, id)
}

// TrashEmptyDone is closed when the run behind op id has ended, or nil when
// this process is not running it.
func (s *Service) TrashEmptyDone(id int64) <-chan struct{} {
	if v, ok := s.trashDone.Load(id); ok {
		return v.(chan struct{})
	}
	return nil
}

// startTrashEmpty runs op in its own goroutine (see the file comment), once.
func (s *Service) startTrashEmpty(op *Op) {
	done := make(chan struct{})
	if _, dup := s.trashDone.LoadOrStore(op.ID, done); dup {
		return
	}
	life := s.lifeCtx()
	ctx, cancel := context.WithCancel(life)
	ch := &cancelHandle{}
	ch.cancel = func() { ch.cancelled.Store(true); cancel() }
	s.cancels.Store(op.ID, ch)
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer s.trashDone.Delete(op.ID)
		defer close(done)
		defer s.cancels.Delete(op.ID)
		defer cancel()
		s.runTrashEmpty(ctx, life, op, ch)
	}()
}

// runTrashEmpty is one run, from its turn to its final row.
func (s *Service) runTrashEmpty(ctx, life context.Context, op *Op, ch *cancelHandle) {
	lp := s.liveFor(op.ID)
	if lp == nil {
		lp = &liveProgress{}
		s.live.Store(op.ID, lp)
	}
	// A row resumed after a restart keeps what it had counted before.
	baseDone, baseFailed := op.Done, op.Failed
	wctx := context.WithoutCancel(ctx)
	gate := &progressGate{every: time.Second, now: time.Now}
	started := func() bool {
		res, err := s.db.ExecContext(wctx, s.q(
			`UPDATE pending_ops SET status=?, started_at=CURRENT_TIMESTAMP WHERE id=? AND status=?`),
			StatusRunning, op.ID, StatusPending)
		if err != nil {
			return false
		}
		n, _ := res.RowsAffected()
		return n > 0
	}
	res, err := s.trashEmptier.RunEmpty(ctx, trashJob(op), started, func(r trash.PurgeResult) {
		lp.done.Store(r.Bytes)
		gate.maybe(func() {
			_, _ = s.db.ExecContext(wctx, s.q(`UPDATE pending_ops SET done=?, failed=? WHERE id=?`),
				baseDone+r.Scanned, baseFailed+r.Failed, op.ID)
		})
	})
	if errors.Is(err, trash.ErrNotStarted) {
		return // withdrawn while it waited; Cancel already wrote the row
	}
	done, failed := baseDone+res.Scanned, baseFailed+res.Failed
	if err != nil && !ch.cancelled.Load() && life.Err() != nil {
		// The server is stopping. The row stays as it is — requeued at the
		// next boot, where the run carries on from what is left.
		_, _ = s.db.ExecContext(wctx, s.q(`UPDATE pending_ops SET done=?, failed=? WHERE id=?`), done, failed, op.ID)
		return
	}
	status, msg := StatusOK, ""
	switch {
	case ch.cancelled.Load():
		status = StatusCancelled
	case err != nil:
		status, msg = StatusFailed, err.Error()
		slog.Warn("ops: trash empty failed", slog.Int64("op", op.ID), slog.String("err", msg))
	case failed > 0 && done > failed:
		status, msg = StatusPartial, fmt.Sprintf("%d of %d items could not be purged; see the server log", failed, done)
	case failed > 0:
		status, msg = StatusFailed, fmt.Sprintf("%d items could not be purged; see the server log", failed)
	}
	slog.Info("trash empty finished",
		slog.Int64("op", op.ID),
		slog.String("status", status),
		slog.Int("total", op.Total),
		slog.Int("scanned", done),
		slog.Int("failed", failed),
		slog.Int64("bytes", res.Bytes))
	_, _ = s.db.ExecContext(wctx, s.q(
		`UPDATE pending_ops SET status=?, error=?, done=?, failed=?, finished_at=CURRENT_TIMESTAMP WHERE id=?`),
		status, msg, done, failed, op.ID)
}

// resumeTrashEmpties starts every OpTrashEmpty row a previous process left
// behind (Migrate put them back to pending).
func (s *Service) resumeTrashEmpties(ctx context.Context) {
	if s.trashEmptier == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id FROM pending_ops WHERE kind=? AND status=? ORDER BY id ASC`),
		OpTrashEmpty, StatusPending)
	if err != nil {
		slog.Warn("ops: resume trash empties", slog.String("err", err.Error()))
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	_ = rows.Close()
	for _, id := range ids {
		op, err := s.Get(ctx, id)
		if err != nil {
			continue
		}
		slog.Info("ops: resuming an interrupted trash empty", slog.Int64("op", id))
		s.startTrashEmpty(op)
	}
}

// Viewer is who is looking at the queue: every row (All), or the rows of
// these storages plus the trash empties of this tenant.
type Viewer struct {
	All        bool
	StorageIDs []int64
	Tenant     string
}

// ViewerOf is the viewer a request is: unscoped callers and the supertenant
// see every row; a tenant its storages' rows and its own trash empties.
func ViewerOf(ctx context.Context) Viewer {
	sc, ok := tenant.FromContext(ctx)
	if !ok || sc == nil || sc.IsSupertenant {
		return Viewer{All: true}
	}
	return Viewer{StorageIDs: append([]int64{}, sc.StorageIDs...), Tenant: TenantKey(ctx)}
}

// Sees reports whether v may see (and so follow or cancel) op. An op is a
// tenant's when either of its storages is; a trash empty, which may name no
// storage at all, is the tenant's that asked for it.
func (v Viewer) Sees(op *Op) bool {
	if v.All {
		return true
	}
	if op == nil {
		return false
	}
	if op.Kind == OpTrashEmpty && op.tenant != "" && op.tenant == v.Tenant {
		return true
	}
	for _, id := range v.StorageIDs {
		if id != 0 && (id == op.StorageID || id == op.DestStorageID) {
			return true
		}
	}
	return false
}

// lifeCtx is the context background runs live in; Stop ends it.
func (s *Service) lifeCtx() context.Context {
	s.lifeMu.Lock()
	defer s.lifeMu.Unlock()
	if s.life == nil {
		s.life, s.lifeCancel = context.WithCancel(context.Background())
	}
	return s.life
}

// stopBackground ends the background runs and waits for them; the rows stay
// as they are, for the next boot to resume.
func (s *Service) stopBackground() {
	s.lifeMu.Lock()
	if s.lifeCancel != nil {
		s.lifeCancel()
	}
	s.life, s.lifeCancel = nil, nil
	s.lifeMu.Unlock()
	s.bg.Wait()
}
