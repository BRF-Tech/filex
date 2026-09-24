// Package syspath is the ONE definition of the directories filex keeps for
// itself inside a storage, and of how a path is judged against them.
//
// filex writes its own machinery next to people's files: the soft-delete bin
// (`.filex-trash`), version history (`.versions`), legacy thumbnails
// (`.thumbs`), the desktop app's "open with filex" working copies
// (`.filex-open`) and the empty-folder marker (`.keepdir`). None of it is
// anybody's document, and a person must never be shown it — not in a listing,
// a search hit, a share, an archive, a notification's text or a notification's
// target — nor write there (Refused: every person-facing write asks it, and
// only the desktop's open-with round trip gets through).
//
// ⚠⚠ Why a package of its own. Until this one existed the list was written by
// hand in seventeen places in the backend alone (the five protocol servers,
// the two listing projectors, the AI surface three times, share browsing, the
// archive walk, the cross-storage copy, versioning, the antivirus eligibility
// check, the thumbnail backfill twice) and no two of them agreed.
// `.filex-open` was known to exactly one of them — the thumbnail backfill — so
// the listing API returned it, global search returned the working copies
// inside it, and a notification about a desktop "open with" save targeted
// `.filex-open/<session>-<name>` and opened it (reported by the owner,
// 2026-09-21). `.versions` was missing from share browsing, the AI listing and
// the AI archive walk. A second copy of this list is a second chance to forget
// a name; import this package instead.
//
// ⚠ The client carries the same list (packages/core/src/lib/internalPaths.ts)
// for the explorer's own guards, and web/tests/lib/internalPaths.test.ts parses
// THIS file and fails when the two differ. Add a name here and the build tells
// you where else it has to go.
//
// Leaf package on purpose: it imports nothing from filex, so trash, versioning,
// notify, every protocol server and every handler can depend on it without a
// cycle.
package syspath

import (
	"errors"
	"path"
	"regexp"
	"strings"
)

// The internal directory names. Each lives at a storage's root today, but they
// are matched as ANY path segment (see InDir) — which is what every protocol
// server already did, and the only rule that also covers a shared sub-folder
// or a confined root whose own children are judged relative to it.
const (
	// Trash is the soft-delete bin. Its contents are reached only through the
	// trash API (listed by ORIGINAL path, restored by node id), never by path.
	Trash = ".filex-trash"
	// Versions holds version snapshots, `.versions/<node id>/<n>`. Reached only
	// through the versions API.
	Versions = ".versions"
	// Thumbs is the legacy in-storage thumbnail cache. Thumbnails are served
	// by node id from /api/files/thumb, never by path.
	Thumbs = ".thumbs"
	// OpenWith is the desktop app's "open with filex" working area
	// (desktop/src/openwith.ts SCRATCH_DIR_NAME): a local document is uploaded
	// here as `<session>-<name>`, edited in the browser editor, and the result
	// is written back over the ORIGINAL file on the person's own computer.
	//
	// ⚠⚠ Hidden, but NOT sealed. Every desktop release since 0.29.0 creates it
	// (`action=newfolder`), uploads into it, and — the part that matters —
	// stats its copy by LISTING this folder (`action=index`) and downloads it
	// by exact path. Refusing any of those would not show an error: the
	// desktop's change detection reads "not there" as "not changed" and the
	// edit is silently never written back. So the server serves this folder
	// to whoever asks for it by exact path and offers it to nobody.
	OpenWith = ".filex-open"
)

// KeepMarker is the zero-byte file filex writes into a folder it creates, so a
// blob store (which has no directories, only keys) keeps the empty folder. It
// is a FILE, not a directory, which is why it is not in Dirs: a protocol client
// that deletes a folder recursively must still be able to remove it.
const KeepMarker = ".keepdir"

// dirs is the closed set, in the order web/tests/lib/internalPaths.test.ts
// expects to read it. Keep the declaration on one line — that test parses it.
var dirs = []string{Trash, Versions, Thumbs, OpenWith}

// Dirs returns the internal directory names. A copy: callers cannot edit the
// set by appending to what they were handed.
func Dirs() []string {
	out := make([]string, len(dirs))
	copy(out, dirs)
	return out
}

