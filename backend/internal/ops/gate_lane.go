package ops

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"

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
// their order), and the other storages' run. The lane's next round - its
// ticker, or the next job submitted - tries the storage again.
//
// A judgement that takes the gate between that question and the job's own
// rowgate.Move costs the job one judgement's wait, and only that: since 0.55 a
// change never waits behind a judgement that is merely waiting (rowgate).

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
	slog.Debug("ops: a storage judgement holds the source's row gate; the job's remaining items wait for the next round",
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
	slog.Debug("ops: a storage judgement holds the row gate; the job waits for the next round",
		slog.Int64("op", op.ID), slog.Int64("storage", op.StorageID), slog.String("kind", op.Kind))
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
