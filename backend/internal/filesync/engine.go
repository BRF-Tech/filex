package filesync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/regfile"
)

// RemoteFS is everything the engine does to a server. Kept as an interface so
// the engine can be driven end to end in a test against a fake server, which is
// the only honest way to test code that deletes files.
type RemoteFS interface {
	RemoteLister
	// Download writes the file at remote into w. size is what the listing
	// said (-1 = unknown); an implementation should refuse a body of any
	// other length before writing it, and the engine checks again after.
	Download(ctx context.Context, remote string, size int64, w io.Writer) (int64, error)
	// Upload writes localPath to remote. expect is the overwrite precondition
	// the server checks before it writes: "none" (the file must not exist
	// yet) or the listing signature "<size>:<mtime-ms>" the plan was made
	// from. A refusal must come back wrapping ErrRemoteChanged.
	//
	// It returns the file as the server listed it right after the write, or
	// nil when it cannot say — the engine then lists the folder itself. That
	// signature is what the baseline records for the server side; see ledger.
	Upload(ctx context.Context, localPath, remote, expect string) (*ListedFile, error)
	Mkdir(ctx context.Context, remote string) error
	Remove(ctx context.Context, remote string) error
}

// ErrRemoteChanged means the server refused a conditional upload: the file was
// written on the server (typically saved in the browser) after the listing
// this run planned from. Nothing was written. The path keeps its old baseline
// row, so the next pass sees BOTH sides changed and keeps both.
var ErrRemoteChanged = errors.New("changed on the server while it was being synced")

// ErrLocalChanged means a file on this computer changed after the run looked
// at it, so the action planned for it (replace it with the server's copy, or
// move it to the trash) was NOT carried out. Same consequence as
// ErrRemoteChanged, from the other side.
var ErrLocalChanged = errors.New("changed on this computer while it was being synced")

// Pair is one folder mapping.
type Pair struct {
	ID     string `json:"id"`
	Local  string `json:"local"`  // absolute path on this machine
	Remote string `json:"remote"` // adapter://path on the server
	// Account is an opaque label the desktop app uses to remember which signed-in
	// server this pair belongs to. The engine only carries it.
	Account string `json:"account,omitempty"`
	Paused  bool   `json:"paused,omitempty"`
	// File marks a single-file pair: Local is a file path and Remote names a
	// file on the server. Same planner, same rules, same trash — the
	// snapshots just carry one entry.
	File bool `json:"file,omitempty"`
	// HoldNew says the pair holds this machine's unknown items for a decision
	// (hold.go), and Held how many. Both are READ from held/<pair-id>.json by
	// Store.LoadPairs and never written to pairs.json (see holdFile): they are
	// on the pair only so `sync list --json` — which the desktop app reads —
	// carries them.
	HoldNew bool `json:"hold_new,omitempty"`
	Held    int  `json:"held,omitempty"`
}

// WatchRoot is the server folder whose changes concern this pair: the pair's
// own folder, or — for a single-file pair — the folder holding the file.
func (p Pair) WatchRoot() string {
	if p.File {
		return parentRemote(p.Remote)
	}
	return p.Remote
}

// Result reports what one run did. Every field is a measurement, not an
// intention: Applied counts actions that actually completed.
type Result struct {
	Planned      int
	Applied      int
	Uploaded     int
	Downloaded   int
	DeletedLocal int
	DeletedRemot int
	Conflicts    int
	Skipped      []string
	Errors       []string
	// Raced lists actions that stood down because the other side changed
	// while the pass was running (ErrLocalChanged / ErrRemoteChanged). They
	// are not failures: nothing was lost, and the next pass keeps both
	// versions. Reported apart from Errors so a live client can run that
	// next pass at once instead of showing an error.
	Raced    []string
	FirstRun bool
	Duration time.Duration

	// Identical counts conflicts that turned out to hold the same bytes on
	// both sides: no copy was made and nothing was uploaded.
	Identical int
	// Held counts local items left alone for a decision (hold.go).
	Held int
	// Retry lists the folders (pair-relative, "" = the pair's root) holding
	// an action that failed, so a caller can retry just those (RunDirs)
	// instead of walking the whole pair again.
	Retry []string
	// LocalFingerprint digests the local side as a FULL pass left it: the
	// snapshot the plan was made from, with what the pass itself wrote on
	// this machine folded in. ⚠ Never a later walk — an edit made while the
	// pass ran would be folded in too, and a watcher comparing fingerprints
	// would never see it as a change. "" for a targeted pass, and when the
	// pass did not get that far.
	LocalFingerprint string
}