// IsDirName reports whether one path segment names an internal directory.
func IsDirName(name string) bool {
	switch strings.TrimSpace(name) {
	case Trash, Versions, Thumbs, OpenWith:
		return true
	}
	return false
}

// IsName reports whether a single listing entry is filex's own: an internal
// directory or the keep marker. What a person-facing listing asks about each
// child it is about to return.
func IsName(name string) bool {
	return IsDirName(name) || strings.TrimSpace(name) == KeepMarker
}

// segments splits a storage-relative path into its segments. It accepts the
// shapes that reach it in practice — `a/b`, `/a/b/`, `a\b`, and a wire path
// `<storage>://a/b` — because a guard that only understands one of them is a
// guard with a hole the size of the others.
func segments(rel string) []string {
	p := strings.ReplaceAll(strings.TrimSpace(rel), "\\", "/")
	if i := strings.Index(p, "://"); i >= 0 {
		p = p[i+3:]
	}
	p = strings.Trim(path.Clean("/"+p), "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// InDir reports whether rel IS an internal directory or lies inside one, at
// any depth. The rule the protocol servers (WebDAV, SFTP, FTPS, NFS, S3) apply
// to every path they are asked about.
func InDir(rel string) bool {
	for _, seg := range segments(rel) {
		if IsDirName(seg) {
			return true
		}
	}
	return false
}

// Hidden reports whether a person must never be shown rel: it is inside an
// internal directory, or it is a keep marker. The rule every person-facing
// listing, search, recent/starred/tag view, share and notification applies.
func Hidden(rel string) bool {
	segs := segments(rel)
	for _, seg := range segs {
		if IsDirName(seg) {
			return true
		}
	}
	return len(segs) > 0 && segs[len(segs)-1] == KeepMarker
}

// Sealed reports whether rel must not be SERVED by path at all to a
// person-facing file API: it is inside the trash, the version history or the
// thumbnail cache. Each has its own API, keyed by something other than the
// path, and nothing legitimate ever asks for these by path.
//
// ⚠ `.filex-open` is deliberately not sealed — see OpenWith for the desktop
// clients that would lose edits silently if it were.
func Sealed(rel string) bool {
	for _, seg := range segments(rel) {
		switch seg {
		case Trash, Versions, Thumbs:
			return true
		}
	}
	return false
}

// openWithSession is the `<session>-` prefix the desktop puts IN FRONT of the
// original name (desktop/src/openwith.ts scratchBasename: 6 random bytes as
// 12 lowercase hex, then `-`, then the original stem and extension). A shorter
// hex run is accepted too, so a copy made by a test fixture or a future
// shorter id still maps back.
var openWithSession = regexp.MustCompile(`^[0-9a-f]{1,32}-`)

// OpenWithOriginal maps a desktop "open with filex" working copy back to the
// name of the document it is a copy of: `.filex-open/a1b2c3d4e5f6-Bütçe.xlsx`
// → `Bütçe.xlsx`, ok=true. Anything else — a path outside the working area, a
// sub-folder in it, a name without the session prefix — is ok=false.
//
// ⚠⚠ The NAME is all there is to map back to. The original lives on the
// person's own computer (a document that already had a copy on this server —
// a synced one — is edited in place and never gets a working copy at all), so
// no path on any storage names it and no click can open it. The desktop keeps
// the local path to itself; the server is never told where on somebody's
// machine a file sits, and it must not be, because every notification is also
// a webhook body.
//
// The name is the desktop's cleaned-up spelling: characters no file system
// accepts (`\ / : * ? " < > |`) arrive as `_`, the extension is lower-cased
// and a stem over 60 characters is cut. For the names people actually use —
// `Bütçe Özeti.xlsx`, `Rapor 2026.docx` — it is the original, byte for byte.
func OpenWithOriginal(rel string) (string, bool) {
	segs := segments(rel)
	if len(segs) != 2 || segs[0] != OpenWith {
		return "", false
	}
	loc := openWithSession.FindStringIndex(segs[1])
	if loc == nil {
		return "", false
	}
	name := segs[1][loc[1]:]
	if name == "" {
		return "", false
	}
	return name, true
}

// ── what a person may WRITE ─────────────────────────────────────────────

// ErrReserved is what a person-facing mutation gets when the path it names is
// one of filex's own (Refused). Handlers answer it as 403 `RESERVED_NAME`.
var ErrReserved = errors.New("reserved for filex's own use")

// ReservedError is ErrReserved for one path, from a service that refuses a
// mutation (Check) — it keeps the path so the answer can name the part of it
// that is filex's.
type ReservedError struct{ Rel string }

func (e *ReservedError) Error() string {
	return `"` + Reserved(e.Rel) + `" is ` + ErrReserved.Error()
}

func (e *ReservedError) Unwrap() error { return ErrReserved }

// Check is Refused as an error: nil, or a *ReservedError naming the first of
// rels that is refused. For services that answer with errors rather than
// HTTP (the operations queue, an app's output, archive extraction).
func Check(v Verb, rels ...string) error {
	for _, rel := range rels {
		if Refused(v, rel) {
			return &ReservedError{Rel: rel}
		}
	}
	return nil
}

// Verb says which person-facing mutation is asking about a path — only so the
// desktop's open-with round trip can be told apart from everything else.
type Verb int

const (
	// Change is every mutation that is not one of the three below: new
	// folder anywhere else, new file, rename, move, copy, archive
	// extraction and creation, text save, chunked and staged upload, share,
	// grant, restore, an app's output. Refused on every Hidden path.
	Change Verb = iota
	// MakeWorkArea is `action=newfolder` creating `<storage>://.filex-open`
	// (desktop/src/openwith-io.ts ensureScratchDir).
	MakeWorkArea
	// PutWorkCopy is `action=upload` of `<session>-<name>` into it
	// (uploadFile) and the document editor's save of that copy (the
	// OnlyOffice config the desktop's editor window asks for).
	PutWorkCopy
	// DropWorkCopy is `action=delete` of that copy after it was written back
	// (deleteRemote).
	DropWorkCopy
	// Mounted is a protocol client's write (WebDAV, SFTP, FTPS, NFS, S3):
	// filex's directories are refused (InDir), and the keep marker is an
	// ordinary file there — a client deleting a folder recursively must be
	// able to remove it, and a sync tool copying one must be able to write
	// it, or the folder can never be emptied or mirrored.
	Mounted
)

// Refused reports whether a person-facing mutation must be refused because
// rel names filex's own machinery: anything Hidden, except the one shape the
// desktop app's "open with filex" writes.
//
// ⚠⚠ The exception is exact on purpose. Every desktop release since 0.29.0
// creates `.filex-open` at the storage ROOT with `action=newfolder`, uploads
// `<12 hex>-<name>` straight into it with `action=upload`, lets the editor
// save that copy, and deletes it with `action=delete` — and refusing any one
// of those does not show an error anywhere a person looks: the upload
// failing ends the open with a dialog, a refused save is an edit that never
// comes back, and a refused delete leaves copies behind forever. Nothing
// else is let through: not a sub-folder in the area, not a copy without the
// session prefix, not a rename or move into or out of it, not an archive
// extracted into it, not the area anywhere but at the root.
func Refused(v Verb, rel string) bool {
	if !Hidden(rel) {
		return false
	}
	segs := segments(rel)
	switch v {
	case Mounted:
		return InDir(rel)
	case MakeWorkArea:
		return !(len(segs) == 1 && segs[0] == OpenWith)
	case PutWorkCopy, DropWorkCopy:
		return !isWorkCopy(segs)
	}
	return true
}

// isWorkCopy: exactly `.filex-open/<session>-<name>`, where the name left
// after the session prefix is not itself one of filex's names.
func isWorkCopy(segs []string) bool {
	if len(segs) != 2 || segs[0] != OpenWith {
		return false
	}
	loc := openWithSession.FindStringIndex(segs[1])
	if loc == nil {
		return false
	}
	rest := segs[1][loc[1]:]
	return rest != "" && !IsName(rest) && !IsName(segs[1])
}

// Reserved names the part of rel that made Refused say no — the first
// internal directory in it, or the keep marker — for an error a person can
// read. "" when rel is not Hidden.
func Reserved(rel string) string {
	segs := segments(rel)
	for _, seg := range segs {
		if IsDirName(seg) {
			return seg
		}
	}
	if len(segs) > 0 && segs[len(segs)-1] == KeepMarker {
		return KeepMarker
	}
	return ""
}
