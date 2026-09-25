package stall

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"time"
)

// Clock is one operation's time account, shared by its attempts (which run
// one after another, never at once).
type Clock struct {
	P        Policy
	Began    time.Time // when the operation started (for the message)
	Attempts int       // attempts started so far

	start    time.Time     // what the budget counts from (see Failed)
	lastTook time.Duration // how long the last failed attempt ran
}

// NewClock starts an operation's clock.
func NewClock(p Policy) *Clock {
	now := time.Now()
	return &Clock{P: p, Began: now, start: now}
}

// Failed records a failed attempt that started at started. An attempt that
// ran longer than the whole budget was a transfer that moved and then broke —
// a store that is down gives no sign of life for one attempt timeout and is
// cut there — so the budget starts over from its failure, and a 2 GB upload
// that loses its connection near the end is still retried.
//
// ⚠ Not when the attempt ended in silence that a guard cut: a send the store
// stopped taking (SendStall, a minute) or an answer that never came. That
// attempt ran long because the store was silent, and a fresh budget would buy
// the same minute again, MaxAttempts times over.
func (c *Clock) Failed(started time.Time, err error) {
	now := time.Now()
	c.lastTook = now.Sub(started)
	if c.lastTook > c.P.TotalTimeout && !EndedInSilence(err) {
		c.start = now
	}
}

// EndedInSilence: the attempt was cut by a timeout — ours (Error) or the
// network's (a connect, a lookup, a send that timed out) — not broken while
// it moved.
func EndedInSilence(err error) bool {
	var s *Error
	if errors.As(err, &s) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) && op.Timeout()
}

// Room is how long the retryer may still wait before the next attempt so that
// a store that is still down could show it inside the budget. That costs what
// the last attempt did, up to one attempt timeout: a refused port fails in a
// millisecond and may be retried until the budget is spent, a silent store
// costs a whole attempt timeout every time. Negative: no more.
//
// ⚠ Capped at the attempt timeout, not at the longest the last attempt could
// have waited. An upload may wait the attempt timeout plus its send tail (up
// to 16 s) for an answer; counting that tail would leave no room after a long
// upload that broke, and the fresh budget Failed gives it would buy nothing.
func (c *Clock) Room() time.Duration {
	return c.P.TotalTimeout - time.Since(c.start) - min(c.lastTook, c.P.AttemptTimeout)
}

// Do runs attempt until it succeeds, fails for good, or the budget has no
// room for another try. retry says whether a failed attempt may be sent again
// — the caller knows whether the request can be repeated (a refusal cannot
// change, an upload that started sending cannot be replayed); only then do
// MaxAttempts and the budget decide. The pause between attempts is an
// exponential backoff with jitter, shortened so the next attempt still fits.
//
// The clock is returned so the caller can report the attempts and the time.
func (p Policy) Do(ctx context.Context, retry func(error) bool, attempt func(context.Context) error) (*Clock, error) {
	c := NewClock(p)
	for {
		c.Attempts++
		started := time.Now()
		err := attempt(ctx)
		if err == nil {
			return c, nil
		}
		c.Failed(started, err)
		if ctx.Err() != nil || c.Attempts >= p.MaxAttempts || !retry(err) {
			return c, err
		}
		room := c.Room()
		if room < 0 {
			return c, err
		}
		pause := min(backoff(c.Attempts), room)
		t := time.NewTimer(pause)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return c, err
		}
	}
}

// backoff is the pause after the n-th failed attempt: a random time up to
// 2^(n-1) × 200 ms, at most MaxBackoff — the AWS SDK's shape, smaller steps.
func backoff(n int) time.Duration {
	ceil := min(200*time.Millisecond<<min(n-1, 10), MaxBackoff)
	return time.Duration(rand.Int64N(int64(ceil)) + 1)
}