// Engine runs one pair.
type Engine struct {
	Pair  Pair
	API   RemoteFS
	Store *Store
	// TrashDays is how long locally-deleted files are kept before being
	// dropped. Zero means the default (TrashRetentionDays).
	TrashDays int
	// Transfers caps how many uploads/downloads run at once. 0 means
	// DefaultTransfers; 1 restores the fully serial engine.
	Transfers int
	// CheckpointEvery is how many settled transfers may pass before the
	// baseline is written mid-run (see ledger). 0 means
	// DefaultCheckpointEvery.
	CheckpointEvery int
	// Log receives one line per action. Optional.
	Log func(string)
	// Progress receives one short status line per phase — inventory counts,
	// transfer counts, settling — and stays on even when the per-action Log
	// is silenced. The desktop app runs the engine with --quiet and mirrors
	// the last stdout line into its panel; before this hook a large first
	// sync spent its whole inventory phase (minutes of per-folder listings)
	// printing nothing, and looked dead enough that people cancelled it.
	// Optional.
	Progress func(string)
	// StopOn, when set, is asked about every action that failed; true ends
	// the pass at once — the rest of the plan is not attempted, the ledger is
	// still written — and the pass returns that error. The CLI sets it to
	// "the server no longer accepts this token": a revoked token must not be
	// tried once for every remaining file.
	StopOn func(error) bool
	// Now is injectable for tests.
	Now func() time.Time
	// beforeReplace is a test hook: it runs after a download reached its
	// temporary file and immediately before the engine checks the local file
	// is still the one it planned to replace — the window a person's save
	// lands in. nil in production.
	beforeReplace func(dest string)

	halt *passHalt
}

// passHalt is one pass's emergency stop (Engine.StopOn).
type passHalt struct {
	mu     sync.Mutex
	err    error
	cancel context.CancelFunc
}

// beginPass arms the emergency stop for one pass.
func (e *Engine) beginPass(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	e.halt = &passHalt{cancel: cancel}
	return ctx, cancel
}

// stopOn reports whether err ends the pass, and ends it when it does.
func (e *Engine) stopOn(err error) bool {
	if err == nil || e.StopOn == nil || e.halt == nil || !e.StopOn(err) {
		return false
	}
	e.halt.mu.Lock()
	if e.halt.err == nil {
		e.halt.err = err
	}
	e.halt.mu.Unlock()
	e.halt.cancel()
	return true
}

// halted is the error that ended the pass early, or nil.
func (e *Engine) halted() error {
	if e.halt == nil {
		return nil
	}
	e.halt.mu.Lock()
	defer e.halt.mu.Unlock()
	return e.halt.err
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) logf(format string, a ...any) {
	if e.Log != nil {
		e.Log(fmt.Sprintf(format, a...))
	}
}

func (e *Engine) progressf(format string, a ...any) {
	if e.Progress != nil {
		e.Progress(fmt.Sprintf(format, a...))
	}
}

// remoteProgress emits a listing count for one remote walk, throttled so a
// huge tree reports every 25 folders rather than every one.
func (e *Engine) remoteProgress(phase string) func(dirs, items int) {
	if e.Progress == nil {
		return nil
	}
	next := 1
	return func(dirs, items int) {
		if dirs < next {
			return
		}
		next = dirs + 25
		e.progressf("%s: listed %d server folder(s), %d item(s) so far", phase, dirs, items)
	}
}

// Run performs one full pass: snapshot both sides, plan, apply, record what
// was agreed.
//
// A failed action does not abort the run — the rest of the pair still syncs and
// the failure is reported. A path whose action failed keeps the baseline row it
// had, so the next pass looks at it again; that is what stops one broken file
// from permanently wedging a folder, and it is also what makes a change that
// raced the run (an edit saved while it was going) come back as a change
// instead of vanishing into the baseline. See ledger.
func (e *Engine) Run(ctx context.Context) (Result, error) {
	if e.Pair.File {
		return e.runFile(ctx)
	}
	ctx, done := e.beginPass(ctx)
	defer done()
	started := e.now()
	var res Result

	base, hadBaseline, err := e.Store.LoadBaseline(e.Pair.ID)
	if err != nil {
		return res, err
	}
	res.FirstRun = !hadBaseline
	if err := e.guardMissingMirror(e.Pair.Local, base); err != nil {
		return res, err
	}
	if err := os.MkdirAll(e.Pair.Local, 0o755); err != nil {
		return res, fmt.Errorf("sync folder %s: %w", e.Pair.Local, err)
	}

	local, skipped, err := WalkLocal(e.Pair.Local)
	if err != nil {
		return res, fmt.Errorf("read %s: %w", e.Pair.Local, err)
	}
	res.Skipped = skipped
	e.progressf("inventory: %d item(s) here, listing the server…", len(local))

	remote, err := e.walkRemoteRoot(ctx, "inventory")
	if err != nil {
		return res, err
	}

	led := e.newLedger(base)
	actions := Plan(local, remote, base, Options{FirstRun: res.FirstRun, Now: e.now()})
	actions, holding, err := e.holdPass(actions, base, local, remote, res.FirstRun, true, &res)
	if err != nil {
		return res, err
	}
	lv := newLocalView(local)
	e.applyPlan(ctx, led, actions, local, remote, base, &res, lv, holding)
	if err := e.finish(ctx, led, &res); err != nil {
		return res, err
	}
	res.LocalFingerprint = lv.fingerprint()
	res.Duration = e.now().Sub(started)
	if err := e.halted(); err != nil {
		return res, err
	}
	return res, nil
}

