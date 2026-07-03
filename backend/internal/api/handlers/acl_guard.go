package handlers

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
)

// aclAllowName reports whether the request's user has at least `need` on rel
// within the storage named storageName. A nil resolver (ACL unwired, e.g.
// tests) allows; any resolution error denies. Shared by the file-mutation /
// read handlers that resolve a storage by adapter name.
func aclAllowName(ctx context.Context, resolver *acl.Resolver, store db.Store, storageName, rel string, need acl.Level) bool {
	if resolver == nil {
		return true
	}
	st, err := store.GetStorageByName(ctx, storageName)
	if err != nil || st == nil {
		return false
	}
	set, err := resolver.LoadSet(ctx, auth.UserFrom(ctx), st)
	if err != nil || set == nil {
		return false
	}
	return set.Effective(rel) >= need
}

// aclAllowID is aclAllowName keyed by storage id.
func aclAllowID(ctx context.Context, resolver *acl.Resolver, store db.Store, storageID int64, rel string, need acl.Level) bool {
	if resolver == nil {
		return true
	}
	st, err := store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return false
	}
	set, err := resolver.LoadSet(ctx, auth.UserFrom(ctx), st)
	if err != nil || set == nil {
		return false
	}
	return set.Effective(rel) >= need
}
