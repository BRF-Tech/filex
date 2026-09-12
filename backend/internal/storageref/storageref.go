// Package storageref resolves the first path segment of a file-protocol
// address to a storage.
//
// Every protocol addresses a storage the same way — it is the first segment of
// the path (`/dav/<ref>/…`, `/<ref>/…` over SFTP and NFS, the bucket over the
// S3-compatible API) — and for the life of the project that segment was the
// storage's NAME. A name is editable, which is the point of a name, so
// renaming a storage silently re-addressed it and every mount, bookmark and
// script written against the old one answered 404 (issue #21).
//
// Every storage now also carries a uid, assigned once and never changed. Both
// resolve, everywhere, through this one function: a person types the name, an
// automated mount is given the uid and survives every rename. Putting the rule
// here rather than in each server is deliberate — five copies of "which
// identifier is this" would drift, and a protocol where only one of them
// worked would be a product that behaves differently depending on how you
// reach it.
package storageref

import (
	"context"
	"regexp"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Store is the slice of the catalogue this needs.
type Store interface {
	GetStorageByName(ctx context.Context, name string) (*model.Storage, error)
	GetStorageByUID(ctx context.Context, uid string) (*model.Storage, error)
}

// uidShape matches a v4-style uuid as we mint them. Deliberately narrow: it
// decides only whether a uid lookup is worth a query, and a ref that is not
// this shape is a name and nothing else.
var uidShape = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsUID reports whether ref is shaped like a storage uid.
func IsUID(ref string) bool { return uidShape.MatchString(ref) }

// Resolve returns the storage a path segment addresses, or the lookup's own
// error when there is none.
//
// ⚠ A uid-shaped ref that matches no storage falls back to the name lookup,
// rather than stopping at "no such uid". Nothing forbids naming a storage
// something that looks like a uuid, and a name that worked before this
// function existed must go on working after it.
func Resolve(ctx context.Context, store Store, ref string) (*model.Storage, error) {
	if ref == "" {
		return store.GetStorageByName(ctx, ref)
	}
	if IsUID(ref) {
		if st, err := store.GetStorageByUID(ctx, ref); err == nil && st != nil {
			return st, nil
		}
	}
	return store.GetStorageByName(ctx, ref)
}
