// Package acl implements per-user / per-file&folder access control for filex.
//
// It is the identity-driven complement to package confine. confine is the
// token HARD-CEILING (a token's `root:` scope + X-Filex-Root header, a single
// subtree) and runs as a chi middleware that rewrites request paths. acl runs
// AFTER, inside the handlers, keyed off the authenticated *user* (not the
// token) so plain cookie/session callers are filtered too — closing the gap
// where a session user previously saw every storage and every path.
//
// The two compose: confine narrows to at most one subtree; acl narrows to the
// (possibly many, possibly none) subtrees the user was granted, and assigns a
// capability level (viewer/editor/owner) that is finally capped by the user's
// account-role ceiling.
//
// Backwards compatible: on a storage with RBACEnabled=false, acl returns the
// account-role base level (user→editor, viewer→viewer, admin→owner) for every
// path and treats the storage as fully visible — reproducing pre-00012
// behavior exactly. Grants are only consulted on RBAC-on storages.
package acl

import (
	"context"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Level is an ordered capability: None < Viewer < Editor < Owner.
type Level int

const (
	LevelNone Level = iota
	LevelViewer
	LevelEditor
	LevelOwner
)

// ParseLevel maps a grant level string to a Level (unknown → LevelNone).
func ParseLevel(s string) Level {
	switch s {
	case model.GrantViewer:
		return LevelViewer
	case model.GrantEditor:
		return LevelEditor
	case model.GrantOwner:
		return LevelOwner
	default:
		return LevelNone
	}
}

// String returns the level string. LevelNone → "none" (NOT "") so the frontend
// can distinguish "no access" (none) from "ACL not enforced" (field absent);
// an empty string would be ambiguous with the unwired/dev case.
func (l Level) String() string {
	switch l {
	case LevelViewer:
		return model.GrantViewer
	case LevelEditor:
		return model.GrantEditor
	case LevelOwner:
		return model.GrantOwner
	default:
		return "none"
	}
}

// RoleCeiling is the maximum effective level an account role may ever reach.
// A viewer account is capped at LevelViewer (read-only everywhere, even on
// RBAC-off storages and even if handed an editor/owner item grant).
func RoleCeiling(role string) Level {
	switch role {
	case model.RoleAdmin, model.RoleUser:
		return LevelOwner
	case model.RoleViewer:
		return LevelViewer
	default:
		return LevelNone
	}
}

// CleanRel normalizes a storage-relative path to confine-form: no leading or
// trailing slash, traversal collapsed, "" == root. Mirrors confine.split's rel
// handling so acl and confine agree on path identity.
func CleanRel(rel string) string {
	rel = strings.Trim(path.Clean("/"+rel), "/")
	if rel == "." {
		rel = ""
	}
	return rel
}

// prefixContains reports whether prefix (a grant path) covers rel. "" (storage
// root) covers everything; equal paths match; a folder prefix covers its
// descendants. Both args must already be clean. This is the adapter-free twin
// of confine.Root.contains.
func prefixContains(prefix, rel string) bool {
	if prefix == "" {
		return true
	}
	return rel == prefix || strings.HasPrefix(rel, prefix+"/")
}

// Resolver loads grants from the store and builds request-scoped ACL Sets.
type Resolver struct {
	store db.Store
}

// New returns a Resolver backed by store.
func New(store db.Store) *Resolver { return &Resolver{store: store} }

// Set is the resolved ACL for one (user, storage) pair for the duration of a
// request. Grants are batch-loaded once by LoadSet; Effective / CanSee /
// StorageVisible are then pure in-memory checks with no further DB access.
type Set struct {
	user    *model.User
	storage *model.Storage
	grants  []*model.FileGrant
	ceiling Level
}

// LoadSet builds the ACL set for user u on storage s. For admins and RBAC-off
// storages it skips the grant query entirely (they never consult grants).
func (r *Resolver) LoadSet(ctx context.Context, u *model.User, s *model.Storage) (*Set, error) {
	set := &Set{user: u, storage: s}
	if u != nil {
		set.ceiling = RoleCeiling(u.Role)
	}
	if u == nil || u.IsAdmin() || s == nil || !s.RBACEnabled {
		return set, nil
	}
	grants, err := r.store.ListFileGrantsByStorageUser(ctx, s.ID, u.ID)
	if err != nil {
		return nil, err
	}
	set.grants = grants
	return set, nil
}

// Effective returns the caller's effective capability on a storage-relative
// path: the highest grant covering it (direct or inherited from an ancestor
// folder), capped by the account-role ceiling. Admins are always Owner;
// RBAC-off storages return the account-role base.
func (s *Set) Effective(rel string) Level {
	if s == nil || s.user == nil {
		return LevelNone
	}
	if s.user.IsAdmin() {
		return LevelOwner
	}
	if s.storage == nil || !s.storage.RBACEnabled {
		return capLevel(roleBase(s.user.Role), s.ceiling)
	}
	rel = CleanRel(rel)
	best := LevelNone
	for _, g := range s.grants {
		if prefixContains(CleanRel(g.PathPrefix), rel) {
			if lv := ParseLevel(g.Level); lv > best {
				best = lv
			}
		}
	}
	return capLevel(best, s.ceiling)
}

// CanSee reports whether rel should appear in a listing for this caller:
// either the caller has ≥viewer on it, OR rel is a (strict or equal) ancestor
// of a granted path — so ancestor folders render as traversal nodes that let
// the user drill down to the subtree they were actually granted.
func (s *Set) CanSee(rel string) bool {
	if s == nil || s.user == nil {
		return false
	}
	if s.user.IsAdmin() {
		return true
	}
	rel = CleanRel(rel)
	if s.storage == nil || !s.storage.RBACEnabled {
		return s.Effective(rel) >= LevelViewer
	}
	if s.Effective(rel) >= LevelViewer {
		return true
	}
	for _, g := range s.grants {
		if prefixContains(rel, CleanRel(g.PathPrefix)) {
			return true
		}
	}
	return false
}

// StorageVisible reports whether the storage should appear in the caller's
// storage/drive list at all. Admins and RBAC-off storages are always visible;
// on an RBAC-on storage a non-admin needs at least one grant.
func (s *Set) StorageVisible() bool {
	if s == nil || s.user == nil {
		return false
	}
	if s.user.IsAdmin() {
		return true
	}
	if s.storage == nil || !s.storage.RBACEnabled {
		return true
	}
	return len(s.grants) > 0
}

// Ceiling is the account-role ceiling for this set's user.
func (s *Set) Ceiling() Level {
	if s == nil {
		return LevelNone
	}
	return s.ceiling
}

// Grants returns the raw grants loaded for this set (may be nil for admins /
// RBAC-off storages). The permissions handler uses this for the panel.
func (s *Set) Grants() []*model.FileGrant {
	if s == nil {
		return nil
	}
	return s.grants
}

// roleBase is the capability a role has on an RBAC-off storage before ceiling.
func roleBase(role string) Level {
	switch role {
	case model.RoleAdmin:
		return LevelOwner
	case model.RoleUser:
		return LevelEditor
	case model.RoleViewer:
		return LevelViewer
	default:
		return LevelNone
	}
}

func capLevel(l, ceil Level) Level {
	if l > ceil {
		return ceil
	}
	return l
}
