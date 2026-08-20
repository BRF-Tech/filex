package filesync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// RemoteFS is everything the engine does to a server. Kept as an interface so
// the engine can be driven end to end in a test against a fake server, which is
// the only honest way to test code that deletes files.
type RemoteFS interface {
	RemoteLister
	Download(ctx context.Context, remote string, w io.Writer) (int64, error)
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
}

// Engine runs one pair.
type Engine struct {
	Pair  Pair
	API   RemoteFS
	Store *Store
	// TrashDays is how long locally-deleted files are kept before being
	// dropped. Zero means the default (TrashRetentionDays).
	TrashDays int
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

	if err := os.MkdirAll(e.Pair.Local, 0o755); err != nil {
		return res, fmt.Errorf("sync folder %s: %w", e.Pair.Local, err)
	}

	base, hadBaseline, err := e.Store.LoadBaseline(e.Pair.ID)
	if err != nil {
		return res, err
	}
	res.FirstRun = !hadBaseline

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
		if err := e.apply(ctx, a, &res); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s %s: %v", a.Kind, a.Rel, err))
			e.logf("!! %s %s: %v", a.Kind, a.Rel, err)
		} else {
			res.Applied++
		}
		if (i+1)%10 == 0 || i+1 == res.Planned {
			e.progressf("transfer: %d/%d", i+1, res.Planned)
		}
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

	if err := os.MkdirAll(parent.Pair.Local, 0o755); err != nil {
		return res, fmt.Errorf("sync folder %s: %w", parent.Pair.Local, err)
	}

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

	local, err := statLocal()
	if err != nil {
		return res, err
	}
	remote, err := listRemote()
	if err != nil {
		return res, err
	}

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
		if err := parent.apply(ctx, a, &res); err != nil {
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

func (e *Engine) trashDays() int {
	if e.TrashDays > 0 {
		return e.TrashDays
	}
	return TrashRetentionDays
}

func (e *Engine) apply(ctx context.Context, a Action, res *Result) error {
	lp, err := localPathOf(e.Pair.Local, a.Rel)
	if err != nil {
		return err
	}
	rp := joinRemote(e.Pair.Remote, a.Rel)

	switch a.Kind {
	case ActionMkdirLocal:
		e.logf("+  %s/  (%s)", a.Rel, a.Reason)
		return os.MkdirAll(lp, 0o755)

	case ActionMkdirRemote:
		e.logf("+> %s/  (%s)", a.Rel, a.Reason)
		return e.API.Mkdir(ctx, rp)

	case ActionUpload:
		e.logf("-> %s  (%s)", a.Rel, a.Reason)
		if err := e.API.Mkdir(ctx, joinRemote(e.Pair.Remote, path.Dir(a.Rel))); err != nil {
			// The parent usually exists; only a real upload failure matters.
			_ = err
		}
		if err := e.API.Upload(ctx, lp, rp); err != nil {
			return err
		}
		res.Uploaded++
		return nil

	case ActionDownload:
		e.logf("<- %s  (%s)", a.Rel, a.Reason)
		if err := e.download(ctx, rp, lp); err != nil {
			return err
		}
		res.Downloaded++
		return nil

	case ActionDeleteLocal:
		e.logf("x  %s  (%s)", a.Rel, a.Reason)
		if err := e.Store.TrashLocal(e.Pair.ID, e.Pair.Local, a.Rel, e.now()); err != nil {
			return err
		}
		res.DeletedLocal++
		return nil

	case ActionDeleteRemot:
		e.logf("x> %s  (%s)", a.Rel, a.Reason)
		if err := e.API.Remove(ctx, rp); err != nil {
			return err
		}
		res.DeletedRemot++
		return nil

	case ActionConflict:
		// Keep both. The server's copy lands beside the local one under a name
		// that says where it came from; the local file then goes up unchanged,
		// so neither edit is lost and the user resolves it by looking at two
		// files in their own folder.
		e.logf("!! %s  (%s) — keeping both", a.Rel, a.Reason)
		sidePath := filepath.Join(filepath.Dir(lp), a.ConflictName)
		if err := e.download(ctx, rp, sidePath); err != nil {
			return err
		}
		res.Conflicts++
		if err := e.API.Upload(ctx, lp, rp); err != nil {
			return err
		}
		res.Uploaded++
		return nil
	}
	return fmt.Errorf("unknown action %q", a.Kind)
}

// download writes to a temporary file in the destination directory and renames
// it into place, so an interrupted transfer can never be mistaken for a
// complete file by the next run's snapshot.
func (e *Engine) download(ctx context.Context, remote, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".filex-part-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op once the rename has happened
	}()

	if _, err := e.API.Download(ctx, remote, tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpName, dest)
}
