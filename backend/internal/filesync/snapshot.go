package filesync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/brf-tech/filex/backend/internal/syspath"
)

// RemoteLister is the slice of the API client this package needs. Declaring it
// here rather than importing cliclient.Client keeps the planner and the walkers
// testable with a fake, and stops this package from growing a dependency on
// every REST call the CLI happens to support.
type RemoteLister interface {
	List(ctx context.Context, remote string) (*Listing, error)
}

// Listing is one remote directory. It mirrors the server's index response,
// reduced to what sync actually reads.
type Listing struct {
	Files []ListedFile
}

// ListedFile is one row of a remote listing.
type ListedFile struct {
	Basename     string
	IsDir        bool
	Size         int64
	LastModified int64 // Unix millis; 0 = the storage does not report one
}

// skipNames are never synced in either direction.
//
// ⚠ The state directory has to be excluded or the engine syncs its own
// bookkeeping, which then changes, which schedules another sync — a loop that
// grows a file on the server forever.
var skipNames = map[string]bool{
	".filex-sync":               true, // this engine's own state + trash
	".DS_Store":                 true,
	"Thumbs.db":                 true,
	"desktop.ini":               true,
	".Trash-1000":               true,
	"$RECYCLE.BIN":              true,
	"System Volume Information": true,
}

// skipName is the ONE question every reader asks of a name: the engine's own
// state and OS junk (skipNames), and filex's own reserved names
// (syspath.IsName — the trash, version history, thumbnails, the desktop's
// open-with working area and the keep marker).
//
// ⚠ The reserved names come from syspath, never from a copy here. The server
// refuses a write that lands inside any of them (writegate) and never lists
// them, so a local folder called `.versions` used to be uploaded on every
// pass and answered 403 every time — an error under the folder that nothing
// the person could do would clear. Ignored, it simply stays on this computer.
func skipName(name string) bool {
	return skipNames[name] || syspath.IsName(name)
}

// WalkLocal snapshots a directory tree. Unreadable entries are skipped rather
// than failing the run: one locked file must not stop the other thousand from
// syncing. The names of skipped entries are returned so the caller can report
// them instead of silently doing less than it claimed.
func WalkLocal(root string) (Snapshot, []string, error) {
	out := Snapshot{}
	var skipped []string

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory we cannot read: note it and carry on.
			skipped = append(skipped, relOf(root, p))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		n, verdict := localEntry(relOf(root, p), d)
		switch verdict {
		case entryIgnore:
			if d.IsDir() {
				return filepath.SkipDir
			}
		case entrySkipped:
			skipped = append(skipped, relOf(root, p))
		case entryTracked:
			out[n.Rel] = n
		}
		return nil
	})
	if err != nil {
		return nil, skipped, err
	}
	sort.Strings(skipped)
	return out, skipped, nil
}

// IgnoredName reports whether a file or folder name is never sync material:
// the engine's own state, OS junk, filex's own reserved names (syspath) and
// its half-written downloads. A file-system watcher uses it to drop events
// the walk would ignore anyway —
// above all the `.filex-part-*` temporaries every download creates, which
// would otherwise wake the engine up once per file it writes.
func IgnoredName(name string) bool {
	return skipName(name) || strings.HasPrefix(name, ".filex-part-")
}

// entryVerdict is what the local walk does with one directory entry.
type entryVerdict int

const (
	entryTracked entryVerdict = iota // part of the snapshot
	entryIgnore                      // never sync material (state dir, OS junk, partial downloads)
	entrySkipped                     // real content sync cannot handle (symlink, unreadable, not regular) — reported
)

// localEntry is the ONE rule for what a local directory entry means to sync,
// shared by the full walk (WalkLocal) and the one-folder listing a live
// change triggers (ListLocalDir). Two copies would disagree about some name
// sooner or later, and a name one reader tracks and the other ignores reads
// as "deleted here" on every other pass.
func localEntry(rel string, d fs.DirEntry) (Node, entryVerdict) {
	name := d.Name()
	if skipName(name) {
		return Node{}, entryIgnore
	}
	// Half-written downloads from an interrupted run. Never sync material:
	// treating one as a user file uploads crash debris to the server with a
	// name nobody chose.
	if strings.HasPrefix(name, ".filex-part-") {
		return Node{}, entryIgnore
	}
	// Symlinks are not followed. A link pointing outside the pair would upload
	// files the user never put in the folder, and a link pointing inside it
	// makes the walk infinite.
	if d.Type()&fs.ModeSymlink != 0 {
		return Node{}, entrySkipped
	}
	if rel == "" {
		return Node{}, entryIgnore
	}
	if d.IsDir() {
		return Node{Rel: rel, IsDir: true}, entryTracked
	}
	info, err := d.Info()
	if err != nil || !info.Mode().IsRegular() {
		return Node{}, entrySkipped
	}
	return Node{Rel: rel, Size: info.Size(), ModMillis: info.ModTime().UnixMilli()}, entryTracked
}