// finish writes the ledger and prunes the trash — the shared tail of every
// kind of pass.
func (e *Engine) finish(ctx context.Context, led *ledger, res *Result) error {
	// If the run was cancelled, the uploads still waiting for their server
	// signature are resolved on a short detached context: the process is
	// leaving, and five seconds of listing is what makes the next run
	// incremental instead of a merge.
	fctx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		fctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if e.halted() == nil {
			res.Errors = append(res.Errors, "stopped: "+ctx.Err().Error())
		}
	}
	led.resolvePending(fctx)
	if err := led.save(); err != nil {
		return err
	}
	if n, err := e.Store.PruneTrash(e.Pair.ID, e.trashDays(), e.now()); err != nil {
		res.Errors = append(res.Errors, "prune trash: "+err.Error())
	} else if n > 0 {
		e.logf("trash: removed %d expired item(s)", n)
	}
	res.Retry = cleanRetry(res.Retry)
	return nil
}

// applyPlan carries out a plan made from (local, remote, base) and records
// every outcome in the ledger. The three snapshots are the plan's own inputs
// and nothing else: whatever happened on either side after they were taken is
// NOT folded in here — it is a change for the next pass to see.
//
// lv, when set (a full pass), learns what the pass itself changed on this
// machine; holding says the pair holds its unknown items for a decision, and
// the conflicts it therefore left alone are added to the hold.
func (e *Engine) applyPlan(ctx context.Context, led *ledger, actions []Action, local, remote Snapshot, base Baseline, res *Result, lv *localView, holding bool) {
	res.Planned += len(actions)
	if len(actions) > 0 {
		e.progressf("plan: %d change(s) to make", len(actions))
	}

	// Three phases. Directory creation first (parents must exist, and it is
	// cheap), then the TRANSFERS — concurrently, because a tree of small
	// files is otherwise priced at one full round-trip each: measured on a
	// live deployment, 2 GB of ~400 KB files crawled at 0.24 MB/s under the
	// serial loop, gated purely on latency — and finally deletes and
	// conflicts, serial, in the planner's careful deepest-first order.
	var mkdirs, transfers, rest []Action
	for _, a := range actions {
		switch a.Kind {
		case ActionMkdirLocal, ActionMkdirRemote:
			mkdirs = append(mkdirs, a)
		case ActionUpload, ActionDownload:
			transfers = append(transfers, a)
		default:
			rest = append(rest, a)
		}
	}

	pc := &passCtx{remote: remote, madeRemote: map[string]bool{}}
	var mu sync.Mutex // guards res, the progress counters, pc.madeRemote, lv, heldConflicts and the ledger; the IO runs unlocked
	done := 0
	total := len(actions)
	applied := 0
	acted := make(map[string]bool, len(actions))
	var bytesTotal, bytesDone int64
	for _, a := range actions {
		acted[a.Rel] = true
		bytesTotal += transferBytes(a, local)
	}
	transfersStarted := time.Now()
	lastLine := transfersStarted
	heldConflicts := map[string]string{}
	settle := func(a Action, tmp Result, o outcome, err error) {
		mu.Lock()
		defer mu.Unlock()
		if o.side != nil {
			// The server's version was rescued beside the original: that
			// copy exists whatever happened to the original afterwards.
			led.settleSide(*o.side)
			lv.set(o.side.rel, o.side.localSig)
		}
		raced := errors.Is(err, ErrLocalChanged) || errors.Is(err, ErrRemoteChanged)
		switch {
		case raced:
			res.Raced = append(res.Raced, fmt.Sprintf("%s %s: %v", a.Kind, a.Rel, err))
			e.logf("~~ %s %s: %v — both versions are kept next pass", a.Kind, a.Rel, err)
		case err != nil:
			res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %v", a.Kind, a.Rel, err))
			res.Retry = append(res.Retry, parentDir(a.Rel))
			e.logf("!! %s %s: %v", a.Kind, a.Rel, err)
			if o.onError != nil {
				led.settleRow(a.Rel, *o.onError)
			}
		default:
			applied++
			res.Applied++
			res.Uploaded += tmp.Uploaded
			res.Downloaded += tmp.Downloaded
			res.DeletedLocal += tmp.DeletedLocal
			res.DeletedRemot += tmp.DeletedRemot
			res.Conflicts += tmp.Conflicts
			res.Identical += tmp.Identical
			if a.Kind == ActionMkdirRemote {
				pc.madeRemote[a.Rel] = true
			}
			if o.held {
				heldConflicts[a.Rel] = a.LocalSig
				res.Held++
			}
			lv.apply(a, o)
			led.settle(a, o)
		}
		if err == nil || o.side != nil || o.onError != nil {
			if led.due() {
				led.checkpoint(ctx)
			}
		}
		bytesDone += transferBytes(a, local)
		done++
		// Every tenth action, the last one, and at least every few seconds:
		// a tree of 1 GB files would otherwise report once per 10 GB.
		if done%10 == 0 || done == total || time.Since(lastLine) >= 5*time.Second {
			lastLine = time.Now()
			e.progressf("%s", transferLine(done, total, bytesDone, bytesTotal, time.Since(transfersStarted)))
		}
	}
	runOne := func(a Action) {
		var tmp Result
		o, err := e.apply(ctx, pc, &mu, a, &tmp)
		settle(a, tmp, o, err)
		e.stopOn(err)
	}
	runSerial := func(batch []Action) bool {
		for _, a := range batch {
			if ctx.Err() != nil {
				return false
			}
			runOne(a)
		}
		return true
	}

	stopped := !runSerial(mkdirs)
	if !stopped && len(transfers) > 0 {
		workers := e.transferWorkers()
		if workers > len(transfers) {
			workers = len(transfers)
		}
		if workers <= 1 {
			stopped = !runSerial(transfers)
		} else {
			jobs := make(chan Action)
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for a := range jobs {
						if ctx.Err() != nil {
							continue
						}
						runOne(a)
					}
				}()
			}
			for _, a := range transfers {
				if ctx.Err() != nil {
					break
				}
				jobs <- a
			}
			close(jobs)
			wg.Wait()
			stopped = ctx.Err() != nil
		}
	}
	if !stopped {
		runSerial(rest)
	}

	// Paths the plan left alone: rows for what was already agreed, or agreed
	// by the adopt rule, and the rows of paths both sides deleted.
	led.keepInStep(local, remote, base, acted)
	if holding {
		e.recordHeldConflicts(heldConflicts, res)
	}
	if total > 0 {
		e.progressf("settling: %d of %d change(s) recorded", applied, total)
	}
}

