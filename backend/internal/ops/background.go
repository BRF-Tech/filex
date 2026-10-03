package ops

// The machinery shared by the ops kinds that run in a goroutine of their own
// rather than on the queue's single worker: "empty the trash now"
// (trash_empty.go) and "repair thumbnails" (thumb_repair.go). One copy, so a
// fix to how such a run starts, is followed, is found again after a restart
// or is cancelled reaches both.

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
)

// latestOfKind is the newest row of kind asked for under key (a tenant key,
// kept in `dest`), or nil when there is none. activeOnly narrows it to one
// still queued or running.
func (s *Service) latestOfKind(ctx context.Context, kind, key string, activeOnly bool) (*Op, error) {
	q := `SELECT id FROM pending_ops WHERE kind=? AND COALESCE(dest,'')=?`
	if activeOnly {
		q += ` AND status IN ('pending','running')`
	}
	q += ` ORDER BY id DESC LIMIT 1`
	var id int64
	if err := s.db.QueryRowContext(ctx, s.q(q), kind, key).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return s.Get(ctx, id)
}

// doneOf is the channel closed when the run of op id ends, from the run
// table of its kind, or nil when this process is not running it.
func doneOf(runs *sync.Map, id int64) <-chan struct{} {
	if v, ok := runs.Load(id); ok {
		return v.(chan struct{})
	}
	return nil
}

// startOwnRun runs op in a goroutine of its own, once (runs is its kind's
// table of runs in flight): cancellable through Cancel, stopped with the
// service's life, and closing its done channel when it ends.
func (s *Service) startOwnRun(runs *sync.Map, op *Op, run func(ctx, life context.Context, op *Op, ch *cancelHandle)) {
	done := make(chan struct{})
	if _, dup := runs.LoadOrStore(op.ID, done); dup {
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
		defer runs.Delete(op.ID)
		defer close(done)
		defer s.cancels.Delete(op.ID)
		defer cancel()
		run(ctx, life, op, ch)
	}()
}

// resumeKind starts every row of kind a previous process left behind
// (Migrate put them back to pending), oldest first.
func (s *Service) resumeKind(ctx context.Context, kind, what string, start func(*Op)) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id FROM pending_ops WHERE kind=? AND status=? ORDER BY id ASC`),
		kind, StatusPending)
	if err != nil {
		slog.Warn("ops: resume "+what, slog.String("err", err.Error()))
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
		slog.Info("ops: resuming an interrupted "+what, slog.Int64("op", id))
		start(op)
	}
}
