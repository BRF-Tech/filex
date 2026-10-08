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
//
// A third rule rides in it since the vault (encryption level 3,
// docs/E2E-VAULT-FORMAT.md → Writes from anywhere else): inside a vault folder
// only the vault API writes. Its packs and index files are written whole,
// once, under a write lock the server keeps; a file dropped into `v/`, a pack
// renamed or deleted from WebDAV, an archive extracted into the vault would be
// writes the vault's index knows nothing about - at best litter, at worst a
// pack an index still names, gone. Every door asks Check already, so every
// door refuses them: the explorer's file API, the queue, uploads, the agent
// API and MCP, ShareX, tickets, file requests, archives, apps, the document
// server, WebDAV, SFTP, FTPS, NFS and S3. The vault folder ITSELF stays an
// ordinary encrypted folder - renamed, moved, deleted whole or copied - and
// its key file is rewritten through its usual door (a password change, an
// escrow slot), which claims RewritesKeyFile and checks what it writes.
package writegate

import (
	"context"
	"errors"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// Target is one path a write names.
type Target struct {
	Rel string
	// Verb is the syspath exception this target may claim (the desktop's
	// open-with round trip, a person's own draft); syspath.Change — the zero
	// value — for every other write.
	Verb syspath.Verb
	// Person is who the write is FOR, when the exception depends on it
	// (syspath.OwnDraft: a draft is written only by its owner's editor). 0
	// when nobody is named, which is what every other write passes.
	Person int64
	// named: the path is named by the write but not changed by it.
	named bool
	// vault: the vault API's own write (ForVault) - the one writer of what is
	// inside a vault folder.
	vault bool
	// keyFile: a rewrite of a vault's key file through its usual door
	// (RewritesKeyFile), which checks the new bytes itself.
	keyFile bool
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

// By names the person the write is for (syspath.RefusedBy): the owner of a
// draft is the only one whose OwnDraft claim is honoured.
func (t Target) By(person int64) Target { t.Person = person; return t }

// ForVault claims the vault API's exception: the target is a pack, an index
// file, the temporary index or the vault's own folder and key file, written
// by POST/PUT /api/files/e2e/vault/* under its write lock. Filex's own names
// and app locks are judged as for every write. No other door claims it.
func (t Target) ForVault() Target { t.vault = true; return t }

// RewritesKeyFile claims that the write replaces a vault's key file with new
// bytes the door has checked keep its `v`, `req` and `vault` (e2e.
// SameVaultBlock) - the password change, the reset with the recovery key, an
// escrow slot added or declined, which the browser writes through the
// explorer's upload. Without the claim a vault's key file is never written,
// renamed, moved or deleted on its own (ErrVaultKeyFile).
func (t Target) RewritesKeyFile() Target { t.keyFile = true; return t }

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

// Vaults answers which vault folder holds a path, or is it (root "" for a
// vault at the storage's root): acl.Set does, through the resolver's vault
// finder (internal/vaultlock.Finder), when a Locks value implements it.
type Vaults interface {
	VaultRoot(rel string) (root string, ok bool)
}

// ErrVaultPath is what a refused write wraps when it touches something
// strictly inside a vault folder: only the vault API writes there.
var ErrVaultPath = errors.New("inside a vault, only the vault API writes")

// ErrVaultKeyFile is what a refused write wraps when it would change a
// vault's key file without the door's RewritesKeyFile claim - or when a
// door's check of the new bytes failed (e2e.SameVaultBlock).
var ErrVaultKeyFile = errors.New("a vault's key file keeps its v, req and vault, and is not removed or moved on its own")

// VaultPathError names the vault folder and the path that refused a write.
type VaultPathError struct {
	Root string
	Rel  string
	// KeyFile: the path is the vault's key file (ErrVaultKeyFile).
	KeyFile bool
}

func (e *VaultPathError) Error() string {
	if e.KeyFile {
		return ErrVaultKeyFile.Error() + ": " + e.Rel
	}
	return ErrVaultPath.Error() + ": " + e.Rel
}

func (e *VaultPathError) Unwrap() error {
	if e.KeyFile {
		return ErrVaultKeyFile
	}
	return ErrVaultPath
}

// cleanRel is a target's path in the one spelling the vault rule compares:
// no leading or trailing slash, "" for the storage's root.
func cleanRel(rel string) string {
	rel = strings.Trim(path.Clean("/"+strings.Trim(rel, "/")), "/")
	if rel == "." {
		return ""
	}
	return rel
}

// vaultRule refuses t when it names something strictly inside a vault folder
// and is not the vault API's (ForVault). The vault folder itself is not
// inside it. Its key file is refused as a change of the key file
// (ErrVaultKeyFile) unless the target is only named or claims
// RewritesKeyFile.
func vaultRule(v Vaults, t Target) error {
	if t.vault {
		return nil
	}
	rel := cleanRel(t.Rel)
	root, ok := v.VaultRoot(rel)
	if !ok {
		return nil
	}
	root = cleanRel(root)
	if rel == root {
		return nil
	}
	if rel == strings.TrimPrefix(root+"/"+syspath.E2EKeyFile, "/") {
		if t.named || t.keyFile {
			return nil
		}
		return &VaultPathError{Root: root, Rel: rel, KeyFile: true}
	}
	return &VaultPathError{Root: root, Rel: rel}
}

// Check reports whether a write that names targets may happen: nil, a
// *syspath.ReservedError, a *VaultPathError or a *LockedError. Reserved names
// are judged first and without a database: a name that can never be written
// is refused the same way whether or not anything is locked. The vault rule
// comes next, when locks can answer it (Vaults), then the app locks.
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
		if syspath.RefusedBy(t.Verb, t.Rel, t.Person) {
			return &syspath.ReservedError{Rel: t.Rel}
		}
	}
	if locks == nil {
		return nil
	}
	if v, ok := locks.(Vaults); ok {
		for _, t := range targets {
			if err := vaultRule(v, t); err != nil {
				return err
			}
		}
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
