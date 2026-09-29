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

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
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