// ListLocalDir snapshots ONE local folder (dir is pair-relative, "" = the
// root) without descending: the direct children only, keyed by their full
// pair-relative path. It is the local half of a targeted pass — a change
// announced for one folder is reconciled by looking at that folder, not by
// walking the whole tree. The folder itself missing is reported as
// os.ErrNotExist so the caller can widen the scope to its parent.
func ListLocalDir(root, dir string) (Snapshot, error) {
	abs := root
	if dir != "" {
		p, err := localPathOf(root, dir)
		if err != nil {
			return nil, err
		}
		abs = p
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := Snapshot{}
	for _, d := range entries {
		rel := d.Name()
		if dir != "" {
			rel = dir + "/" + d.Name()
		}
		if n, v := localEntry(rel, d); v == entryTracked {
			out[rel] = n
		}
	}
	return out, nil
}

// ListRemoteDir is ListLocalDir's server half: one listing, direct children
// only, keyed by full pair-relative path.
func ListRemoteDir(ctx context.Context, api RemoteLister, remoteRoot, dir string) (Snapshot, error) {
	l, err := api.List(ctx, joinRemote(remoteRoot, dir))
	if err != nil {
		return nil, err
	}
	out := Snapshot{}
	for _, f := range l.Files {
		if skipName(f.Basename) {
			continue
		}
		rel := f.Basename
		if dir != "" {
			rel = dir + "/" + f.Basename
		}
		if f.IsDir {
			out[rel] = Node{Rel: rel, IsDir: true}
			continue
		}
		out[rel] = Node{Rel: rel, Size: f.Size, ModMillis: f.LastModified}
	}
	return out, nil
}

func relOf(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// listWorkers is how many directory listings run at once inside WalkRemote.
// One round-trip per folder is what makes this walk the slow phase of a big
// sync: measured on a live deployment behind a CDN proxy (~0.35s per
// request), a 3,328-folder tree took ~19 minutes to list serially — twice
// per run, since the settle pass walks again. Eight at a time turns that
// into a couple of minutes without hammering the server: listings are cheap
// index reads, and they multiplex over one HTTP/2 connection anyway.
const listWorkers = 8

// ErrRemoteNotFound marks a listing error that means "this folder does not
// exist on the server" — the ONE kind of failure WalkRemote may skip (a
// folder deleted between its parent's listing and its own). The API adapter
// wraps a 404 with it.
//
// Every other error fails the walk. A folder that merely could not be listed
// — a timeout, a 5xx, the half-dead connection this client now detects — must
// never come back as "gone from the server": the planner would read that as
// a delete and bin the local copy of the whole subtree, and if it happened in
// the settle pass the baseline would lose the subtree instead, turning every
// uploaded file in it into a conflict pair on the next round. The walk is
// eight listings wide now, so one bad second can touch eight folders at once.
var ErrRemoteNotFound = errors.New("remote folder not found")

// WalkRemote snapshots a server folder by listing it breadth-first, one
// level at a time with listWorkers concurrent listings per level. The
// snapshot itself is only written by this goroutine — workers hand their
// listings back and the merge stays single-threaded.
//
// remoteRoot is an `adapter://path` prefix; every returned Node.Rel is relative
// to it, so local and remote snapshots share one key space and the planner can
// compare them directly.
//
// progress, when non-nil, is called after each merged directory with the
// running totals.
func WalkRemote(ctx context.Context, api RemoteLister, remoteRoot string, progress func(dirsListed, itemsSeen int)) (Snapshot, error) {
	out := Snapshot{}
	dirsListed := 0
	level := []string{""}
	for len(level) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		type listed struct {
			rel     string
			listing *Listing
			err     error
		}
		results := make([]listed, len(level))
		sem := make(chan struct{}, listWorkers)
		var wg sync.WaitGroup
		for i, rel := range level {
			wg.Add(1)
			go func(i int, rel string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if err := ctx.Err(); err != nil {
					results[i] = listed{rel: rel, err: err}
					return
				}
				l, err := api.List(ctx, joinRemote(remoteRoot, rel))
				results[i] = listed{rel: rel, listing: l, err: err}
			}(i, rel)
		}
		wg.Wait()

		var next []string
		for _, r := range results {
			if r.err != nil {
				if r.rel != "" && errors.Is(r.err, ErrRemoteNotFound) {
					// A folder that vanished mid-walk is not fatal; the next
					// run sees it.
					continue
				}
				return nil, fmt.Errorf("list %s: %w", joinRemote(remoteRoot, r.rel), r.err)
			}
			dirsListed++
			for _, f := range r.listing.Files {
				if skipName(f.Basename) {
					continue
				}
				childRel := f.Basename
				if r.rel != "" {
					childRel = r.rel + "/" + f.Basename
				}
				if f.IsDir {
					out[childRel] = Node{Rel: childRel, IsDir: true}
					next = append(next, childRel)
					continue
				}
				out[childRel] = Node{
					Rel:       childRel,
					Size:      f.Size,
					ModMillis: f.LastModified,
				}
			}
			if progress != nil {
				progress(dirsListed, len(out))
			}
		}
		level = next
	}
	return out, nil
}

// joinRemote appends a pair-relative path to an `adapter://root` prefix.
func joinRemote(root, rel string) string {
	if rel == "" {
		return root
	}
	if strings.HasSuffix(root, "/") {
		return root + rel
	}
	return root + "/" + rel
}

// localPathOf resolves a pair-relative path against the local root and refuses
// anything that would land outside it.
//
// ⚠ Names come from the SERVER. A listing carrying `..` or an absolute path
// would otherwise let a compromised or buggy server write anywhere on the disk
// of everyone syncing with it.
func localPathOf(root, rel string) (string, error) {
	if rel == "" || path.IsAbs(rel) || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("refusing suspicious path %q", rel)
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("refusing suspicious path %q", rel)
		}
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	// Belt and braces: even with the segment check above, confirm the result is
	// still under the root after Join has cleaned it.
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if fullAbs != rootAbs && !strings.HasPrefix(fullAbs, rootAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("refusing path outside the sync folder: %q", rel)
	}
	return full, nil
}
