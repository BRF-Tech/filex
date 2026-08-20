// Package filesync keeps a local folder and a remote filex folder in step.
//
// ⚠ Not named `sync`: internal/sync already means something else here
// (walking a storage to refresh the catalog). This package is the Dropbox-shaped
// thing — a folder on someone's disk mirrored against a server path.
//
// The design is deliberately split in two:
//
//   - plan.go is a PURE function. Local snapshot + remote snapshot + the
//     baseline from the last run go in, a list of actions comes out. No disk, no
//     network, no clock. This is where every decision about someone's files is
//     made, and it is decided in code that a test can drive through every branch
//     without a server.
//   - engine.go performs those actions and writes the new baseline.
//
// Sync code deletes files for a living. The rules below are chosen so that the
// failure mode is "too many copies", never "the file is gone".
package filesync

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

// Side names the two halves of a pair. Used in conflict copy names so the
// surviving file says where it came from.
type Side string

const (
	SideLocal  Side = "local"
	SideRemote Side = "server"
)

// Node is one file or directory in a snapshot, keyed by its slash-separated
// path relative to the pair root ("" is the root itself and never appears).
type Node struct {
	Rel   string
	IsDir bool
	Size  int64
	// ModMillis is the modification time in Unix milliseconds. Local and remote
	// clocks are NEVER compared to each other — see Signature.
	ModMillis int64
}

// Signature is the cheap "has this changed?" test for one side.
//
// ⚠ Local and remote signatures are never compared against each other. An
// upload gives the server its own mtime, so the two sides legitimately differ
// the instant after a successful sync; comparing them would make every file look
// permanently conflicted. Instead each side is compared against what IT looked
// like at the end of the last run, which is what the baseline stores.
func (n Node) Signature() string {
	if n.IsDir {
		return "dir"
	}
	return fmt.Sprintf("%d:%d", n.Size, n.ModMillis)
}

// Snapshot is one side's tree, indexed by relative path.
type Snapshot map[string]Node

// BaselineEntry records what a path looked like on BOTH sides when they were
// last agreed. Absence from the baseline means "never synced".
type BaselineEntry struct {
	Local  string `json:"local"`  // Signature() of the local node
	Remote string `json:"remote"` // Signature() of the remote node
	IsDir  bool   `json:"is_dir"`
}

// Baseline is the state carried between runs.
type Baseline map[string]BaselineEntry

// ActionKind is what the engine should do about one path.
type ActionKind string

const (
	ActionUpload      ActionKind = "upload"       // local → remote
	ActionDownload    ActionKind = "download"     // remote → local
	ActionMkdirRemote ActionKind = "mkdir_remote" // create a folder on the server
	ActionMkdirLocal  ActionKind = "mkdir_local"  // create a folder on disk
	ActionDeleteLocal ActionKind = "delete_local" // remove locally (into local trash)
	ActionDeleteRemot ActionKind = "delete_remote"
	// ActionConflict keeps both versions: the remote copy is downloaded beside
	// the local one under ConflictName, then the local file is uploaded.
	ActionConflict ActionKind = "conflict"
)

// Action is one unit of work. Rel is always the pair-relative path.
type Action struct {
	Kind ActionKind
	Rel  string
	// ConflictName is set only for ActionConflict: the name the SERVER's copy
	// is saved under locally so the user's own edit keeps its filename.
	ConflictName string
	// Reason is human-readable and shown in `filex sync run` output. Sync is
	// the one subsystem where people need to see why their file moved.
	Reason string
	// RemoteMod carries the server file's mtime (Unix millis) on download
	// and conflict actions, so the local copy can be stamped with it. 0 =
	// the storage reported none.
	RemoteMod int64
}

// Options tunes a plan.
type Options struct {
	// FirstRun suppresses every delete. With no baseline there is no way to
	// tell "you deleted this" from "you have not downloaded this yet", and
	// guessing wrong empties someone's folder. A first run is a union merge.
	FirstRun bool
	// Now stamps conflict copy names. Zero value means the caller did not care
	// (tests); the engine always sets it.
	Now time.Time
}

