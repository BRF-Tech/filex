package handlers

import (
	"context"
	"net/http"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
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

// aclAllowForPlugin is aclAllowID for an app-plugin job submit BY THE CALLER
// of this request: the lock cap is waived when the file is locked by pluginID
// itself — the app that froze the document is the one that must still write
// into it (the signature), so its own jobs are judged on the level the caller
// would have without the lock.
func aclAllowForPlugin(ctx context.Context, resolver *acl.Resolver, store db.Store, storageID int64, rel string, need acl.Level, pluginID int64) bool {
	return aclAllowForPluginAs(ctx, resolver, store, auth.UserFrom(ctx), storageID, rel, need, pluginID)
}

// aclAllowForPluginAs is the same question asked about a NAMED person rather
// than about whoever is holding this request.
//
// ⚠⚠ It exists because the two doors that queue an app-plugin job disagree
// about who the actor is. The authenticated submit runs as the person on the
// keyboard, so reading the context is right. A public page's submit
// (PublicAPI.enqueueAsCreator) is pressed by an ANONYMOUS visitor and queued
// as the link's CREATOR — the worker reads the creator's storage with the
// creator's grants — so asking the context there would have judged the job by
// the rights of a person who has none, i.e. refused everything, and copying
// this body with the creator substituted would have been the second
// implementation this repo forbids. One body, two entry points: whatever the
// lock waiver or the level arithmetic grows into, both doors grow with it.
//
// ⚠ user == nil is NOT "unknown, allow": acl.Set.Effective answers LevelNone
// for a nil user, so a link whose creator was deleted stops queueing jobs.
// That is the intended answer — the account whose ACL the job would run under
// no longer exists.
func aclAllowForPluginAs(ctx context.Context, resolver *acl.Resolver, store db.Store, user *model.User, storageID int64, rel string, need acl.Level, pluginID int64) bool {
	if resolver == nil {
		return true
	}
	st, err := store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return false
	}
	set, err := resolver.LoadSet(ctx, user, st)
	if err != nil || set == nil {
		return false
	}
	if set.Effective(rel) >= need {
		return true
	}
	if l := set.Lock(rel); l != nil && l.PluginID == pluginID {
		return set.EffectiveIgnoringLocks(rel) >= need
	}
	return false
}

// aclLockWithin returns the app-plugin lock that forbids moving, renaming or
// deleting rel (a lock on rel itself or on anything under it), or nil. It
// binds administrators too — a document under signature stays put for
// everyone — so it is checked even when the resolver would grant owner.
func aclLockWithin(ctx context.Context, resolver *acl.Resolver, store db.Store, storageID int64, rel string) *model.AppPluginLock {
	if resolver == nil {
		return nil
	}
	st, err := store.GetStorage(ctx, storageID)
	if err != nil || st == nil {
		return nil
	}
	set, err := resolver.LoadSet(ctx, auth.UserFrom(ctx), st)
	if err != nil || set == nil {
		return nil
	}
	return set.LockWithin(rel)
}

// lockedAnswer writes the 423 a locked source gets: who holds the lock and
// why, so the client can say more than "forbidden".
func lockedAnswer(w http.ResponseWriter, l *model.AppPluginLock, rel string) {
	body := map[string]any{"error": "locked", "message": "locked by app " + l.PluginName + ": " + rel, "plugin": l.PluginName, "path": l.Rel}
	// The refusal is read by a person too (the toast says who is holding the
	// file), so it carries the app's label beside its name — see lockView.
	if label := lockAppLabel(l.PluginName); len(label) > 0 {
		body["plugin_label"] = label
	}
	plain, text := lockReasonOf(l)
	if plain != "" {
		body["reason"] = plain
	}
	if len(text) > 0 {
		body["reason_text"] = text
	}
	if l.Until != nil {
		body["until"] = l.Until
	}
	writeJSON(w, http.StatusLocked, body)
}