// passCtx is what apply needs to know about the pass it is part of.
type passCtx struct {
	remote     Snapshot        // the plan-time server snapshot
	madeRemote map[string]bool // folders this pass created on the server (guarded by the pass mutex)
}

// parentRemote is the folder holding a remote file path:
// docs://a/b.txt → docs://a, docs://b.txt → docs://.
func parentRemote(remote string) string {
	idx := strings.Index(remote, "://")
	root, rel := remote[:idx+3], strings.Trim(remote[idx+3:], "/")
	if rel == "" {
		return root
	}
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return root + rel[:i]
	}
	return root
}

// runFile is Run for a single-file pair. The planner and apply() are reused
// unchanged: both snapshots carry at most one entry, keyed by the file's
// basename under its PARENT folders — so upload, download, conflict copies
// and the local trash all work exactly as they do for a folder of one.
func (e *Engine) runFile(ctx context.Context) (Result, error) {
	ctx, done := e.beginPass(ctx)
	defer done()
	started := e.now()
	var res Result

	name := filepath.Base(e.Pair.Local)
	parent := *e
	parent.Pair.Local = filepath.Dir(e.Pair.Local)
	parent.Pair.Remote = parentRemote(e.Pair.Remote)

	statLocal := func() (Snapshot, error) {
		out := Snapshot{}
		info, err := os.Lstat(e.Pair.Local)
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file any more; remove the pair", e.Pair.Local)
		}
		out[name] = Node{Rel: name, Size: info.Size(), ModMillis: info.ModTime().UnixMilli()}
		return out, nil
	}
	listRemote := func() (Snapshot, error) {
		out := Snapshot{}
		listing, err := e.API.List(ctx, parent.Pair.Remote)
		if err != nil {
			// The parent may simply not exist yet — the first upload of a kept
			// file into a fresh tree. Create it; a real failure comes back on
			// the retry.
			if mkErr := e.API.Mkdir(ctx, parent.Pair.Remote); mkErr != nil {
				return nil, err
			}
			if listing, err = e.API.List(ctx, parent.Pair.Remote); err != nil {
				return nil, err
			}
		}
		for _, f := range listing.Files {
			if f.Basename != name {
				continue
			}
			if f.IsDir {
				return nil, fmt.Errorf("%s is a folder on the server; remove the pair", e.Pair.Remote)
			}
			out[name] = Node{Rel: name, Size: f.Size, ModMillis: f.LastModified}
		}
		return out, nil
	}

	base, hadBaseline, err := e.Store.LoadBaseline(e.Pair.ID)
	if err != nil {
		return res, err
	}
	res.FirstRun = !hadBaseline
	// The file's parent folder plays the mirror's role here: recreating it
	// empty under a baseline that remembers the file reads as "deleted here".
	if err := e.guardMissingMirror(parent.Pair.Local, base); err != nil {
		return res, err
	}
	if err := os.MkdirAll(parent.Pair.Local, 0o755); err != nil {
		return res, fmt.Errorf("sync folder %s: %w", parent.Pair.Local, err)
	}

	local, err := statLocal()
	if err != nil {
		return res, err
	}
	remote, err := listRemote()
	if err != nil {
		return res, err
	}

	// A file pair's apply is serial: one entry, and the parent engine is the
	// one whose Pair points at the containing folders.
	parent.Transfers = 1
	led := parent.newLedger(base)
	actions := Plan(local, remote, base, Options{FirstRun: res.FirstRun, Now: e.now()})
	lv := newLocalView(local)
	parent.applyPlan(ctx, led, actions, local, remote, base, &res, lv, false)
	if err := parent.finish(ctx, led, &res); err != nil {
		return res, err
	}
	// A single-file pair's conflict copy stays beside it on this machine and
	// belongs to no pair: it is not part of what the pair's fingerprint says.
	for rel := range lv.sigs {
		if rel != name {
			delete(lv.sigs, rel)
		}
	}
	res.LocalFingerprint = lv.fingerprint()
	res.Duration = e.now().Sub(started)
	if err := e.halted(); err != nil {
		return res, err
	}
	return res, nil
}

