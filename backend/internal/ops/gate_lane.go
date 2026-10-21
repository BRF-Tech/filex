package ops

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/rowgate"
)

// The queue and the row gate (internal/rowgate, sec055).
//
// The main worker runs every tenant's copies, moves, deletes and upload
// commits one job at a time, and the finishing lane every rename, restore and
// purge. A job that moves, deletes, renames or restores holds its storage's
// row gate; while a storage judgement (a directory of a scan, a batch of the
// tombstone pass, a folder of the lazy catalogue) holds that gate, the job
// would wait - and every other storage's jobs, and every other tenant's, with
// it. So a lane asks first (rowgate.Judged): a job whose storage is being
// judged right now goes back to the queue as it was, the lane leaves that
// storage's later jobs where they are for the round (one storage's jobs keep
// their order), and the other storages' run.
//
// ⚠⚠ The job put back is announced to the gate and its lane is woken (issue
// #127). Up to 0.57.4 it was tried again at the lane's next round only - its
// 5 s ticker, or the next job submitted - and a scan holds the gate for one
// directory, lets go of it and takes it again microseconds later for the
// next: every try met a judgement, and on a local storage of tens of
// thousands of entries a delete sat "Queued" for the scan's whole run, minutes,
// then ran two seconds after it. Now (awaitTurn) the job announces itself
// (rowgate.Want), so the scan finishes the directory or batch in hand and takes
// no next one; the lane is woken the moment the judgement lets go
// (rowgate.Await, then the lane's poke) and runs the job, which ends the
// announcement once it holds the gate (endTurn). The announcement lasts
// turnFor at most: a job its lane does not reach in time (busy with another
// storage's long job, or cancelled meanwhile) holds the scan back no longer
// than that. While it waits the row says why (WaitReason, in the reader's
// words in WaitText): the tray shows that instead of a bare "Queued".
//
// A judgement that takes the gate between that question and the job's own
// rowgate.Move costs the job one judgement's wait, and only that: a change
// never waits behind a judgement that is merely waiting, and no new judgement
// starts while a change waits (rowgate).

// errSourceJudged: a cross-storage move found a judgement holding its source
// storage's row gate before a byte travelled (crossTransfer). Not a failure:
// execute puts the row back from that item on (putBackFrom).
var errSourceJudged = errors.New("ops: the source storage is being judged; the move waits for its turn")

// putBackFrom returns a running row to the queue with the items from i on
// (the ones before it are done, and their counts stay on the row), and
// reports whether it did. The row reads as begun when it is claimed again.
// A row that could not be put back is the caller's to run on.
func (s *Service) putBackFrom(ctx context.Context, op *Op, i int) bool {
	rest, err := json.Marshal(op.Sources[i:])
	if err != nil {
		return false
	}
	res, err := s.db.ExecContext(context.WithoutCancel(ctx), s.q(
		`UPDATE pending_ops SET status=?, sources_json=?, done=?, failed=? WHERE id=? AND status=?`),
		StatusPending, string(rest), op.Done, op.Failed, op.ID, StatusRunning)
	if err != nil {
		slog.Warn("ops: put a job back for its storage's row gate", slog.Int64("op", op.ID), slog.String("err", err.Error()))
		return false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false
	}
	op.putBack = true
	slog.Debug("ops: a storage judgement holds the source's row gate; the job's remaining items wait for their turn",
		slog.Int64("op", op.ID), slog.Int64("storage", op.StorageID), slog.Int("left", len(op.Sources)-i))
	return true
}

// takesGate is a kind whose job holds its storage's row gate.
func takesGate(op *Op) bool {
	switch op.Kind {
	case OpMove, OpDelete, OpRename, OpRestore:
		return true
	}
	return false
}

// waitsForAJudgement reports whether op would wait for a judgement holding
// its storage's row gate if it ran now.
func waitsForAJudgement(op *Op) bool {
	return op != nil && takesGate(op) && rowgate.Judged(op.StorageID)
}

// putBack returns a claimed job to the queue as it was before the claim: it
// is waiting again, and a job claimed for the first time does not read as
// begun (claimWhere's resumed).
func (s *Service) putBack(ctx context.Context, op *Op) {
	q := `UPDATE pending_ops SET status=?, started_at=NULL WHERE id=? AND status=?`
	if op.resumed {
		q = `UPDATE pending_ops SET status=? WHERE id=? AND status=?`
	}
	if _, err := s.db.ExecContext(context.WithoutCancel(ctx), s.q(q), StatusPending, op.ID, StatusRunning); err != nil {
		slog.Warn("ops: put a job back for its storage's row gate", slog.Int64("op", op.ID), slog.String("err", err.Error()))
		return
	}
	op.Status = StatusPending
}

// holdBack puts op back for the judgement holding its storage's gate (putBack)
// and announces it (announce), so wake - its lane's poke - runs it as soon as
// that judgement lets go.
func (s *Service) holdBack(ctx context.Context, op *Op, wake func()) {
	s.putBack(ctx, op)
	s.announce(op, wake)
}

