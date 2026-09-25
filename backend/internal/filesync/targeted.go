package filesync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
)

// ── targeted passes: reconcile the folders that changed, not the tree ────
//
// A full pass walks both trees: one listing per server folder. That is fine
// as a safety net every 30 s for a small pair, and far too slow to be the
// answer to "I just saved this file in the browser": measured on a live
// deployment behind a proxy, a 3,328-folder tree took minutes to list. When
// the server announces that folder X changed (or the local file system says a
// file in X changed), the engine reconciles X alone: ONE listing on each side,
// the same planner, the same apply, the same ledger.
//
// ⚠ The planner decides each path from that path's own (local, remote,
// baseline) triple, so planning over one folder's direct children decides
// every FILE in it exactly as a full pass would. Folders are the exception —
// a new or vanished sub-folder brings a whole subtree with it that one
// listing cannot see — so any folder-level action widens the pass to the
// subtree rooted at X, which is a full pass restricted to X. A folder that is
// itself missing on either side widens to its parent. Nothing is ever decided
// from less information than a full pass would have had for the same paths.

// RunDirs reconciles only the given folders (pair-relative, "" = the pair's
// root). It is what a live change triggers; Run stays the full pass.
//
// A pair with no history yet takes a full pass instead — the first run of a
// pair is a merge of both trees and must see all of them — and so does a pair
// whose local folder is missing (the full pass owns that refusal).
func (e *Engine) RunDirs(ctx context.Context, dirs []string) (Result, error) {
	release, err := e.holdLock()
	if err != nil {
		return Result{}, err
	}
	defer release()
	if e.Pair.File {
		return e.runFile(ctx)
	}
	base, hadBaseline, err := e.Store.LoadBaseline(e.Pair.ID)
	if err != nil {
		return Result{}, err
	}
	if !hadBaseline || len(dirs) == 0 {
		return e.Run(ctx)
	}
	if info, err := os.Lstat(e.Pair.Local); err != nil || !info.IsDir() {
		return e.Run(ctx)
	}
	ctx, done := e.beginPass(ctx)
	defer done()
	started := e.now()
	var res Result
	led := e.newLedger(base)

	var subtrees []string // folders already reconciled recursively this call
	for _, d := range cleanDirs(dirs) {
		if under(d, subtrees) {
			continue
		}
		wide, err := e.passFolder(ctx, led, d, &res)
		if err != nil {
			// The pass could not even look — a listing failed. Nothing was
			// applied for this folder; the next full pass covers it.
			res.Errors = append(res.Errors, fmt.Sprintf("folder %q: %v", d, err))
			res.Retry = append(res.Retry, d)
			if e.stopOn(err) {
				break
			}
			continue
		}
		if wide != nil {
			subtrees = append(subtrees, *wide)
		}
		if ctx.Err() != nil {
			break
		}
	}
	if err := e.finish(ctx, led, &res); err != nil {
		return res, err
	}
	res.Duration = e.now().Sub(started)
	if err := e.halted(); err != nil {
		return res, err
	}
	return res, nil
}

// passFolder reconciles one folder's direct children, widening to its subtree
// (or its parent) when one listing is not enough information. It reports the
// subtree root when it went recursive, so the caller can skip folders under it.
func (e *Engine) passFolder(ctx context.Context, led *ledger, dir string, res *Result) (*string, error) {
	for {
		local, lerr := ListLocalDir(e.Pair.Local, dir)
		if lerr != nil && !isMissingDir(lerr) {
			return nil, lerr
		}
		var remote Snapshot
		var rerr error
		if lerr == nil {
			remote, rerr = ListRemoteDir(ctx, e.API, e.Pair.Remote, dir)
			if rerr != nil && !errors.Is(rerr, ErrRemoteNotFound) {
				return nil, rerr
			}
		}
		if lerr != nil || rerr != nil {
			// The folder itself is new or gone on one side: its PARENT's
			// listing is where that shows up.
			if dir == "" {
				return nil, fmt.Errorf("the pair's root cannot be listed: %v %v", lerr, rerr)
			}
			dir = parentDir(dir)
			continue
		}
		base := led.flatRows(dir)
		actions := Plan(local, remote, base, Options{Now: e.now()})
		if !touchesFolders(actions) {
			actions, holding, err := e.holdPass(actions, base, local, remote, false, false, res)
			if err != nil {
				return nil, err
			}
			e.applyPlan(ctx, led, actions, local, remote, base, res, nil, holding)
			return nil, nil
		}
		return &dir, e.passSubtree(ctx, led, dir, res)
	}
}

