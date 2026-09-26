package storage

import (
	"context"
	"sync/atomic"
)

// Tally counts the objects a driver call works through.
//
// An object store's folder copy, move or delete is ONE driver call that lists
// the folder and then works through its objects one at a time — for a large
// folder, minutes — and nothing outside the call could see how far it had got:
// the operations centre read "0%" until the whole folder was done. A driver
// that works object by object says so here: Found when it knows how many it
// has before it, Done after each one.
//
// It travels on the context (WithTally), so no driver interface changes and
// neither does any caller between the ops worker and the driver (the trash
// alone has nine). A driver that does not report leaves it untouched.
//
// Nil-safe, and safe for concurrent use: the ops worker runs several deletes
// of one job at once, all into one Tally, which is also why it counts in
// deltas rather than setting totals.
type Tally struct {
	found atomic.Int64
	done  atomic.Int64
	// up receives every count as it happens (TallyUnder).
	up *Tally
}

// TallyUnder returns a tally of its own whose counts also reach parent as they
// happen: a caller that must know what one call counted (to take back what it
// left undone, Forget) without hiding the progress from the tally above.
func TallyUnder(parent *Tally) *Tally {
	return &Tally{up: parent}
}

// Found adds n objects the call now knows it has to work through.
func (t *Tally) Found(n int) {
	if t != nil && n > 0 {
		t.found.Add(int64(n))
		t.up.Found(n)
	}
}

// Done adds n objects the call has finished with.
func (t *Tally) Done(n int) {
	if t != nil && n > 0 {
		t.done.Add(int64(n))
		t.up.Done(n)
	}
}

// Forget takes back n objects Found counted that the call will not work
// through after all: it stopped part-way, and whoever carries on counts what
// is left.
func (t *Tally) Forget(n int) {
	if t != nil && n > 0 {
		t.found.Add(-int64(n))
		t.up.Forget(n)
	}
}

// Load returns how many objects are done out of how many were found.
func (t *Tally) Load() (done, total int64) {
	if t == nil {
		return 0, 0
	}
	return t.done.Load(), t.found.Load()
}

type tallyKey struct{}

// WithTally returns a context whose driver calls count into t.
func WithTally(ctx context.Context, t *Tally) context.Context {
	return context.WithValue(ctx, tallyKey{}, t)
}

// TallyOf returns the context's tally, or nil — on which every method is a
// no-op, so a driver calls it unconditionally.
func TallyOf(ctx context.Context) *Tally {
	t, _ := ctx.Value(tallyKey{}).(*Tally)
	return t
}
