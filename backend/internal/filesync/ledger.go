package filesync

import (
	"context"
	"path"
	"time"
)

// ── the ledger: what the baseline is allowed to learn from a pass ────────
//
// The baseline says "on both sides this path was X when we last agreed". It
// used to be REBUILT after every run from a fresh walk of both sides — every
// path present on both sides went in as agreed. That was only right as long
// as nothing changed while the run was going, and a run is not instantaneous:
//
//   - a browser save that landed after the listing but before the settle walk
//     went into the baseline as "agreed" with the old local file, so it was
//     never downloaded at all (measured on this engine before the change: web
//     edit during a run → local file still old, pair reported "in step");
//   - a failed download of a CHANGED file did the same — present on both
//     sides, so recorded as agreed, never retried;
//   - a local save made during the run was likewise absorbed and never
//     uploaded.
//
// With changes now announced the instant they happen, a pass is routinely
// running when the next change arrives, so these stopped being corner cases.
//
// The ledger therefore only records what the pass KNOWS:
//
//	path with a successful action  → the outcome of that action (a download
//	                                  records the file it wrote and the server
//	                                  signature it planned from; an upload the
//	                                  file it sent and the server's answer)
//	path with a failed/skipped one  → its old row, untouched
//	path the plan left alone        → its old row; with no old row, the
//	                                  adopt rule's twins and shared folders
//	path gone from both sides       → no row
//
// Anything that happened after the snapshots is therefore still a CHANGE for
// the next pass to see, never an agreement. As a side effect the second full
// walk of both trees at the end of every run (the "settle pass") is gone —
// on a 3,328-folder tree behind a proxy that walk alone was minutes.

// DefaultCheckpointEvery is how many settled transfers pass between two
// mid-run baseline writes; checkpointInterval caps the wait in time.
const (
	DefaultCheckpointEvery = 50
	checkpointInterval     = 15 * time.Second
)

// outcome is what one applied action taught the ledger.
type outcome struct {
	row  *BaselineEntry // the path's new row
	drop bool           // the path is gone from both sides: no row
	// pending is set for an upload whose server-side signature is not known
	// yet: the local signature that was sent. resolvePending lists the folder.
	pending string

	// identical: a conflict whose two sides held the same bytes (row is set).
	identical bool
	// held: a conflict a holding pair left alone because its sides differ —
	// nothing was touched and nothing is recorded (hold.go).
	held bool
	// side is a conflict's copy of the server's version (resolveConflict). It
	// is recorded even when the action then FAILED: the copy exists either
	// way, and forgetting it would bring it back as a new file to upload.
	side *sideOutcome
	// onError is the row the path takes when the action failed after the
	// server's version had already been rescued as the side copy — see
	// resolveConflict for why that must not be the old row.
	onError *BaselineEntry
}

// sideOutcome is a conflict copy written on this machine and, when uploaded
// is set, on the server under the same name.
type sideOutcome struct {
	rel      string
	localSig string
	uploaded bool
	row      *BaselineEntry // nil with uploaded: the server's signature is not known yet
}

// ledger is the working baseline of one pass.
//
// It also CHECKPOINTS: the baseline is what makes a run incremental, and a
// first run of 10,000 files that died at 9,000 (a closed laptop, a killed
// watcher, a dropped connection) used to start the next run with no history
// at all. Downloads survive that thanks to the adopt rule (the copy carries
// the server's mtime); UPLOADS did not — the server stamps its own mtime on
// what it receives, so every file this machine had pushed came back as a
// conflict pair. Rows are therefore written every CheckpointEvery settled
// actions or checkpointInterval, whichever comes first.
//
// A checkpoint turns the NEXT run into a non-first run, which enables
// deletes — deliberately safe: rows exist only for files that settled on both
// sides, and everything else is still "never synced" and is copied, not
// deleted.
type ledger struct {
	e       *Engine
	rows    Baseline
	pending map[string]string // uploaded rel → the local signature that was sent
	dirty   int
	last    time.Time
}

func (e *Engine) newLedger(base Baseline) *ledger {
	rows := make(Baseline, len(base))
	for k, v := range base {
		rows[k] = v
	}
	return &ledger{e: e, rows: rows, pending: map[string]string{}, last: time.Now()}
}

