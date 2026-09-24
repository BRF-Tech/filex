package main

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/filesync"
)

// feed is a fake `action=changes`: it counts calls and answers from its fields.
type feed struct {
	calls   int
	cursor  string
	changed bool
	err     error
}

func (f *feed) ask(since string) (string, bool, error) {
	f.calls++
	if f.err != nil {
		return "", false, f.err
	}
	if since == "" {
		return f.cursor, true, nil
	}
	return f.cursor, f.changed, nil
}

func fixedFP(fp string) func() (string, error) { return func() (string, error) { return fp, nil } }

var t0 = time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

func newPlanner() *watchPlanner {
	return newWatchPlanner(30*time.Second, 5*time.Minute, 30*time.Minute)
}

func settle(w *watchPlanner, p filesync.Pair, at time.Time, cursor string, planned int, errs ...string) {
	w.ran(p, at, cursor, filesync.Result{Planned: planned, Errors: errs, LocalFingerprint: "fp1"}, nil, "fp1")
}

func TestWatch_AFirstLookAlwaysRuns(t *testing.T) {
	w, f := newPlanner(), &feed{cursor: "e.1"}
	d, err := w.decide(filesync.Pair{ID: "p"}, t0, fixedFP("fp1"), f.ask)
	require.NoError(t, err)
	require.True(t, d.run)
	require.Equal(t, "e.1", d.cursor, "the cursor is taken BEFORE the run, so changes during it are seen next time")
}

// The whole point: a quiet pair on a server that can report changes costs one
// request per tick, not a walk of its tree.
func TestWatch_AQuietPairIsSkippedForOneRequest(t *testing.T) {
	w, f := newPlanner(), &feed{cursor: "e.1"}
	p := filesync.Pair{ID: "p"}
	settle(w, p, t0, "e.1", 0)

	for i := 1; i <= 10; i++ {
		d, err := w.decide(p, t0.Add(time.Duration(i)*30*time.Second), fixedFP("fp1"), f.ask)
		require.NoError(t, err)
		require.False(t, d.run, "tick %d", i)
	}
	require.Equal(t, 10, f.calls, "one changes request per tick, nothing else")
}

func TestWatch_AServerChangeRunsWithTheNewCursor(t *testing.T) {
	w, f := newPlanner(), &feed{cursor: "e.9", changed: true}
	p := filesync.Pair{ID: "p"}
	settle(w, p, t0, "e.1", 0)

	d, err := w.decide(p, t0.Add(30*time.Second), fixedFP("fp1"), f.ask)
	require.NoError(t, err)
	require.True(t, d.run)
	require.Equal(t, "e.9", d.cursor)
	require.Equal(t, 1, f.calls)
}

func TestWatch_ALocalChangeRuns(t *testing.T) {
	w, f := newPlanner(), &feed{cursor: "e.1"}
	p := filesync.Pair{ID: "p"}
	settle(w, p, t0, "e.1", 0)

	d, err := w.decide(p, t0.Add(30*time.Second), fixedFP("fp2"), f.ask)
	require.NoError(t, err)
	require.True(t, d.run)
	require.Equal(t, "local change", d.why)
}

// Changes that do not pass through the server's change log (bytes written
// straight into the backend and found by a scan) are caught by a slow full
// walk.
func TestWatch_TheSafetyNetRunsEvenWhenNothingSeemsToChange(t *testing.T) {
	w, f := newPlanner(), &feed{cursor: "e.1"}
	p := filesync.Pair{ID: "p"}
	settle(w, p, t0, "e.1", 0)

	d, err := w.decide(p, t0.Add(30*time.Minute), fixedFP("fp1"), f.ask)
	require.NoError(t, err)
	require.True(t, d.run)
}

// An older server has no change log: the watcher still walks, but a quiet
// pair's walks back off from the tick to --watch-max.
func TestWatch_WithoutAChangeLogAQuietPairBacksOff(t *testing.T) {
	w, f := newPlanner(), &feed{err: cliclient.ErrChangesUnsupported}
	p := filesync.Pair{ID: "p"}
	var gaps []time.Duration
	last := t0
	settle(w, p, t0, "", 0)
	for now := t0; now.Sub(t0) < 20*time.Minute; now = now.Add(30 * time.Second) {
		d, err := w.decide(p, now, fixedFP("fp1"), f.ask)
		require.NoError(t, err)
		if d.run {
			gaps = append(gaps, now.Sub(last))
			last = now
			settle(w, p, now, d.cursor, 0)
		}
	}
	require.Equal(t, []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 5 * time.Minute, 5 * time.Minute}, gaps[:5])
}

func TestWatch_AnyWorkResetsTheBackoff(t *testing.T) {
	w, f := newPlanner(), &feed{err: cliclient.ErrChangesUnsupported}
	p := filesync.Pair{ID: "p"}
	settle(w, p, t0, "", 0)
	settle(w, p, t0.Add(time.Minute), "", 0)
	settle(w, p, t0.Add(3*time.Minute), "", 5) // this run moved files
	d, err := w.decide(p, t0.Add(3*time.Minute+30*time.Second), fixedFP("fp1"), f.ask)
	require.NoError(t, err)
	require.True(t, d.run, "after a busy round the next check is one tick away")
}

// A pair whose last round failed is retried, but not every tick forever.
func TestWatch_AFailingPairIsRetriedWithBackoff(t *testing.T) {
	w, f := newPlanner(), &feed{cursor: "e.1"}
	p := filesync.Pair{ID: "p"}
	settle(w, p, t0, "e.1", 1, "upload a.txt: HTTP 500")

	d, _ := w.decide(p, t0.Add(30*time.Second), fixedFP("fp1"), f.ask)
	require.False(t, d.run, "the first retry waits one doubled interval")
	d, _ = w.decide(p, t0.Add(time.Minute), fixedFP("fp1"), f.ask)
	require.True(t, d.run)
	require.Equal(t, "retry", d.why)
}

// A pair changed in pairs.json (paused/unpaused, confirmed, moved) runs at
// once. The engine's own bookkeeping (Held) does not count as a change.
func TestWatch_AnEditedPairRunsAtOnce(t *testing.T) {
	w, f := newPlanner(), &feed{cursor: "e.1"}
	p := filesync.Pair{ID: "p", HoldNew: true, Held: 3}
	settle(w, p, t0, "e.1", 0)

	p.Held = 4
	d, _ := w.decide(p, t0.Add(30*time.Second), fixedFP("fp1"), f.ask)
	require.False(t, d.run, "a new held count is the engine's own note, not an edit")

	p.HoldNew, p.Held = false, 0 // `filex sync confirm`
	d, _ = w.decide(p, t0.Add(30*time.Second), fixedFP("fp1"), f.ask)
	require.True(t, d.run)
}

func TestWatch_A401FromTheChangeLogStopsTheWatcher(t *testing.T) {
	w := newPlanner()
	p := filesync.Pair{ID: "p"}
	settle(w, p, t0, "e.1", 0)
	f := &feed{err: &cliclient.APIError{Status: 401, Message: "unauthorized"}}
	_, err := w.decide(p, t0.Add(30*time.Second), fixedFP("fp1"), f.ask)
	require.True(t, cliclient.IsUnauthorized(err))
	require.True(t, errors.As(err, new(*cliclient.APIError)))
}