// walkRemoteRoot snapshots the server side, creating the pair's remote folder
// if it is not there yet.
//
// ⚠ Without this, pairing a folder to a server path that does not exist yet
// fails on every run — the walk cannot list a missing directory, so nothing
// ever syncs and the user is left to go and create the folder by hand in the
// web UI. Pairing is a statement of intent; the folder is part of it. The
// mkdir is only attempted after a failed listing, so the ordinary case still
// costs one request.
func (e *Engine) walkRemoteRoot(ctx context.Context, phase string) (Snapshot, error) {
	snap, err := WalkRemote(ctx, e.API, e.Pair.Remote, e.remoteProgress(phase))
	if err == nil {
		return snap, nil
	}
	if e.StopOn != nil && e.StopOn(err) {
		// A refused token is not a missing folder: creating one would only
		// be refused the same way.
		return nil, err
	}
	if mkErr := e.API.Mkdir(ctx, e.Pair.Remote); mkErr != nil {
		// Report the original listing failure: it says what actually went
		// wrong (unauthorized, storage down), where the mkdir error would only
		// say the folder could not be created.
		return nil, err
	}
	e.logf("+> created %s on the server", e.Pair.Remote)
	return WalkRemote(ctx, e.API, e.Pair.Remote, e.remoteProgress(phase))
}

// guardMissingMirror refuses to run a pair whose local folder is GONE while
// its baseline still remembers files. Creating the folder empty and carrying
// on — which is what MkdirAll followed by a walk would do — makes every
// remembered file look deleted here, and the planner would carry that to the
// server as a mass delete. The folder is gone for one of three reasons (it
// moved, its drive is unplugged, the user removed it) and none of them is
// "please delete everything on the server"; the message names the command
// for each. An EMPTY baseline (a pair that never synced a file) may still
// have its folder created: there is nothing to lose.
func (e *Engine) guardMissingMirror(dir string, base Baseline) error {
	if len(base) == 0 {
		return nil
	}
	if _, err := os.Lstat(dir); err == nil || !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("sync folder %s is missing but pair %s has history; nothing was touched. "+
		"If the folder moved: filex sync move %s <new-path>. If its drive is unplugged: plug it back in. "+
		"To stop syncing it: filex sync remove %s",
		dir, e.Pair.ID, e.Pair.ID, e.Pair.ID)
}

// DefaultTransfers is how many uploads/downloads run concurrently. Four is
// enough to stop a tree of small files being priced at one full round-trip
// each, without stampeding a small server.
const DefaultTransfers = 4

func (e *Engine) transferWorkers() int {
	if e.Transfers > 0 {
		return e.Transfers
	}
	return DefaultTransfers
}

func (e *Engine) trashDays() int {
	if e.TrashDays > 0 {
		return e.TrashDays
	}
	return TrashRetentionDays
}

// expectFor is the overwrite precondition an upload carries: the server copy
// the plan saw, or "none" when the plan saw no server copy at all.
func expectFor(a Action) string {
	if a.RemoteSig == "" {
		return "none"
	}
	return a.RemoteSig
}

