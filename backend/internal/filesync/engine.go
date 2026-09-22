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
	"strings"
	"sync"
	"time"
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
	Upload(ctx context.Context, localPath, remote string) error
	Mkdir(ctx context.Context, remote string) error
	Remove(ctx context.Context, remote string) error
}

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
	FirstRun     bool
	Duration     time.Duration

	// Identical counts conflicts that turned out to hold the same bytes on
	// both sides: no copy was made and nothing was uploaded.
	Identical int
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
	// baseline is written mid-run (see checkpoint). 0 means
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
	// Now is injectable for tests.
	Now func() time.Time

	// runRemote is this run's remote snapshot, so a conflict copy's name can
	// be checked against what the server already holds.
	runRemote Snapshot
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

// Run performs one full pass: snapshot both sides, plan, apply, save the new
// baseline.
//
// A failed action does not abort the run — the rest of the pair still syncs and
// the failure is reported. The baseline is rebuilt from FRESH snapshots taken
// after the work, so a path whose action failed is simply not recorded as
// settled and gets retried next time. That is what stops one broken file from
// permanently wedging a folder.
func (e *Engine) Run(ctx context.Context) (Result, error) {
	if e.Pair.File {
		return e.runFile(ctx)
	}
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
	e.runRemote = remote

	actions := Plan(local, remote, base, Options{FirstRun: res.FirstRun, Now: e.now()})
	res.Planned = len(actions)
	if res.Planned > 0 {
		e.progressf("plan: %d change(s) to make", res.Planned)
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

	cp := e.newCheckpoint(base, local, remote)
	var mu sync.Mutex // guards res, the progress counter and cp; the IO runs unlocked
	done := 0
	settle := func(a Action, tmp Result, out outcome, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %v", a.Kind, a.Rel, err))
			e.logf("!! %s %s: %v", a.Kind, a.Rel, err)
		} else {
			res.Applied++
			res.Uploaded += tmp.Uploaded
			res.Downloaded += tmp.Downloaded
			res.DeletedLocal += tmp.DeletedLocal
			res.DeletedRemot += tmp.DeletedRemot
			res.Conflicts += tmp.Conflicts
			res.Identical += tmp.Identical
			cp.note(a, out)
			if cp.due() {
				cp.flush(ctx)
			}
		}
		done++
		if done%10 == 0 || done == res.Planned {
			e.progressf("transfer: %d/%d", done, res.Planned)
		}
	}
	runSerial := func(batch []Action) bool {
		for _, a := range batch {
			if ctx.Err() != nil {
				return false
			}
			var tmp Result
			out, err := e.apply(ctx, a, &tmp)
			settle(a, tmp, out, err)
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
						var tmp Result
						out, err := e.apply(ctx, a, &tmp)
						settle(a, tmp, out, err)
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
	// The last checkpoint. If the run was cancelled, the remaining upload
	// rows are resolved on a short detached context: the process is leaving,
	// and five seconds of listing is what makes the next run incremental
	// instead of a merge.
	if cp.dirty > 0 {
		fctx := ctx
		if ctx.Err() != nil {
			var cancel context.CancelFunc
			fctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
		}
		cp.flush(fctx)
	}
	if err := ctx.Err(); err != nil {
		res.Errors = append(res.Errors, "stopped: "+err.Error())
	}

	// Re-snapshot. Using the post-run state rather than assuming the plan
	// succeeded means a partial failure cannot poison the baseline.
	local2, _, err := WalkLocal(e.Pair.Local)
	if err != nil {
		return res, fmt.Errorf("re-read %s: %w", e.Pair.Local, err)
	}
	remote2, err := e.walkRemoteRoot(ctx, "settling")
	if err != nil {
		return res, err
	}
	if err := e.Store.SaveBaseline(e.Pair.ID, NextBaseline(local2, remote2)); err != nil {
		return res, err
	}

	if n, err := e.Store.PruneTrash(e.Pair.ID, e.trashDays(), e.now()); err != nil {
		res.Errors = append(res.Errors, "prune trash: "+err.Error())
	} else if n > 0 {
		e.logf("trash: removed %d expired item(s)", n)
	}

	res.Duration = e.now().Sub(started)
	return res, nil
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
	parent.runRemote = remote

	actions := Plan(local, remote, base, Options{FirstRun: res.FirstRun, Now: e.now()})
	res.Planned = len(actions)
	if res.Planned > 0 {
		e.progressf("plan: %d change(s) to make", res.Planned)
	}
	for i, a := range actions {
		if err := ctx.Err(); err != nil {
			res.Errors = append(res.Errors, "stopped: "+err.Error())
			break
		}
		if _, err := parent.apply(ctx, a, &res); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %v", a.Kind, a.Rel, err))
			e.logf("!! %s %s: %v", a.Kind, a.Rel, err)
		} else {
			res.Applied++
		}
		if i+1 == res.Planned {
			e.progressf("transfer: %d/%d", i+1, res.Planned)
		}
	}

	local2, err := statLocal()
	if err != nil {
		return res, err
	}
	remote2, err := listRemote()
	if err != nil {
		return res, err
	}
	if err := e.Store.SaveBaseline(e.Pair.ID, NextBaseline(local2, remote2)); err != nil {
		return res, err
	}

	if n, err := e.Store.PruneTrash(e.Pair.ID, e.trashDays(), e.now()); err != nil {
		res.Errors = append(res.Errors, "prune trash: "+err.Error())
	} else if n > 0 {
		e.logf("trash: removed %d expired item(s)", n)
	}

	res.Duration = e.now().Sub(started)
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

// DefaultCheckpointEvery is how many settled transfers pass between two
// mid-run baseline writes; checkpointInterval caps the wait in time.
const (
	DefaultCheckpointEvery = 50
	checkpointInterval     = 15 * time.Second
)

// checkpoint writes the baseline DURING a run, not only at its end.
//
// The baseline is what makes a run incremental: without it every file present
// on both sides is a "changed in both places" candidate. It used to be written
// once, from the settle pass after all the work — so a first run of 10,000
// files that died at 9,000 (a closed laptop, a killed watcher, a dropped
// connection) started the next run with no history at all. Downloads survive
// that thanks to the adopt rule (the copy carries the server's mtime); UPLOADS
// do not — the server stamps its own mtime on what it receives, so every file
// this machine had pushed came back as a conflict pair.
//
// Rows are recorded as transfers settle: a download's row is exact (the local
// file was just written and stamped, the remote side is the snapshot the plan
// was made from); an upload's remote signature is unknown until the server is
// asked, so uploads are held and resolved at flush time by listing each
// touched folder ONCE — the same source of truth the settle pass uses. The
// final settle pass still rewrites the whole baseline from fresh snapshots; a
// checkpoint only matters when that pass never happens.
//
// A checkpoint turns the NEXT run into a non-first run, which enables
// deletes — deliberately safe: rows exist only for files that settled on both
// sides, and everything else is still "never synced" and is copied, not
// deleted.
type checkpoint struct {
	e       *Engine
	rows    Baseline // working copy of the loaded baseline
	local   Snapshot // pre-run snapshots: the signatures that were transferred
	remote  Snapshot
	pending map[string]Node // uploaded rel → its local node, remote side not yet listed
	dirty   int
	last    time.Time
}

func (e *Engine) newCheckpoint(base Baseline, local, remote Snapshot) *checkpoint {
	rows := make(Baseline, len(base))
	for k, v := range base {
		rows[k] = v
	}
	return &checkpoint{e: e, rows: rows, local: local, remote: remote, pending: map[string]Node{}, last: time.Now()}
}

func (c *checkpoint) every() int {
	if c.e.CheckpointEvery > 0 {
		return c.e.CheckpointEvery
	}
	return DefaultCheckpointEvery
}

// localNode stats rel in the pair folder as it is NOW.
func (c *checkpoint) localNode(rel string) (Node, bool) {
	lp, err := localPathOf(c.e.Pair.Local, rel)
	if err != nil {
		return Node{}, false
	}
	info, err := os.Lstat(lp)
	if err != nil || !info.Mode().IsRegular() {
		return Node{}, false
	}
	return Node{Rel: rel, Size: info.Size(), ModMillis: info.ModTime().UnixMilli()}, true
}

// note records one SETTLED action. Called under the engine's mutex.
func (c *checkpoint) note(a Action, out outcome) {
	switch a.Kind {
	case ActionDownload:
		r, ok := c.remote[a.Rel]
		if !ok {
			return
		}
		l, ok := c.localNode(a.Rel)
		if !ok {
			return
		}
		c.rows[a.Rel] = BaselineEntry{Local: l.Signature(), Remote: r.Signature()}
	case ActionUpload:
		l, ok := c.local[a.Rel]
		if !ok || l.IsDir {
			return
		}
		c.pending[a.Rel] = l
	case ActionConflict:
		if out.identical {
			// The same bytes on both sides: settled exactly as a download
			// would be, against the remote signature the plan compared.
			r, ok := c.remote[a.Rel]
			if !ok {
				return
			}
			l, ok := c.localNode(a.Rel)
			if !ok {
				return
			}
			c.rows[a.Rel] = BaselineEntry{Local: l.Signature(), Remote: r.Signature()}
			break
		}
		// Kept both: the local file went up over the server's, and the
		// server's version went up again under the copy's name. Both are
		// uploads waiting for their remote signature — which is what puts the
		// copy in the baseline NOW, so a copy tidied away on the server is
		// deleted here next run instead of being uploaded again as "new".
		if l, ok := c.local[a.Rel]; ok && !l.IsDir {
			c.pending[a.Rel] = l
		}
		if out.sideRel != "" {
			if l, ok := c.localNode(out.sideRel); ok {
				c.pending[out.sideRel] = l
			}
		}
	case ActionDeleteLocal, ActionDeleteRemot:
		delete(c.rows, a.Rel)
		delete(c.pending, a.Rel)
	default:
		return
	}
	c.dirty++
}

func (c *checkpoint) due() bool {
	return c.dirty >= c.every() || (c.dirty > 0 && time.Since(c.last) >= checkpointInterval)
}

// flush resolves the held uploads and writes the baseline. A folder that
// cannot be listed right now simply keeps its uploads pending; they are
// recorded at the next flush or by the settle pass.
func (c *checkpoint) flush(ctx context.Context) {
	byDir := map[string][]string{}
	for rel := range c.pending {
		d := path.Dir(rel)
		if d == "." {
			d = ""
		}
		byDir[d] = append(byDir[d], rel)
	}
	for d, rels := range byDir {
		listing, err := c.e.API.List(ctx, joinRemote(c.e.Pair.Remote, d))
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
			c.rows[rel] = BaselineEntry{Local: c.pending[rel].Signature(), Remote: r.Signature()}
			delete(c.pending, rel)
		}
	}
	if err := c.e.Store.SaveBaseline(c.e.Pair.ID, c.rows); err != nil {
		c.e.logf("!! checkpoint: %v", err)
	} else {
		c.e.logf("checkpoint: %d row(s) recorded, %d upload(s) awaiting a listing", len(c.rows), len(c.pending))
	}
	c.dirty = 0
	c.last = time.Now()
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

// outcome is what one applied action left behind that its Action does not
// say — the checkpoint needs it to record the right rows.
type outcome struct {
	identical bool   // a conflict whose two sides held the same bytes
	sideRel   string // a conflict copy written beside the original AND uploaded
}

func (e *Engine) apply(ctx context.Context, a Action, res *Result) (outcome, error) {
	lp, err := localPathOf(e.Pair.Local, a.Rel)
	if err != nil {
		return outcome{}, err
	}
	rp := joinRemote(e.Pair.Remote, a.Rel)

	switch a.Kind {
	case ActionMkdirLocal:
		e.logf("+  %s/  (%s)", a.Rel, a.Reason)
		return outcome{}, os.MkdirAll(lp, 0o755)

	case ActionMkdirRemote:
		e.logf("+> %s/  (%s)", a.Rel, a.Reason)
		return outcome{}, e.API.Mkdir(ctx, rp)

	case ActionUpload:
		e.logf("-> %s  (%s)", a.Rel, a.Reason)
		if err := e.API.Mkdir(ctx, joinRemote(e.Pair.Remote, path.Dir(a.Rel))); err != nil {
			// The parent usually exists; only a real upload failure matters.
			_ = err
		}
		if err := e.API.Upload(ctx, lp, rp); err != nil {
			return outcome{}, err
		}
		res.Uploaded++
		return outcome{}, nil

	case ActionDownload:
		e.logf("<- %s  (%s)", a.Rel, a.Reason)
		if err := e.download(ctx, rp, lp, a.RemoteMod, a.RemoteSize); err != nil {
			return outcome{}, err
		}
		res.Downloaded++
		return outcome{}, nil

	case ActionDeleteLocal:
		e.logf("x  %s  (%s)", a.Rel, a.Reason)
		if err := e.Store.TrashLocal(e.Pair.ID, e.Pair.Local, a.Rel, e.now()); err != nil {
			return outcome{}, err
		}
		res.DeletedLocal++
		return outcome{}, nil

	case ActionDeleteRemot:
		e.logf("x> %s  (%s)", a.Rel, a.Reason)
		if err := e.API.Remove(ctx, rp); err != nil {
			return outcome{}, err
		}
		res.DeletedRemot++
		return outcome{}, nil

	case ActionConflict:
		return e.resolveConflict(ctx, a, lp, rp, res)
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
// and then the local file goes up over the server's. Neither edit is lost, both
// sides hold both files, and the checkpoint records both — a copy that is
// later tidied away on the server is deleted here, never re-uploaded as "new".
func (e *Engine) resolveConflict(ctx context.Context, a Action, lp, rp string, res *Result) (outcome, error) {
	if a.Mixed {
		// A folder on one side and a file on the other. Neither may win and
		// nothing is touched; the person has to rename one of them.
		return outcome{}, fmt.Errorf("a folder on one side and a file on the other: nothing was touched, rename one of them")
	}
	tmp, err := e.fetch(ctx, rp, filepath.Dir(lp), a.RemoteSize)
	if err != nil {
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
		return outcome{identical: true}, nil
	}

	e.logf("!! %s  (%s) — keeping both", a.Rel, a.Reason)
	relDir := path.Dir(a.Rel)
	if relDir == "." {
		relDir = ""
	}
	sideName := e.freeSideName(filepath.Dir(lp), relDir, a.ConflictName)
	sidePath := filepath.Join(filepath.Dir(lp), sideName)
	if err := e.install(tmp, sidePath, a.RemoteMod); err != nil {
		return outcome{}, err
	}
	res.Conflicts++

	var out outcome
	// A single-file pair covers one name; a copy beside it belongs to no pair
	// on the server, so it stays on this machine only.
	if !e.Pair.File {
		sideRel := sideName
		if relDir != "" {
			sideRel = relDir + "/" + sideName
		}
		if err := e.API.Upload(ctx, sidePath, joinRemote(e.Pair.Remote, sideRel)); err != nil {
			// Not fatal: the copy is safe on this disk and goes up next run
			// as a new file. Failing here would re-conflict the original.
			e.logf("!! %s: kept here, not uploaded yet: %v", sideRel, err)
		} else {
			res.Uploaded++
			out.sideRel = sideRel
		}
	}
	if err := e.API.Upload(ctx, lp, rp); err != nil {
		return out, err
	}
	res.Uploaded++
	return out, nil
}

// freeSideName returns name, or name with " (2)", " (3)"… before its
// extension — the first that is taken neither in this folder nor on the
// server. Two conflicts on one file inside the same minute used to write the
// second server version over the first.
func (e *Engine) freeSideName(localDir, relDir, name string) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		cand := name
		if i > 1 {
			cand = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		if _, err := os.Lstat(filepath.Join(localDir, cand)); err == nil {
			continue
		}
		rel := cand
		if relDir != "" {
			rel = relDir + "/" + cand
		}
		if _, taken := e.runRemote[rel]; taken {
			continue
		}
		return cand
	}
}

// sameFileContent reports whether two files hold exactly the same bytes.
func sameFileContent(a, b string) (bool, error) {
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
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

// download writes to a temporary file in the destination directory and renames
// it into place, so an interrupted transfer can never be mistaken for a
// complete file by the next run's snapshot.
//
// ⚠ A body whose length is not the listed size is never renamed into place.
// Once it wears the file's name, the next run sees a local file that differs
// from its baseline — a local edit — and uploads it over the real one. That is
// how 202 "preparing" JSON replaced 45 large files on a real server: every one
// of them was listed at tens of megabytes and delivered as ~100 bytes.
func (e *Engine) download(ctx context.Context, remote, dest string, remoteMod, size int64) error {
	tmp, err := e.fetch(ctx, remote, filepath.Dir(dest), size)
	if err != nil {
		return err
	}
	return e.install(tmp, dest, remoteMod)
}

// fetch downloads remote into a new `.filex-part-*` file in dir and returns
// its path; the caller installs it or removes it. A body of the wrong length
// never leaves here.
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
