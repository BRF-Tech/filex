package handlers

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// linkCreatorAllows reports whether a public link may still answer: the
// person who made it must STILL be allowed to make it — share.links for a
// download link, share.upload_links for a file request (drop) — at the level
// that permission needs on the item (≥editor, acl.NeedLevel). Burak,
// 2026-09-28: when someone loses the right to share, the links they already
// handed out close ("linkler kapanır").
//
// Nothing is deleted: the answer is computed on every visit, so a link whose
// creator gets the permission back answers again, and the owner still sees and
// can revoke it in My shares.
//
// Links it leaves alone:
//   - an app's own public page (IsApp): the app action that opened it was
//     allowed by the app's own permission, and a signer's page must not close
//     because the requester's sharing rights changed;
//   - a link with no recorded creator: made before shares recorded one, or
//     whose creator was deleted before 0.49.0 (the store used to keep the row
//     and clear created_by). There is nobody to ask, the two cannot be told
//     apart, and closing every such link on upgrade would break links nobody
//     chose to break. Deleting an account NOW deletes the links it opened
//     with it (db.Store.DeleteUser), so no new link joins this group;
//   - an unwired resolver (a handler built by hand in a test).
//
// A missing item or storage is left to each door's own "not found" answer.
//
// ⚠ One function for every door that serves a link — /s/ (download, metadata,
// folder browse), /d/ (drop page and upload) and /api/public/* — so a door
// that forgets it is the only door that still serves a closed link.
func linkCreatorAllows(ctx context.Context, resolver *acl.Resolver, store db.Store, sh *model.Share) bool {
	if resolver == nil || store == nil || sh == nil || sh.IsApp() || sh.CreatedBy == nil || *sh.CreatedBy <= 0 {
		return true
	}
	creator, err := store.GetUser(ctx, *sh.CreatedBy)
	if err != nil || creator == nil {
		return true
	}
	node, err := store.GetNode(ctx, sh.NodeID)
	if err != nil || node == nil {
		return true
	}
	st, err := store.GetStorage(ctx, node.StorageID)
	if err != nil || st == nil {
		return true
	}
	set, err := resolver.LoadSet(ctx, creator, st)
	if err != nil || set == nil {
		return false
	}
	need := perm.ShareLinks
	if sh.IsDrop() {
		need = perm.ShareUploadLinks
	}
	// ⚠ The level is read WITHOUT the app-plugin lock cap. A lock limits what
	// may be done TO the file — a signed document is locked for good — not who
	// may hand it out: Set.Can caps a locked file at viewer, share.links needs
	// editor, and every link to a locked file closed, the signing app's own
	// delivery link included (v0.49.0 release run, e2e 113). A creator who
	// really lost the level or the permission still closes the link.
	if lv := acl.NeedLevel(need); lv > acl.LevelNone && set.EffectiveIgnoringLocks(node.Path) < lv {
		return false
	}
	return set.AllowsAt(node.Path, need)
}

// linkCreatorBlocksName reports whether the link creator's role blocks a file
// of this name (blocked_extensions) in the drop folder: a file request lands
// in the creator's storage as the creator's file, so the creator's limits are
// the ones that apply (the per-file size limit is already held against the
// creator through quotastore.WithOwner). Returns the blocked extension, or "".
func linkCreatorBlocksName(ctx context.Context, resolver *acl.Resolver, store db.Store, sh *model.Share, st *model.Storage, rel string) string {
	if resolver == nil || store == nil || sh == nil || sh.CreatedBy == nil || *sh.CreatedBy <= 0 || st == nil {
		return ""
	}
	creator, err := store.GetUser(ctx, *sh.CreatedBy)
	if err != nil || creator == nil {
		return ""
	}
	set, err := resolver.LoadSet(ctx, creator, st)
	if err != nil || set == nil {
		return ""
	}
	return set.BlockedExtension(rel)
}
