// Package writegate is the one question every person-facing write asks
// before it touches a storage: may this path be changed?
//
// Two rules, one call, so that a door cannot honour one and forget the other:
//
//   - filex's own names (syspath): `.filex-trash`, `.versions`, `.thumbs`,
//     the desktop's `.filex-open` and the `.keepdir` marker are never written
//     by a person — except the desktop's open-with round trip, which the
//     caller claims with a syspath.Verb (syspath.Refused).
//   - app locks: while an app holds a lock on a path (the signing app freezes
//     a document while signatures are collected, `files:lock`), nothing
//     changes it, moves it or removes the folder around it — whoever asks,
//     administrators included — except the app that holds the lock.
//
// ⚠⚠ Why one package (2026-09-21). The lock check lived in five handlers
// (the explorer's verbs, the queue, the text editor, two upload paths) and
// nowhere else. Measured before this package: the document server's save
// callback overwrote a frozen document ({"error":0}); an agent's delete of the
// folder holding it answered {"ok":true}; an archive extracted over it; a
// WebDAV DELETE of its folder answered 204; FTP and SFTP sessions opened
// before the freeze overwrote and renamed it; NFS renamed it. The signing
// app's own hash check caught the change later — the request then died as
// "the document changed", and the freeze the person was promised on screen
// ("… dondurulmuş; imzalar toplanırken kimse değiştiremez") was never real.
// The reserved-name rule had just been written into the same doors, one call
// each; the lock rule now rides in that call.
package writegate

import (
	"context"
	"errors"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// Target is one path a write names.
type Target struct {
	Rel string
	// Verb is the syspath exception this target may claim (the desktop's
	// open-with round trip); syspath.Change — the zero value — for every
	// other write.
	Verb syspath.Verb
	// named: the path is named by the write but not changed by it.
	named bool
}

// Writes is a path the write changes: its content is created or replaced, or
// the entry is created, moved or removed — and, for a folder, everything
// under it goes along. Judged on reserved names AND on app locks on the path
// or anywhere below it.
func Writes(rel string) Target { return Target{Rel: rel} }

// Names is a path the write names without changing it: the folder a file is
// created, moved or extracted INTO, a source that is only read (a copy, an
// archive being packed or unpacked), the target of a share or a grant.
// Judged on reserved names only — a lock freezes a document and the folder
// entry that holds it, not what may be added beside it.
func Names(rel string) Target { return Target{Rel: rel, named: true} }

// As claims the syspath exception v for this target.
func (t Target) As(v syspath.Verb) Target { t.Verb = v; return t }

// Locks answers which app lock covers a path: on it (Lock) or on it or
// anything under it (LockWithin). *acl.Set implements it; a nil *acl.Set
// answers "none".
type Locks interface {
	Lock(rel string) *model.AppPluginLock
	LockWithin(rel string) *model.AppPluginLock
}

// ErrLocked is what a refused write wraps when an app holds a lock.
var ErrLocked = errors.New("locked by an app")

// LockedError names the lock that refused a write and the path it was asked
// about, so the answer can say who froze it and why (handlers.lockedAnswer).
type LockedError struct {
	Lock *model.AppPluginLock
	Rel  string
}

func (e *LockedError) Error() string {
	return "locked by app " + e.Lock.PluginName + ": " + e.Rel
}

func (e *LockedError) Unwrap() error { return ErrLocked }

// Check reports whether a write that names targets may happen: nil, a
// *syspath.ReservedError, or a *LockedError. Reserved names are judged first
// and without a database: a name that can never be written is refused the
// same way whether or not anything is locked.
//
// app is the app plugin doing the write — 0 for a person, a protocol client
// or a document server. A lock held by that same app does not stop it: the
// app that froze the document is the one that must still write into it (the
// signed output), exactly the waiver acl.Set.EffectiveIgnoringLocks gives
// its jobs.
//
// locks may be nil (no ACL resolver wired): then only names are judged.
func Check(locks Locks, app int64, targets ...Target) error {
	for _, t := range targets {
		if syspath.Refused(t.Verb, t.Rel) {
			return &syspath.ReservedError{Rel: t.Rel}
		}
	}
	if locks == nil {
		return nil
	}
	for _, t := range targets {
		if t.named {
			continue
		}
		if l := locks.LockWithin(t.Rel); l != nil && (app == 0 || l.PluginID != app) {
			return &LockedError{Lock: l, Rel: t.Rel}
		}
	}
	return nil
}

type appKey struct{}

// WithApp marks ctx as carrying a write BY app plugin id — a job's output
// being committed (wasmplugin's job runner sets it; handlers.AppPlugins reads
// it with AppFrom). Only that path sets it: every other write is a person's,
// and a person is never the lock holder.
func WithApp(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, appKey{}, id)
}

// AppFrom is the app plugin id WithApp put on ctx, or 0.
func AppFrom(ctx context.Context) int64 {
	id, _ := ctx.Value(appKey{}).(int64)
	return id
}

// RefusesMounted is Check for a protocol client (WebDAV, SFTP, FTPS, NFS, S3):
// filex's own directories (syspath.Mounted — the keep marker is an ordinary
// file there) and the storage's live locks.
//
// ⚠ Pass locks read NOW (acl.Resolver.Locks), not a session's cached
// permission set: SFTP, FTPS and NFS load that set once when the session
// starts, so it does not see a freeze taken afterwards. Measured before this
// (2026-09-21): a session opened before the signing app froze imza/NDA.docx
// overwrote it, renamed it and renamed the folder holding it.
func RefusesMounted(locks Locks, rel string) bool {
	return Check(locks, 0, Writes(rel).As(syspath.Mounted)) != nil
}