// uploaded is the outcome of a successful upload: the local signature the
// plan uploaded, and the server's signature for the result when the server
// told us — otherwise the ledger lists the folder for it.
func uploaded(a Action, got *ListedFile) outcome {
	if got == nil || got.IsDir {
		return outcome{pending: a.LocalSig}
	}
	r := Node{Size: got.Size, ModMillis: got.LastModified}
	return outcome{row: &BaselineEntry{Local: a.LocalSig, Remote: r.Signature()}}
}

// ensureRemoteParent creates the server folder an upload lands in, when the
// plan does not already know it exists. ⚠ Only then: this used to be one
// mkdir request in front of EVERY upload, ignored when the folder existed —
// a whole round-trip per edited file on a server behind a proxy.
func (e *Engine) ensureRemoteParent(ctx context.Context, pc *passCtx, mu *sync.Mutex, rel string) {
	dir := path.Dir(rel)
	if dir == "." || dir == "" {
		return
	}
	if n, ok := pc.remote[dir]; ok && n.IsDir {
		return
	}
	mu.Lock()
	made := pc.madeRemote[dir]
	mu.Unlock()
	if made {
		return
	}
	// The parent usually exists by now; only a real upload failure matters.
	_ = e.API.Mkdir(ctx, joinRemote(e.Pair.Remote, dir))
	mu.Lock()
	pc.madeRemote[dir] = true
	mu.Unlock()
}

func (e *Engine) apply(ctx context.Context, pc *passCtx, mu *sync.Mutex, a Action, res *Result) (outcome, error) {
	lp, err := localPathOf(e.Pair.Local, a.Rel)
	if err != nil {
		return outcome{}, err
	}
	rp := joinRemote(e.Pair.Remote, a.Rel)
	dirRow := outcome{row: &BaselineEntry{Local: "dir", Remote: "dir", IsDir: true}}

	switch a.Kind {
	case ActionMkdirLocal:
		e.logf("+  %s/  (%s)", a.Rel, a.Reason)
		return dirRow, os.MkdirAll(lp, 0o755)

	case ActionMkdirRemote:
		e.logf("+> %s/  (%s)", a.Rel, a.Reason)
		return dirRow, e.API.Mkdir(ctx, rp)

	case ActionUpload:
		e.logf("-> %s  (%s)", a.Rel, a.Reason)
		e.ensureRemoteParent(ctx, pc, mu, a.Rel)
		got, err := e.API.Upload(ctx, lp, rp, expectFor(a))
		if err != nil {
			return outcome{}, err
		}
		res.Uploaded++
		return uploaded(a, got), nil

	case ActionDownload:
		e.logf("<- %s  (%s)", a.Rel, a.Reason)
		sig, err := e.download(ctx, rp, lp, a.RemoteMod, a.RemoteSize, a.LocalSig)
		if err != nil {
			return outcome{}, err
		}
		res.Downloaded++
		return outcome{row: &BaselineEntry{Local: sig, Remote: a.RemoteSig}}, nil

	case ActionDeleteLocal:
		e.logf("x  %s  (%s)", a.Rel, a.Reason)
		// ⭐ A delete never beats an edit — and that has to hold at the moment
		// of the delete, not only at the moment of the plan.
		if err := stillAsPlanned(lp, a.LocalSig); err != nil {
			return outcome{}, err
		}
		if err := e.Store.TrashLocal(e.Pair.ID, e.Pair.Local, a.Rel, e.now()); err != nil {
			return outcome{}, err
		}
		res.DeletedLocal++
		return outcome{drop: true}, nil

	case ActionDeleteRemot:
		// ⚠ Not conditional: the server has no "delete only if unchanged". A
		// browser save landing between this pass's listing and this request
		// is therefore deleted with it — into the server's trash, where it
		// stays restorable, not gone. The window is one request long.
		e.logf("x> %s  (%s)", a.Rel, a.Reason)
		if err := e.API.Remove(ctx, rp); err != nil {
			return outcome{}, err
		}
		res.DeletedRemot++
		return outcome{drop: true}, nil

	case ActionConflict:
		return e.resolveConflict(ctx, pc, a, lp, rp, res)
	}
	return outcome{}, fmt.Errorf("unknown action %q", a.Kind)
}