// WaitStorageScan is a queued row's WaitReason while a judgement of its
// storage (a scan's directory, a tombstone batch, a lazy folder) holds the row
// gate the job needs: it runs as soon as that judgement lets go.
const WaitStorageScan = "storage_scan"

// turnFor is how long a job put back for a judgement keeps new judgements of
// its storage back (rowgate.Want). The lane is woken the moment the judgement
// in hand lets go and normally runs the job within milliseconds; the bound is
// for a lane that is busy elsewhere. Under the storage sync's GateWait (30 s),
// past which a judgement that stepped back defers the whole scan. Atomic so a
// test can shorten it while another test's lane is still winding down.
var turnFor = func() *atomic.Int64 {
	var d atomic.Int64
	d.Store(int64(rowgate.WantFor))
	return &d
}()

// turn is one put-back job's turn at its storage's gate: end calls off its
// announcement (rowgate.Want) and the watcher that wakes its lane.
type turn struct {
	end func()
}

// announce tells op's storage gate that op, just put back, wants it
// (rowgate.Want), and wakes op's lane (wake) once the judgement holding the
// gate lets go (rowgate.Await): the job runs before the next judgement starts
// instead of at the lane's next tick. One turn per job: a job put back again
// replaces its turn.
//
// The announcement runs out after turnFor (rowgate.Want's own timer); the
// watcher does not: a judgement that holds the gate longer than that (one
// huge directory) still wakes the lane when it lets go. The watcher ends with
// the turn (endTurn), once it has woken the lane and turnFor has passed, or
// when the service stops - so at most one per job, never past its job. The
// row says why it waits (attachWait) for as long as the turn stands.
//
// The first time a job is put back is said at INFO, once per job: a scan holds
// a gate for a moment many times a minute, and saying every round of it would
// drown the log.
func (s *Service) announce(op *Op, wake func()) {
	if old, ok := s.turns.Load(op.ID); ok {
		old.(*turn).end()
	}
	lasts := time.Duration(turnFor.Load())
	cancelWant := rowgate.Want(op.StorageID, lasts)
	runsOut := time.Now().Add(lasts)
	ctx, stopWatch := context.WithCancel(s.lifeCtx())
	t := &turn{}
	var once sync.Once
	t.end = func() {
		once.Do(func() {
			cancelWant()
			stopWatch()
		})
	}
	s.turns.Store(op.ID, t)
	if _, again := s.heldSince.LoadOrStore(op.ID, time.Now()); !again {
		slog.Info("ops: a scan of the storage holds its row gate; the job waits for its turn and runs once the scan lets go",
			slog.Int64("op", op.ID), slog.Int64("storage", op.StorageID), slog.String("kind", op.Kind))
	} else {
		slog.Debug("ops: a scan of the storage holds its row gate again; the job waits for its turn",
			slog.Int64("op", op.ID), slog.Int64("storage", op.StorageID), slog.String("kind", op.Kind))
	}
	storageID := op.StorageID
	id := op.ID
	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		if rowgate.Await(ctx, storageID) == nil {
			wake()
		}
		left := time.NewTimer(time.Until(runsOut))
		defer left.Stop()
		select {
		case <-ctx.Done():
		case <-left.C:
		}
		if s.turns.CompareAndDelete(id, t) {
			t.end()
		}
	}()
}

// endTurn ends job id's turn, if it has one: the job holds its storage's gate
// (or its fence) now, has started without needing it, has ended, or was
// cancelled. Its announcement is called off at once, so the scan goes on.
func (s *Service) endTurn(id int64) {
	if v, ok := s.turns.LoadAndDelete(id); ok {
		v.(*turn).end()
	}
}

// forgetHeld drops what job id's turns left behind (heldSince) once the job
// has ended or was cancelled.
func (s *Service) forgetHeld(id int64) {
	s.endTurn(id)
	s.heldSince.Delete(id)
}

// attachWait says on a queued row why it has not started (WaitReason, and
// since when it waits: WaitingSince) while its job waits for its turn at its
// storage's gate. Live, from the worker's memory like the byte counters, never
// stored; the reader's sentence for it is sayRows'.
func (s *Service) attachWait(op *Op) {
	if op == nil || op.Status != StatusPending {
		return
	}
	if _, ok := s.turns.Load(op.ID); !ok {
		return
	}
	op.WaitReason = WaitStorageScan
	if v, ok := s.heldSince.Load(op.ID); ok {
		since := v.(time.Time)
		op.WaitingSince = &since
	}
}

// skipping is a lane's claim condition with the storages put back this round
// left out, as source or as destination.
func skipping(cond string, held []int64) string {
	if len(held) == 0 {
		return cond
	}
	ids := make([]string, len(held))
	for i, id := range held {
		ids[i] = strconv.FormatInt(id, 10)
	}
	in := strings.Join(ids, ",")
	return cond + ` AND storage_id NOT IN (` + in + `) AND COALESCE(dest_storage_id, 0) NOT IN (` + in + `)`
}