// passSubtree is a full pass restricted to one folder and everything below it.
func (e *Engine) passSubtree(ctx context.Context, led *ledger, dir string, res *Result) error {
	localRoot := e.Pair.Local
	if dir != "" {
		p, err := localPathOf(e.Pair.Local, dir)
		if err != nil {
			return err
		}
		localRoot = p
	}
	local, skipped, err := WalkLocal(localRoot)
	if err != nil {
		return err
	}
	res.Skipped = append(res.Skipped, skipped...)
	remote, err := WalkRemote(ctx, e.API, joinRemote(e.Pair.Remote, dir), nil)
	if err != nil {
		return err
	}
	local, remote = prefixed(local, dir), prefixed(remote, dir)
	base := led.subtreeRows(dir)
	actions := Plan(local, remote, base, Options{Now: e.now()})
	actions, holding, err := e.holdPass(actions, base, local, remote, false, false, res)
	if err != nil {
		return err
	}
	e.applyPlan(ctx, led, actions, local, remote, base, res, nil, holding)
	return nil
}

// LocalDirChanged reports whether one local folder's direct children differ
// from what the baseline last agreed — without touching the network.
//
// ⚠ This is how the engine ignores its OWN writes. Every download it makes
// raises file-system events (the rename into place, the mtime stamp), and a
// watcher that answered each one with a server listing would spend a round
// trip per downloaded file finding out that nothing happened. The download
// records exactly the signature it left on disk, so an unchanged folder is
// recognised from the disk alone. A genuine edit changes size or mtime and
// fails this check.
func (e *Engine) LocalDirChanged(dir string) (bool, error) {
	if e.Pair.File {
		return true, nil
	}
	base, had, err := e.Store.LoadBaseline(e.Pair.ID)
	if err != nil || !had {
		return true, err
	}
	local, err := ListLocalDir(e.Pair.Local, dir)
	if err != nil {
		// Gone or unreadable: let a pass decide what that means.
		return true, nil
	}
	rows := (&ledger{rows: base}).flatRows(dir)
	if len(rows) != len(local) {
		return true, nil
	}
	for rel, n := range local {
		row, ok := rows[rel]
		if !ok || row.IsDir != n.IsDir || (!n.IsDir && row.Local != n.Signature()) {
			return true, nil
		}
	}
	return false, nil
}

// flatRows are the baseline rows of dir's direct children.
func (l *ledger) flatRows(dir string) Baseline {
	out := Baseline{}
	for rel, row := range l.rows {
		if parentDir(rel) == dir {
			out[rel] = row
		}
	}
	return out
}

// subtreeRows are the baseline rows strictly below dir.
func (l *ledger) subtreeRows(dir string) Baseline {
	if dir == "" {
		out := make(Baseline, len(l.rows))
		for k, v := range l.rows {
			out[k] = v
		}
		return out
	}
	out := Baseline{}
	for rel, row := range l.rows {
		if strings.HasPrefix(rel, dir+"/") {
			out[rel] = row
		}
	}
	return out
}

// touchesFolders reports whether a plan involves a folder — the cue that one
// listing is not enough and the pass must go recursive.
func touchesFolders(actions []Action) bool {
	for _, a := range actions {
		switch {
		case a.Kind == ActionMkdirLocal, a.Kind == ActionMkdirRemote:
			return true
		case a.LocalSig == "dir", a.RemoteSig == "dir":
			return true
		}
	}
	return false
}

// prefixed re-keys a snapshot taken at a sub-folder onto pair-relative paths.
func prefixed(s Snapshot, dir string) Snapshot {
	if dir == "" {
		return s
	}
	out := make(Snapshot, len(s))
	for rel, n := range s {
		n.Rel = dir + "/" + rel
		out[n.Rel] = n
	}
	return out
}

// cleanDirs normalises, de-duplicates and orders folders shallowest first,
// dropping anything the engine never syncs, so a parent that goes recursive
// covers its children before they are visited.
func cleanDirs(dirs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		d = strings.Trim(path.Clean("/"+strings.ReplaceAll(d, "\\", "/")), "/")
		if d == "." {
			d = ""
		}
		if seen[d] || !syncable(d) {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i], "/"), strings.Count(out[j], "/")
		if out[i] == "" || out[j] == "" {
			return out[i] == ""
		}
		if di != dj {
			return di < dj
		}
		return out[i] < out[j]
	})
	return out
}

// syncable rejects paths inside anything the walk skips (the engine's state
// folder, OS junk) and anything that tries to climb out of the pair.
func syncable(rel string) bool {
	if rel == "" {
		return true
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." || skipName(seg) {
			return false
		}
	}
	return true
}

func under(d string, roots []string) bool {
	for _, r := range roots {
		if r == "" || d == r || strings.HasPrefix(d, r+"/") {
			return true
		}
	}
	return false
}

func parentDir(rel string) string {
	d := path.Dir(rel)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

func isMissingDir(err error) bool {
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	// A FILE where the folder should be (ENOTDIR) reads the same: the parent
	// is where that is decided.
	var pe *fs.PathError
	return errors.As(err, &pe) && strings.Contains(strings.ToLower(pe.Err.Error()), "not a directory")
}