// settle records one successful action. Called under the pass mutex.
func (l *ledger) settle(a Action, o outcome) {
	switch {
	case o.drop:
		delete(l.rows, a.Rel)
		delete(l.pending, a.Rel)
	case o.row != nil:
		l.rows[a.Rel] = *o.row
		delete(l.pending, a.Rel)
	case o.pending != "":
		// The old row stays until the server side is known: recording the
		// upload with a guessed signature would make the next pass either
		// re-send it or — if the guess was wrong — call it a conflict.
		l.pending[a.Rel] = o.pending
	default:
		return
	}
	l.dirty++
}

// settleSide records a conflict copy. Called under the pass mutex.
func (l *ledger) settleSide(sd sideOutcome) {
	switch {
	case !sd.uploaded:
		// Only on this disk: no row, so the next pass uploads it as new.
		return
	case sd.row != nil:
		l.rows[sd.rel] = *sd.row
		delete(l.pending, sd.rel)
	default:
		l.pending[sd.rel] = sd.localSig
	}
	l.dirty++
}

// settleRow records a row for a path whose action did not complete but still
// changed what is known about it (outcome.onError). Called under the pass
// mutex.
func (l *ledger) settleRow(rel string, row BaselineEntry) {
	l.rows[rel] = row
	delete(l.pending, rel)
	l.dirty++
}

// keepInStep handles every path of the plan's snapshots that got NO action.
func (l *ledger) keepInStep(local, remote Snapshot, base Baseline, acted map[string]bool) {
	visit := func(rel string) {
		if acted[rel] {
			return
		}
		ln, hasL := local[rel]
		rn, hasR := remote[rel]
		_, hasB := base[rel]
		switch {
		case !hasL && !hasR:
			// In the baseline but on neither side: both ends deleted it.
			if hasB {
				delete(l.rows, rel)
			}
		case hasB:
			// Agreed before, untouched by the plan: still agreed as it was.
			// ⚠ NOT refreshed from the snapshots — that is the whole point.
		case hasL && hasR && ln.IsDir && rn.IsDir:
			l.rows[rel] = BaselineEntry{Local: "dir", Remote: "dir", IsDir: true}
		case hasL && hasR && nearlySameFile(ln, rn):
			// The adopt rule (see Plan): twins with no history.
			l.rows[rel] = BaselineEntry{Local: ln.Signature(), Remote: rn.Signature()}
		}
	}
	for rel := range local {
		visit(rel)
	}
	for rel := range remote {
		if _, seen := local[rel]; !seen {
			visit(rel)
		}
	}
	for rel := range base {
		_, inL := local[rel]
		_, inR := remote[rel]
		if !inL && !inR {
			visit(rel)
		}
	}
}

func (l *ledger) every() int {
	if l.e.CheckpointEvery > 0 {
		return l.e.CheckpointEvery
	}
	return DefaultCheckpointEvery
}

func (l *ledger) due() bool {
	return l.dirty >= l.every() || (l.dirty > 0 && time.Since(l.last) >= checkpointInterval)
}

// checkpoint writes the rows mid-run. Called under the pass mutex.
func (l *ledger) checkpoint(ctx context.Context) {
	l.resolvePending(ctx)
	if err := l.save(); err != nil {
		l.e.logf("!! checkpoint: %v", err)
	} else {
		l.e.logf("checkpoint: %d row(s) recorded, %d upload(s) awaiting a listing", len(l.rows), len(l.pending))
	}
	l.dirty = 0
	l.last = time.Now()
}

// resolvePending lists each folder holding an upload whose server signature
// is not known yet, ONCE, and records what the server reports. A folder that
// cannot be listed right now keeps its uploads pending — and their old rows —
// until a later checkpoint or pass.
func (l *ledger) resolvePending(ctx context.Context) {
	if len(l.pending) == 0 {
		return
	}
	byDir := map[string][]string{}
	for rel := range l.pending {
		d := path.Dir(rel)
		if d == "." {
			d = ""
		}
		byDir[d] = append(byDir[d], rel)
	}
	for d, rels := range byDir {
		listing, err := l.e.API.List(ctx, joinRemote(l.e.Pair.Remote, d))
		if err != nil {
			continue
		}
		seen := make(map[string]ListedFile, len(listing.Files))
		for _, f := range listing.Files {
			seen[f.Basename] = f
		}
		for _, rel := range rels {
			f, ok := seen[path.Base(rel)]
			if !ok || f.IsDir {
				continue
			}
			r := Node{Rel: rel, Size: f.Size, ModMillis: f.LastModified}
			l.rows[rel] = BaselineEntry{Local: l.pending[rel], Remote: r.Signature()}
			delete(l.pending, rel)
		}
	}
}

func (l *ledger) save() error {
	return l.e.Store.SaveBaseline(l.e.Pair.ID, l.rows)
}