// resolveConflict keeps both versions of a file that changed on both sides —
// unless they are the same bytes, which is far more common than a real
// conflict: a lost baseline, a reinstalled client, two people saving the same
// attachment. The server's copy is fetched first and COMPARED; only a real
// difference makes a copy.
//
// When they differ, the server's version is installed beside the local file
// under a name that says where it came from, uploaded under that same name,
// and then the local file goes up over the server's — with the plan's server
// signature as its precondition, so a save made on the server after the fetch
// is refused instead of overwritten. Neither edit is lost, both sides hold
// both files, and the ledger records both: a copy that is later tidied away on
// the server is deleted here, never re-uploaded as "new".
//
// ⚠⚠ One copy per version, whatever happens next. A copy used to be made and
// the original's upload then fail — a lock (423), a size limit, a full quota —
// and the next pass, seeing the same two versions, made another copy, every
// pass, for ever. Once the server's version is rescued as a copy, the path's
// row records that version as known (onError): the next pass plans a plain
// upload of the local file, which fails the same way until the cause is gone,
// and makes no copy.
func (e *Engine) resolveConflict(ctx context.Context, pc *passCtx, a Action, lp, rp string, res *Result) (outcome, error) {
	if a.Mixed {
		// A folder on one side and a file on the other. Neither may win and
		// nothing is touched; the person has to rename one of them.
		return outcome{}, fmt.Errorf("a folder on one side and a file on the other: nothing was touched, rename one of them")
	}
	tmp, err := e.fetch(ctx, rp, filepath.Dir(lp), a.RemoteSize)
	if err != nil {
		return outcome{}, err
	}
	// The local side must still be the one the plan saw: a save made here in
	// the meantime is a new decision for the next pass.
	if err := stillAsPlanned(lp, a.LocalSig); err != nil {
		os.Remove(tmp)
		return outcome{}, err
	}
	same, err := sameFileContent(tmp, lp)
	if err != nil {
		os.Remove(tmp)
		return outcome{}, err
	}
	if same {
		os.Remove(tmp)
		e.logf("=  %s  (%s, but the same bytes on both sides — nothing to keep twice)", a.Rel, a.Reason)
		res.Identical++
		// Settled exactly as a download would be: both plan-time signatures,
		// which is what the two sides held when the bytes were compared.
		return outcome{identical: true, row: &BaselineEntry{Local: a.LocalSig, Remote: a.RemoteSig}}, nil
	}
	if a.Hold {
		// The pair is waiting for a decision about this machine's side:
		// pushing a possibly stale local version over the server's is
		// exactly what it is waiting to be told.
		os.Remove(tmp)
		e.logf("?  %s  (%s — held for a decision)", a.Rel, a.Reason)
		return outcome{held: true}, nil
	}

	e.logf("!! %s  (%s) — keeping both", a.Rel, a.Reason)
	relDir := parentDir(a.Rel)
	sideName := freeSideName(filepath.Dir(lp), relDir, a.ConflictName, pc.remote)
	sidePath := filepath.Join(filepath.Dir(lp), sideName)
	if err := e.install(tmp, sidePath, a.RemoteMod); err != nil {
		return outcome{}, err
	}
	res.Conflicts++
	sideSig, err := localSignature(sidePath)
	if err != nil {
		return outcome{}, err
	}
	sideRel := sideName
	if relDir != "" {
		sideRel = relDir + "/" + sideName
	}
	out := outcome{
		side: &sideOutcome{rel: sideRel, localSig: sideSig},
		// The server's version is safe as the copy; see the doc comment.
		onError: &BaselineEntry{Local: "", Remote: a.RemoteSig},
	}
	// A single-file pair covers one name; a copy beside it belongs to no pair
	// on the server, so it stays on this machine only.
	if !e.Pair.File {
		// Create-only ("none"): two machines resolving the same conflict in the
		// same minute must not write their copies over each other's.
		got, err := e.API.Upload(ctx, sidePath, joinRemote(e.Pair.Remote, sideRel), "none")
		if err != nil {
			// Not fatal: the copy is safe on this disk and goes up next pass
			// as a new file. Failing here would re-conflict the original.
			e.logf("!! %s: kept here, not uploaded yet: %v", sideRel, err)
		} else {
			res.Uploaded++
			out.side.uploaded = true
			if got != nil && !got.IsDir {
				r := Node{Size: got.Size, ModMillis: got.LastModified}
				out.side.row = &BaselineEntry{Local: sideSig, Remote: r.Signature()}
			}
		}
	}
	got, err := e.API.Upload(ctx, lp, rp, expectFor(a))
	if err != nil {
		return out, err
	}
	res.Uploaded++
	up := uploaded(a, got)
	out.row, out.pending, out.onError = up.row, up.pending, nil
	return out, nil
}

// freeSideName returns name, or name with " (2)", " (3)"… before its
// extension — the first that is taken neither in this folder nor in the
// server listing the pass planned from. Conflict copy names are stamped to the
// MINUTE, and two conflicts on one file inside a minute are ordinary once
// changes travel instantly (an autosaving editor on each side): the second
// server version used to be written over the first.
func freeSideName(localDir, relDir, name string, remote Snapshot) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		if _, err := os.Lstat(filepath.Join(localDir, cand)); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		rel := cand
		if relDir != "" {
			rel = relDir + "/" + cand
		}
		if _, taken := remote[rel]; taken {
			continue
		}
		return cand
	}
}

