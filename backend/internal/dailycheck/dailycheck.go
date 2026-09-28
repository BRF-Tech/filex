// Package dailycheck is the daily beat the update checks run on — the app
// plugins' (wasmplugin/updates.go) and the storage plugins' (plugin/updates.go):
// a check a day after the last one, never sooner than a short wait after
// start, with the time of the last run kept in the settings table so a
// restart neither skips a day nor checks at every boot.
//
// ⚠ One beat, not two: both checks had their own copy of this loop until the
// duplication gate caught the second (web/tests/quality/duplication.test.ts).
package dailycheck

import (
	"context"
	"strings"
	"time"
)

// Settings is the part of the store the beat reads.
type Settings interface {
	GetSetting(ctx context.Context, key string) (string, error)
}

// Beat is one daily check's schedule.
type Beat struct {
	Store Settings
	// Key is the settings key holding the last run (RFC 3339).
	Key string
	// First is the least wait after start; Interval the time between runs.
	First, Interval time.Duration
	// Now is the clock (time.Now when nil).
	Now func() time.Time
}

// Last is when the check last ran; zero when never.
func (b Beat) Last(ctx context.Context) time.Time {
	v, err := b.Store.GetSetting(ctx, b.Key)
	if err != nil {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}
	}
	return t
}

// Wait is how long until the next run: Interval after the last, and never
// less than First.
func (b Beat) Wait(ctx context.Context) time.Duration {
	last := b.Last(ctx)
	if last.IsZero() {
		return b.First
	}
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	if d := last.Add(b.Interval).Sub(now()); d > b.First {
		return d
	}
	return b.First
}

// Run calls check each time the beat comes round, until ctx ends. A failed
// check is handed to failed (when ctx is still alive) and the beat goes on.
func (b Beat) Run(ctx context.Context, check func(context.Context) error, failed func(error)) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(b.Wait(ctx)):
		}
		if err := check(ctx); err != nil && ctx.Err() == nil && failed != nil {
			failed(err)
		}
	}
}
