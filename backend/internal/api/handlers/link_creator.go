package handlers

import (
	"context"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protoperm"
)

// linkCreatorAllows reports whether a public link may still answer: the
// person who made it must STILL be allowed to make it — share.links for a
// download link, share.upload_links for a file request (drop) — at the level
// that permission needs on the item (≥editor, acl.NeedLevel), and a download
// link's creator must still be allowed to download the item (files.download
// there, protoperm.CanRead). The maintainer,
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
//     because the requester's sharing rights changed. The copies of files it
//     hands over answer appLinkCopiesServed;
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
	creator := ruledLinkCreator(ctx, resolver, store, sh)
	if creator == nil {
		return true
	}
	node, set, ask := linkCreatorSet(ctx, resolver, store, creator, sh.NodeID)
	if !ask {
		return true
	}
	if set == nil {
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
	if !set.AllowsAt(node.Path, need) {
		return false
	}
	// A download link hands the item's bytes out, so it answers only while
	// its creator may still download it there (files.download, the rule
	// publicLinkRefusal asked at the mint). A locked file stays served: the
	// lock caps the level at viewer, which a download needs no more than.
	return sh.IsDrop() || protoperm.CanRead(set, node.Path)
}

// appLinkCopiesServed reports whether an app's public page may still hand
// over its copies (`pub:N`, the files the app exposed when it opened the
// page). They are made from the page's document, so they answer what a
// download link of the document answers: its creator may still download it
// there (protoperm.CanRead, as linkCreatorAllows asks). When not, the copies
// are neither listed nor served; the page itself - the app's own content - is
// untouched, and a page whose app exposed no copy is asked nothing.
//
// Left alone, as linkCreatorAllows leaves them: a link with no recorded
// creator (an app's scheduled wake-up opened it), a creator that cannot be
// loaded (the doors' own creator rule, linkCreator, answers that), an
// unwired resolver; a missing document is the door's own "not found".
//
// The app is asked the same of every input when it opens the page
// (wasmplugin hfShareCreate, actorMayDownload); this is the question again at
// every visit.
func appLinkCopiesServed(ctx context.Context, resolver *acl.Resolver, store db.Store, sh *model.Share) bool {
	if resolver == nil || store == nil || sh == nil || !sh.IsApp() || sh.CreatedBy == nil || *sh.CreatedBy <= 0 {
		return true
	}
	creator, err := store.GetUser(ctx, *sh.CreatedBy)
	if err != nil || creator == nil {
		return true
	}
	node, set, ask := linkCreatorSet(ctx, resolver, store, creator, sh.NodeID)
	return !ask || (set != nil && protoperm.CanRead(set, node.Path))
}

// linkCreatorSet loads what the link rules ask a creator about: the node a
// link points at and the creator's set on its storage. ask is false when
// there is nothing to ask - no node or storage, which each door answers as its
// own "not found"; set is nil, and the rules refuse, when the set could not be
// loaded.
func linkCreatorSet(ctx context.Context, resolver *acl.Resolver, store db.Store, creator *model.User, nodeID int64) (node *model.Node, set *acl.Set, ask bool) {
	node, err := store.GetNode(ctx, nodeID)
	if err != nil || node == nil {
		return nil, nil, false
	}
	st, err := store.GetStorage(ctx, node.StorageID)
	if err != nil || st == nil {
		return nil, nil, false
	}
	if set, err = resolver.LoadSet(ctx, creator, st); err != nil {
		set = nil
	}
	return node, set, true
}

// LinkMembers is the rule each file inside a FOLDER link answers before it is
// served - in the folder's ZIP (and the cached one the warmer builds), from
// /s/{token}/f/..., and in the folder's listing: memberRule, asked as the
// link's creator, so a file the creator may not download is not handed out
// by the link either. It returns nil - every member - for the links
// linkCreatorAllows leaves alone: an app's page, a link with no recorded
// creator (or one that cannot be loaded), an unwired resolver.
//
// ⚠ linkCreatorAllows asks about the shared folder; a role's part can still
// refuse files.download on one file or subfolder inside it. A door that
// serves a member of a folder link asks this, and writes no rule of its own.
//
// The answer is computed once per call (one set per storage); a door asks it
// per request, the warmer once per pass.
func LinkMembers(ctx context.Context, resolver *acl.Resolver, store db.Store, sh *model.Share, storageID int64) func(rel string) bool {
	creator := ruledLinkCreator(ctx, resolver, store, sh)
	if creator == nil {
		return nil
	}
	cctx := auth.WithUser(ctx, creator)
	rule := newMemberRule(resolver, store)
	return func(rel string) bool { return rule.allows(cctx, storageID, rel) }
}

// ruledLinkCreator is the account a link's rules (linkCreatorAllows,
// LinkMembers) answer for, or nil for a link they leave alone: an app's page,
// a link with no recorded creator or one that cannot be loaded, an unwired
// resolver. (linkCreator, public_api.go, is the other question about the
// same account: whether it may still sign in at all.)
func ruledLinkCreator(ctx context.Context, resolver *acl.Resolver, store db.Store, sh *model.Share) *model.User {
	if resolver == nil || store == nil || sh == nil || sh.IsApp() || sh.CreatedBy == nil || *sh.CreatedBy <= 0 {
		return nil
	}
	creator, err := store.GetUser(ctx, *sh.CreatedBy)
	if err != nil {
		return nil
	}
	return creator
}

// linkMemberServed reports whether keep (LinkMembers) lets rel out; a nil
// keep lets everything out.
func linkMemberServed(keep func(string) bool, rel string) bool {
	return keep == nil || keep(rel)
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