// Plan decides what to do. It is pure: same inputs, same output, no I/O.
//
// The rules, in the order they are applied per path:
//
//	both sides unchanged since the baseline      → nothing
//	present on one side only, never synced       → copy it to the other side
//	changed on one side only                     → copy that change over
//	changed on both sides, and differently       → conflict (keep both)
//	gone from one side, unchanged on the other   → delete the other (not on a first run)
//	gone from one side, CHANGED on the other     → resurrect: copy the change back
//
// The last rule is the one that matters most: a delete never beats an edit.
func Plan(local, remote Snapshot, base Baseline, opts Options) []Action {
	var out []Action

	// Every path either side knows about, plus everything we synced before (so
	// deletions, which appear in neither snapshot, are still considered).
	seen := make(map[string]bool, len(local)+len(remote)+len(base))
	for rel := range local {
		seen[rel] = true
	}
	for rel := range remote {
		seen[rel] = true
	}
	for rel := range base {
		seen[rel] = true
	}

	rels := make([]string, 0, len(seen))
	for rel := range seen {
		rels = append(rels, rel)
	}
	// Shallow paths first so a directory is created before anything inside it,
	// and so deletes of a tree are reported top-down.
	sort.Slice(rels, func(i, j int) bool {
		di, dj := strings.Count(rels[i], "/"), strings.Count(rels[j], "/")
		if di != dj {
			return di < dj
		}
		return rels[i] < rels[j]
	})

	for _, rel := range rels {
		l, hasL := local[rel]
		r, hasR := remote[rel]
		b, hasB := base[rel]

		switch {
		// ── directories ────────────────────────────────────────────────
		// Folders carry no content, so they are only ever created, never
		// conflicted. Deleting them is left to the file rules: once a folder is
		// empty on one side because its files were deleted, the delete branch
		// below removes it.
		case hasL && l.IsDir && !hasR:
			if hasB && !opts.FirstRun {
				out = append(out, Action{Kind: ActionDeleteLocal, Rel: rel,
					Reason: "folder removed on the server"})
				continue
			}
			out = append(out, Action{Kind: ActionMkdirRemote, Rel: rel,
				Reason: "new folder"})
			continue
		case hasR && r.IsDir && !hasL:
			if hasB && !opts.FirstRun {
				out = append(out, Action{Kind: ActionDeleteRemot, Rel: rel,
					Reason: "folder removed locally"})
				continue
			}
			out = append(out, Action{Kind: ActionMkdirLocal, Rel: rel,
				Reason: "new folder on the server"})
			continue
		case hasL && hasR && l.IsDir && r.IsDir:
			continue

		// ⚠ A folder on one side and a file on the other. The server-side guard
		// (internal/storage kind guard) refuses this collision; doing it here
		// too means the local disk never gets into that state either. Neither
		// side wins — the user is told and nothing is touched.
		case hasL && hasR && l.IsDir != r.IsDir:
			out = append(out, Action{Kind: ActionConflict, Rel: rel,
				ConflictName: conflictName(rel, SideRemote, opts.Now),
				Reason:       "one side has a folder where the other has a file"})
			continue

		// ── files ──────────────────────────────────────────────────────
		case hasL && hasR:
			// Twins with no history adopt each other: same size, same mtime,
			// no baseline row. That is exactly what an interrupted first run
			// leaves behind — downloads carry the server's own mtime — and
			// without this rule, RESUMING one conflicted every finished file:
			// thousands of "(remote copy)" duplicates from a single restart.
			if !hasB && l.Signature() == r.Signature() {
				continue
			}
			lChanged := !hasB || b.Local != l.Signature()
			rChanged := !hasB || b.Remote != r.Signature()
			switch {
			case !lChanged && !rChanged:
				// In step.
			case lChanged && !rChanged:
				out = append(out, Action{Kind: ActionUpload, Rel: rel,
					Reason: "changed locally"})
			case !lChanged && rChanged:
				out = append(out, Action{Kind: ActionDownload, Rel: rel,
					RemoteMod: r.ModMillis, Reason: "changed on the server"})
			default:
				// Both moved. Same size is not proof of same content, so we do
				// not try to be clever: keep both and let the person decide.
				out = append(out, Action{Kind: ActionConflict, Rel: rel,
					ConflictName: conflictName(rel, SideRemote, opts.Now),
					RemoteMod:    r.ModMillis,
					Reason:       "changed in both places"})
			}

		case hasL && !hasR:
			switch {
			case !hasB:
				out = append(out, Action{Kind: ActionUpload, Rel: rel, Reason: "new file"})
			case opts.FirstRun:
				out = append(out, Action{Kind: ActionUpload, Rel: rel,
					Reason: "first run — nothing is deleted"})
			case b.Local != l.Signature():
				// Deleted on the server, but edited here since. The edit wins.
				out = append(out, Action{Kind: ActionUpload, Rel: rel,
					Reason: "deleted on the server but edited here — kept"})
			default:
				out = append(out, Action{Kind: ActionDeleteLocal, Rel: rel,
					Reason: "deleted on the server"})
			}

		case !hasL && hasR:
			switch {
			case !hasB:
				out = append(out, Action{Kind: ActionDownload, Rel: rel,
					RemoteMod: r.ModMillis, Reason: "new file on the server"})
			case opts.FirstRun:
				out = append(out, Action{Kind: ActionDownload, Rel: rel,
					RemoteMod: r.ModMillis, Reason: "first run — nothing is deleted"})
			case b.Remote != r.Signature():
				out = append(out, Action{Kind: ActionDownload, Rel: rel,
					RemoteMod: r.ModMillis, Reason: "deleted here but edited on the server — kept"})
			default:
				out = append(out, Action{Kind: ActionDeleteRemot, Rel: rel,
					Reason: "deleted locally"})
			}

		default:
			// In the baseline but on neither side: both ends deleted it. Drop
			// the baseline row by simply not emitting anything; Apply rebuilds
			// the baseline from the post-run snapshots.
		}
	}

	// Deletes must run deepest-first, or removing a folder fails because its
	// children are still there. Everything else is already shallow-first.
	sort.SliceStable(out, func(i, j int) bool {
		di := isDelete(out[i].Kind)
		dj := isDelete(out[j].Kind)
		if di != dj {
			return !di // non-deletes first
		}
		if !di {
			return false // keep the shallow-first order established above
		}
		return strings.Count(out[i].Rel, "/") > strings.Count(out[j].Rel, "/")
	})
	return out
}

func isDelete(k ActionKind) bool {
	return k == ActionDeleteLocal || k == ActionDeleteRemot
}

// conflictName builds "report (server copy 2026-08-07 14-05).xlsx".
//
// The extension is preserved so the copy still opens in the right application —
// a conflict file the user cannot double-click is a conflict file they ignore.
func conflictName(rel string, from Side, now time.Time) string {
	base := path.Base(rel)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if now.IsZero() {
		return fmt.Sprintf("%s (%s copy)%s", stem, from, ext)
	}
	return fmt.Sprintf("%s (%s copy %s)%s", stem, from, now.Format("2006-01-02 15-04"), ext)
}

// NextBaseline rebuilds the carried state from the two snapshots taken AFTER a
// run. Paths still disagreeing (a conflict nobody resolved) are left out, so the
// next run looks at them again rather than assuming they are settled.
func NextBaseline(local, remote Snapshot) Baseline {
	out := make(Baseline, len(local))
	for rel, l := range local {
		r, ok := remote[rel]
		if !ok || r.IsDir != l.IsDir {
			continue
		}
		out[rel] = BaselineEntry{Local: l.Signature(), Remote: r.Signature(), IsDir: l.IsDir}
	}
	return out
}