// sameFileContent reports whether two files hold exactly the same bytes.
func sameFileContent(a, b string) (bool, error) {
	// ⚠ regfile: the walk tracks regular files only, but a name can turn into
	// a named pipe between the walk and this read, and os.Open on a pipe
	// nobody writes to never returns (issue #38).
	fa, err := regfile.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := regfile.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	sa, err := fa.Stat()
	if err != nil {
		return false, err
	}
	sb, err := fb.Stat()
	if err != nil {
		return false, err
	}
	if sa.Size() != sb.Size() {
		return false, nil
	}
	bufA := make([]byte, 64<<10)
	bufB := make([]byte, 64<<10)
	for {
		na, errA := io.ReadFull(fa, bufA)
		nb, errB := io.ReadFull(fb, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, nil
		}
		endA := errA == io.EOF || errA == io.ErrUnexpectedEOF
		endB := errB == io.EOF || errB == io.ErrUnexpectedEOF
		if errA != nil && !endA {
			return false, errA
		}
		if errB != nil && !endB {
			return false, errB
		}
		if endA || endB {
			return endA && endB, nil
		}
	}
}

// localSignature is Node.Signature for whatever is at p right now: "" when
// nothing is, "dir" for a folder.
func localSignature(p string) (string, error) {
	info, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "dir", nil
	}
	return Node{Size: info.Size(), ModMillis: info.ModTime().UnixMilli()}.Signature(), nil
}

// stillAsPlanned checks that the local path is what the plan saw (want is the
// plan-time Action.LocalSig). A folder additionally has to hold nothing sync
// would track: its planned children were trashed before it (deepest first),
// so anything still in it arrived after the plan — a file saved into it a
// moment ago — and trashing the folder would take that file with it.
func stillAsPlanned(p, want string) error {
	got, err := localSignature(p)
	if err != nil {
		return err
	}
	if got != want {
		return ErrLocalChanged
	}
	if want != "dir" {
		return nil
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		return err
	}
	for _, d := range entries {
		if _, v := localEntry(d.Name(), d); v == entryTracked {
			return ErrLocalChanged
		}
	}
	return nil
}

// download fetches the server's file into a temporary file in the destination
// directory and renames it into place (install), so an interrupted transfer
// can never be mistaken for a complete file by the next run's snapshot. It
// returns the local signature of the file it left behind — the local half of
// the new baseline row.
//
// expectLocal is the plan-time signature of what is at dest ("" = nothing).
// ⚠⚠ It is checked AFTER the bytes arrived and immediately before the
// replace, because that is the window a person's save lands in: the pass that
// a browser edit triggers starts within a second of it, and a desktop editor
// saving the same file in that same second is exactly the race the "changed in
// both places — keep both" rule exists for. Replacing the file anyway would
// erase the local edit with nothing to show for it.
func (e *Engine) download(ctx context.Context, remote, dest string, remoteMod, size int64, expectLocal string) (string, error) {
	tmp, err := e.fetch(ctx, remote, filepath.Dir(dest), size)
	if err != nil {
		return "", err
	}
	if e.beforeReplace != nil {
		e.beforeReplace(dest)
	}
	if err := stillAsPlanned(dest, expectLocal); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := e.install(tmp, dest, remoteMod); err != nil {
		return "", err
	}
	return localSignature(dest)
}

// fetch downloads remote into a new `.filex-part-*` file in dir and returns
// its path; the caller installs it or removes it.
//
// ⚠⚠ A body whose length is not the listed size never leaves here. Once it
// wears the file's name, the next pass sees a local file that differs from its
// baseline — a local edit — and uploads it over the real one. That is how 202
// "preparing" JSON replaced 45 large files on a real server: every one of them
// was listed at tens of megabytes and delivered as ~100 bytes.
func (e *Engine) fetch(ctx context.Context, remote, dir string, size int64) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".filex-part-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	n, err := e.API.Download(ctx, remote, size, tmp)
	if err == nil && size >= 0 && n != size {
		err = fmt.Errorf("the server listed %d bytes but sent %d; not installed", size, n)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmpName)
		return "", err
	}
	return tmpName, nil
}

// install renames a fetched temporary file into place and stamps the server's
// mtime on it. The temporary file is gone either way.
func (e *Engine) install(tmpName, dest string, remoteMod int64) error {
	if err := os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return err
	}
	// Stamp the server's own mtime on the copy. (size, mtime) equality is how
	// a later run with NO baseline recognises settled work — the interrupted
	// first run that used to conflict every finished file on resume.
	if remoteMod > 0 {
		t := time.UnixMilli(remoteMod)
		if err := os.Chtimes(dest, t, t); err != nil {
			e.logf("!! stamp mtime %s: %v", dest, err)
		}
	}
	return nil
}

// cleanRetry de-duplicates and orders the folders to retry.
func cleanRetry(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}
