package coedit

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
)

// SweepEvery is how often Run sweeps.
const SweepEvery = 15 * time.Second

// Sweep drops the members whose page went away (unseen for MemberTTL: a
// leave entry, the lease goes, every bridge drops their locks) and forgets
// the sessions nobody is in any more: a saved one after SavedIdle, one with
// unsaved changes after UnsavedRetention (30 days - for the next person to
// open the file to see and save them), one that never got its key or base
// after NotReadyTTL. It returns the ids of the sessions it forgot.
func (r *Relay) Sweep(ctx context.Context) []string {
	now := r.nowMs()
	stale, err := r.store.ListStaleCoEditMembers(ctx, now-r.opts.MemberTTL.Milliseconds(), 200)
	if err != nil {
		slog.Warn("coedit: stale members not read", slog.String("err", err.Error()))
	}
	for _, m := range stale {
		if err := r.Leave(ctx, m.SID, m.Client, "timeout"); err != nil && !errors.Is(err, ErrNotMember) && !errors.Is(err, ErrNoSession) {
			slog.Warn("coedit: a member whose page went away was not dropped",
				slog.String("sid", m.SID), slog.String("client", m.Client), slog.String("err", err.Error()))
		}
	}
	idle, err := r.store.ListIdleCoEditSessions(ctx, 200)
	if err != nil {
		slog.Warn("coedit: idle sessions not read", slog.String("err", err.Error()))
		return nil
	}
	var out []string
	for _, s := range idle {
		if !r.forgettable(s, now) {
			continue
		}
		forgot := false
		err := r.change(ctx, s.SID, func(ctx context.Context, cur *model.CoEditSession) (bool, error) {
			// Judged again on the row as it is now: somebody may have joined.
			if cur.Members > 0 || !r.forgettable(cur, now) {
				return false, nil
			}
			// Claimed by compare-and-set before it goes: a process joining
			// it at this moment either wins (and this sweep reads it again)
			// or loses (and starts a new session).
			ok, err := r.store.UpdateCoEditSession(ctx, cur, cur.Rev)
			if err != nil {
				return false, err
			}
			if !ok {
				return false, errLost
			}
			forgot = true
			return false, r.store.DeleteCoEditSession(ctx, s.SID)
		})
		if err != nil && !errors.Is(err, ErrNoSession) {
			slog.Warn("coedit: an idle session was not forgotten", slog.String("sid", s.SID), slog.String("err", err.Error()))
			continue
		}
		if forgot {
			r.dropBlobs(s.SID)
			r.hub.end(s.SID, "", "closed")
			r.cursors.forget(s.SID, "")
			out = append(out, s.SID)
		}
	}
	r.cursors.prune(r.opts.Now())
	return out
}

func (r *Relay) forgettable(s *model.CoEditSession, now int64) bool {
	if s.Members > 0 {
		return false
	}
	switch {
	case !ready(s):
		return now-s.CreatedMs >= r.opts.NotReadyTTL.Milliseconds()
	case s.ChangesHead > s.SavedThrough:
		return now-s.ActiveMs >= r.opts.UnsavedRetention.Milliseconds()
	default:
		return now-s.ActiveMs >= r.opts.SavedIdle.Milliseconds()
	}
}

// Run sweeps every SweepEvery until ctx ends.
func (r *Relay) Run(ctx context.Context) {
	t := time.NewTicker(SweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Sweep(ctx)
		}
	}
}
