package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protoperm"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// downloadLift is protoperm.TreeDownloadLifted asked as the caller on ctx, for
// the explorer's and the agent API's doors: src on srcID (a file or a folder)
// landing at dst on dstID - a copy, a move, a rename, an extraction, an app's
// result. ok is false when that would make bytes downloadable where they are
// not, and v is then the files.download verdict at the source path that keeps
// them, for the door to write (v.WritePerm) or word.
//
// ⚠ One question for every door that puts a file's bytes at another path. A
// role's part that refuses files.download on some paths or names is about
// the bytes, not about one spelling of their path: a door that carries them
// somewhere the rule does not cover, and does not ask this, undoes the rule.
//
// An unwired resolver (a handler built by hand in a test) lifts nothing. A
// storage, set or folder walk that fails is an error, never a yes.
func downloadLift(ctx context.Context, resolver *acl.Resolver, store db.Store, drvFor func(int64) (storage.Driver, error), srcID int64, src string, dstID int64, dst string) (v permVerdict, ok bool, err error) {
	ok = true
	if resolver == nil {
		return v, ok, nil
	}
	srcSet, err := callerSetOn(ctx, resolver, store, srcID)
	if err != nil {
		return v, false, err
	}
	dstSet := srcSet
	if dstID != srcID {
		if dstSet, err = callerSetOn(ctx, resolver, store, dstID); err != nil {
			return v, false, err
		}
	}
	var drv storage.Driver
	if drvFor != nil {
		if drv, err = drvFor(srcID); err != nil {
			return v, false, err
		}
	}
	at, lifted, err := protoperm.TreeDownloadLifted(ctx, drv, srcSet, acl.CleanRel(src), dstSet, acl.CleanRel(dst))
	if err != nil {
		return v, false, err
	}
	if !lifted {
		return v, true, nil
	}
	return verdictOf(srcSet, at, perm.FilesDownload), false, nil
}

// errNoCallerSet is a storage or ACL set downloadLift could not load.
var errNoCallerSet = errors.New("access check unavailable")

// callerSetOn loads the caller's ACL set on one storage.
func callerSetOn(ctx context.Context, resolver *acl.Resolver, store db.Store, storageID int64) (*acl.Set, error) {
	st, err := store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return nil, errNoCallerSet
	}
	set, err := resolver.LoadSet(ctx, auth.UserFrom(ctx), st)
	if err != nil || set == nil {
		return nil, errNoCallerSet
	}
	return set, nil
}

// refuseDownloadLift is downloadLift for an explorer door, with the answer
// written: the files.download refusal at the path that keeps it (the
// permission, the rule that refused it, in the reader's language), or a 500
// when the question could not be asked. It reports true when it refused.
func refuseDownloadLift(w http.ResponseWriter, r *http.Request, resolver *acl.Resolver, store db.Store, drvFor func(int64) (storage.Driver, error), srcID int64, src string, dstID int64, dst string) bool {
	v, ok, err := downloadLift(r.Context(), resolver, store, drvFor, srcID, src, dstID, dst)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", nil, "detail", err.Error())
		return true
	}
	if ok {
		return false
	}
	if !v.WritePerm(w, r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + src})
	}
	return true
}
