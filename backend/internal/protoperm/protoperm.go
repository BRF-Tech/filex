// Package protoperm is the one place the protocol front ends (ftpsrv, sftpsrv,
// nfssrv, dav, s3api) ask per-user permissions (package perm) about a path.
//
// Each protocol owns the shape of its refusal — NoSuchFile, 550, EACCES,
// AccessDenied — and none of them owns the question. Written once per
// protocol, the question drifted before it was ever asked: one front end
// checked the level and forgot the permission, another the lock. Here it is
// the level, the lock and the permission, in one call.
package protoperm

import (
	"context"
	"errors"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2epolicy"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writegate"
)

// CanRead is the door for a file's CONTENT over a protocol: viewer on the
// path itself (never inherited from being on the way to a grant — that is
// acl.Set.CanSee's traversal), AND files.download, because reading a file's
// bytes over a protocol IS a download: there is no preview to leave open.
func CanRead(set *acl.Set, rel string) bool {
	return set != nil && set.Effective(rel) >= acl.LevelViewer && set.AllowsAt(rel, perm.FilesDownload)
}

// CanDo is the door for every change: the storage exists and is writable, no
// app lock refuses the path (read through resolver, so a freeze taken a
// second ago counts), and the caller may take action p there — the level it
// needs (≥editor for every mutation) AND the per-user permission, blocked
// file types included.
func CanDo(ctx context.Context, set *acl.Set, st *model.Storage, resolver *acl.Resolver, rel string, p perm.Perm) bool {
	if st == nil || st.ReadOnly || writegate.RefusesMounted(resolver.Locks(ctx, st.ID), rel) {
		return false
	}
	return set != nil && set.Can(rel, p)
}

// VerbFor is the API-token verb (auth.VerbRead / VerbWrite / VerbDelete) the
// action p needs when the credential behind a protocol session is an API
// token used as the password: reading content is `read`, removing to the
// trash or for good is `delete`, every other change is `write` — the same
// split /dav, /api/files and /api/ai make. A password or a public key carries
// every verb (protocolauth.Principal.HasScope), so it changes nothing there.
//
// ⚠ One mapping, asked by every protocol front end inside its canRead/canDo:
// a front end that checks the permission and forgets the verb hands a
// read-only token the right to delete (the gap #112 closed).
func VerbFor(p perm.Perm) string {
	switch p {
	case perm.FilesDownload:
		return auth.VerbRead
	case perm.FilesDelete, perm.FilesPurge:
		return auth.VerbDelete
	default:
		return auth.VerbWrite
	}
}

// WriteNeed is what writing rel's bytes is: files.modify when something is
// there to replace, files.create when not. A driver that cannot be reached
// answers files.create — the stricter of the two is not implied, but the
// write will fail on the driver anyway.
func WriteNeed(ctx context.Context, drv storage.Driver, err error, rel string) perm.Perm {
	if err == nil && drv != nil {
		if _, serr := drv.Stat(ctx, rel); serr == nil {
			return perm.FilesModify
		}
	}
	return perm.FilesCreate
}

// EncryptionAnswer is what EncryptionAllowed found out.
type EncryptionAnswer int

const (
	// EncryptionOK: the write goes ahead — its name encrypts nothing, a file
	// is there to rewrite, or the rule said yes.
	EncryptionOK EncryptionAnswer = iota
	// EncryptionRefused: the rule said no. The door answers its own
	// "permission denied".
	EncryptionRefused
	// EncryptionUndecided: the rule could not be decided — a lookup behind it
	// failed, and that was logged. The door answers its own server failure:
	// a broken store must not read as the policy doing its job, to the client
	// or to the operator.
	EncryptionUndecided
)

// ErrEncryptionUndecided is what a door gives its library for
// EncryptionUndecided where the library turns errors into codes. pkg/sftp
// answers it as SSH_FX_FAILURE, and go-nfs a RENAME's as NFS3ERR_IO.
// ftpserverlib answers any error opening an upload or renaming as 550, and
// go-nfs any error of a CREATE as NFS3ERR_ACCES, so there its words — and the
// log line — are what tell it from a refusal. The HTTP doors answer with the
// same words (e2epolicy.ErrUndecided).
var ErrEncryptionUndecided = e2epolicy.ErrUndecided

// EncryptionAllowed is who may encrypt (internal/e2epolicy) at a protocol
// door about to write a FILE at rel on drv, st's driver: an upload's open, a
// PUT, a CREATE, the destination of a copy, and of a rename onto the name
// that is not free (RenameEncryptionAllowed: a folder, a `.fxe` that stays a
// `.fxe` and a key file that stays its own folder's are). Creating an
// encrypted folder's key file (`.filex-e2e.json`) or a `.fxe` needs the
// service provider's ceiling, the tenant's policy and files.encrypt there —
// and under the approval policy an approval, which this spends. A storage it
// cannot see is refused.
//
// Whether the write creates the file is decided here, for every door: it does
// unless a FILE is at rel (e2epolicy.FileThere). A folder with the name is not
// the file — a copy onto it replaces the folder with one, an object store
// keeps the file beside the folder (a prefix and an object of one name) — and
// a Stat that fails for any reason, or no driver to ask, counts as a create
// too: "could not look" is not "it is there". Only a file that is there is a
// rewrite (a password change, a key-slot update), and free. A door's own
// files.create/modify question (WriteNeed) reads a folder as "something to
// replace", so it is not this one.
//
// Every other name is EncryptionOK without a lookup, and so is an unwired
// service (a server with no store to build the rule from).
//
// ⚠ Ask it once per write. Under the approval policy the question spends the
// approval, and a second one for the same write would find it spent.
func EncryptionAllowed(ctx context.Context, svc *e2epolicy.Service, drv storage.Driver, st *model.Storage, rel string) EncryptionAnswer {
	if svc == nil || !e2epolicy.IsEncryptionName(rel) || e2epolicy.FileThere(ctx, drv, rel) {
		return EncryptionOK
	}
	// An undecided rule is logged there, with who and where but not the
	// path (e2epolicy.DoorError, as the HTTP doors log it).
	u := auth.UserFrom(ctx)
	switch err := e2epolicy.DoorError(u, st, svc.CheckCreate(ctx, u, st, rel)); {
	case err == nil:
		return EncryptionOK
	case errors.Is(err, e2epolicy.ErrRefused):
		return EncryptionRefused
	}
	return EncryptionUndecided
}

// RenameEncryptionAllowed is EncryptionAllowed for a rename or a move of src
// to dst on drv, st's driver — a protocol renames within one storage:
// WebDAV's MOVE, SFTP's rename and posix-rename, FTPS's RNFR/RNTO, NFS's
// RENAME.
//
// Landing on a key file's or a `.fxe`'s name encrypts as surely as creating
// one (operator decisions 2026-09-30 and 2026-10-03): upload rapor.bin, rename
// it rapor.bin.fxe. So such a rename is asked exactly as a write of the file
// at dst: a file there is replaced — a rewrite, free — and nothing or a folder
// there is a create. The rename is EncryptionOK with no question when it
// carries what is encrypted already (e2epolicy.RelocationEncrypts, the rule
// the HTTP doors ask too): a folder under any name, a `.fxe` that stays a
// `.fxe`, a key file that stays its own folder's.
func RenameEncryptionAllowed(ctx context.Context, svc *e2epolicy.Service, drv storage.Driver, st *model.Storage, src, dst string) EncryptionAnswer {
	if svc == nil || !e2epolicy.RelocationEncrypts(ctx, drv, src, dst, true) {
		return EncryptionOK
	}
	return EncryptionAllowed(ctx, svc, drv, st, dst)
}

// CopyEncryptionAllowed is EncryptionAllowed for a COPY of src (on
// srcStorageID, srcDrv its driver) to dst on dstDrv, st's driver: a WebDAV
// COPY, a protocol's only copy (operator decision 2026-10-03: a copy of an
// encrypted folder or of a `.fxe` is a new encryption where it lands; a
// rename and a move are not, RenameEncryptionAllowed).
//
// A file is asked as a write of the file at dst (EncryptionAllowed): onto a
// file there it is a rewrite, free (⚠ the honest limit: a `.fxe` or a key file
// that exists can be overwritten with any bytes); onto nothing or a folder it
// creates the name. A folder is asked by what it holds
// (e2epolicy.Service.CheckCopy): one that holds a key file or a `.fxe`
// anywhere below it is a new encrypted folder at dst. Ask it once per COPY.
func CopyEncryptionAllowed(ctx context.Context, svc *e2epolicy.Service, srcDrv storage.Driver, srcStorageID int64, src string, dstDrv storage.Driver, st *model.Storage, dst string) EncryptionAnswer {
	if svc == nil {
		return EncryptionOK
	}
	u := auth.UserFrom(ctx)
	isDir, err := svc.SourceIsFolder(ctx, srcStorageID, srcDrv, src)
	if err != nil {
		_ = e2epolicy.DoorError(u, st, err)
		return EncryptionUndecided
	}
	if !isDir {
		return EncryptionAllowed(ctx, svc, dstDrv, st, dst)
	}
	switch err := e2epolicy.DoorError(u, st, svc.CheckCopy(ctx, u, st, dst, srcStorageID, src, true)); {
	case err == nil:
		return EncryptionOK
	case errors.Is(err, e2epolicy.ErrRefused):
		return EncryptionRefused
	}
	return EncryptionUndecided
}

// EncryptionPolicy is the rule a protocol server asks (EncryptionAllowed): the
// router's own, svc, when it was handed one — otherwise one built over the
// server's store, its ACL resolver and its tenancy, as BuildRouter builds
// it, the e2e_request.use row of an approval it spends included. So a server
// whose wiring forgot the rule still enforces it, under every policy, and
// still records what it spends: what it loses is only the shared instance.
// It is nil only with no store to build from. (It also loses the storage's
// own answer to "is this folder new": the catalogue alone says.)
func EncryptionPolicy(svc *e2epolicy.Service, store db.Store, aclR *acl.Resolver, multiTenant bool) *e2epolicy.Service {
	if svc != nil || store == nil {
		return svc
	}
	return e2epolicy.New(e2epolicy.Options{
		Store: store, ACL: aclR, MultiTenant: multiTenant,
		OnUse: e2epolicy.UseRecorder(store),
	})
}
