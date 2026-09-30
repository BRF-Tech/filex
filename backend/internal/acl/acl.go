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
	"log/slog"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/syspath"
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
	// perms resolves the caller's per-user permissions (package perm). It is
	// built by New from the same store, so every Resolver — the router's,
	// the protocol servers', the app-plugin host's — enforces them; there is
	// no separate wiring step to forget. Nil only for a nil store (tests),
	// where Set.Can falls back to the level check alone.
	perms *perm.Loader
}

// New returns a Resolver backed by store.
func New(store db.Store) *Resolver {
	r := &Resolver{store: store}
	if store != nil {
		r.perms = perm.NewLoader(store)
	}
	return r
}

// Perms resolves u's per-user permissions — for the checks that are not about
// a path (comments, tokens, protocol access, the admin area). A nil Resolver
// or one without a store answers nil, which Allows reads as "not enforced".
func (r *Resolver) Perms(ctx context.Context, u *model.User) (*perm.Result, error) {
	if r == nil || r.perms == nil {
		return nil, nil
	}
	return r.perms.Load(ctx, u)
}

// PermsPreview is Perms for a state that is not stored yet (perm.Loader.
// Preview): what u would hold after change. Nil, like Perms, when unwired.
func (r *Resolver) PermsPreview(ctx context.Context, u *model.User, change func(*perm.Input)) (*perm.Result, error) {
	if r == nil || r.perms == nil {
		return nil, nil
	}
	return r.perms.Preview(ctx, u, change)
}

// Set is the resolved ACL for one (user, storage) pair for the duration of a
// request. Grants are batch-loaded once by LoadSet; Effective / CanSee /
// StorageVisible are then pure in-memory checks with no further DB access.
type Set struct {
	user    *model.User
	storage *model.Storage
	grants  []*model.FileGrant
	ceiling Level
	// perms is the caller's resolved per-user permissions; nil when the
	// Resolver has none wired (tests), in which case Can is the level check.
	perms *perm.Result
	// locks are the live app-plugin locks on this storage, keyed by clean
	// rel. A locked path is capped at LevelViewer for EVERY caller —
	// administrators and owners included — which is the one place the
	// admin short-circuit does not apply: a document under signature must
	// not change under the signers, whoever is asking.
	locks map[string]*model.AppPluginLock
}

// LoadSet builds the ACL set for user u on storage s. For admins and RBAC-off
// storages it skips the grant query entirely (they never consult grants).
func (r *Resolver) LoadSet(ctx context.Context, u *model.User, s *model.Storage) (*Set, error) {
	set := &Set{user: u, storage: s}
	if u != nil {
		set.ceiling = RoleCeiling(u.Role)
		if r.perms != nil {
			// A failure here denies (the caller treats a LoadSet error as
			// "no access"), like a failed grants query below.
			p, err := r.perms.Load(ctx, u)
			if err != nil {
				return nil, err
			}
			set.perms = p
		}
	}
	if s != nil {
		// Locks come before the admin/RBAC-off short-circuit on purpose:
		// they bind everyone. See loadLocks.
		set.locks = r.loadLocks(ctx, s.ID)
	}
	if u == nil || u.IsAdmin() || s == nil || !s.RBACEnabled {
		return set, nil
	}
	grants, err := UserGrants(ctx, r.store, s.ID, u)
	if err != nil {
		return nil, err
	}
	set.grants = grants
	return set, nil
}

// UserGrants is every grant that reaches u on one storage: their own, and
// those of the groups they are in (group_file_grants) that are in their
// tenant. A group's grant counts exactly as a grant to each member would —
// the highest covering level wins, and the account-role ceiling caps it.
func UserGrants(ctx context.Context, store db.Store, storageID int64, u *model.User) ([]*model.FileGrant, error) {
	grants, err := store.ListFileGrantsByStorageUser(ctx, storageID, u.ID)
	if err != nil {
		return nil, err
	}
	viaGroups, err := store.ListGroupFileGrantsByStorageUser(ctx, storageID, u.ID)
	if err != nil {
		return nil, err
	}
	if len(viaGroups) == 0 {
		return grants, nil
	}
	// A person moved to another tenant keeps their rows until someone
	// removes them; a group of the tenant they left must not reach them.
	inScope := map[int64]bool{}
	for _, g := range viaGroups {
		ok, seen := inScope[g.GroupID]
		if !seen {
			gr, gerr := store.GetGroup(ctx, g.GroupID)
			if gerr != nil {
				return nil, gerr
			}
			ok = perm.GroupInScope(gr, u.ProviderID)
			inScope[g.GroupID] = ok
		}
		if ok {
			grants = append(grants, g)
		}
	}
	return grants, nil
}

// Locks returns the live app-plugin locks of one storage as a Set that
// answers only Lock and LockWithin — for a write with no person behind it (a
// document server's save, an app's output) and for protocol sessions, whose
// per-session Set was loaded when the session began and does not see a lock
// taken after that (writegate.Check). A nil Resolver answers nil, which
// writegate reads as "nothing is locked".
func (r *Resolver) Locks(ctx context.Context, storageID int64) *Set {
	if r == nil {
		return nil
	}
	return &Set{locks: r.loadLocks(ctx, storageID)}
}

// loadLocks reads the live locks of one storage, keyed by clean rel.
//
// ⚠ A failure is logged and ignored rather than returned: this runs on the
// path of EVERY request, including an administrator's on an RBAC-off
// storage, which used to reach the DB not at all. A transient error must not
// turn into "nobody may touch any file". Locks protect a flow's integrity,
// not confidentiality, and for everyone but an administrator the grants query
// fails closed on the same outage anyway. The query is a primary-key range
// scan on a table that is empty on most installs.
func (r *Resolver) loadLocks(ctx context.Context, storageID int64) map[string]*model.AppPluginLock {
	locks, err := r.store.ListAppPluginLocks(ctx, storageID)
	if err != nil {
		slog.Warn("acl: app plugin locks unavailable, continuing without them",
			slog.Int64("storage_id", storageID), slog.String("err", err.Error()))
		return nil
	}
	var out map[string]*model.AppPluginLock
	now := time.Now()
	for _, l := range locks {
		if l.Live(now) {
			if out == nil {
				out = map[string]*model.AppPluginLock{}
			}
			out[CleanRel(l.Rel)] = l
		}
	}
	return out
}

// Effective returns the caller's effective capability on a storage-relative
// path: the highest grant covering it (direct or inherited from an ancestor
// folder), capped by the account-role ceiling. Admins are always Owner;
// RBAC-off storages return the account-role base.
func (s *Set) Effective(rel string) Level {
	if s == nil || s.user == nil {
		return LevelNone
	}
	lv := s.effective(rel)
	if lv > LevelViewer && s.Lock(rel) != nil {
		return LevelViewer
	}
	return lv
}

// EffectiveIgnoringLocks is Effective without the app-plugin lock cap — for
// the one caller that may write into a locked file: a job of the plugin
// holding the lock.
func (s *Set) EffectiveIgnoringLocks(rel string) Level {
	if s == nil || s.user == nil {
		return LevelNone
	}
	return s.effective(rel)
}

// effective is Effective before the lock cap.
func (s *Set) effective(rel string) Level {
	// A drafts area is ONE person's (issue #71, syspath.Drafts): its owner
	// edits their own drafts, and nobody else — an administrator included,
	// which is why this comes before the admin short-circuit — reaches it by
	// any grant, role or storage setting. A draft is somebody's unfinished
	// document, not a file of the storage; the area's root is nobody's.
	if syspath.InDrafts(rel) {
		if owner, ok := syspath.DraftOwner(rel); ok && owner == s.user.ID {
			return capLevel(LevelEditor, s.ceiling)
		}
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

// LockWithin returns a live app-plugin lock on rel itself or anywhere under
// it, or nil. Renaming, moving or deleting a folder takes every file inside
// along, so a mutation of the folder ENTRY is refused while any descendant
// is locked — while uploads into the folder, which touch no locked file,
// stay open (LockWithin is consulted by source-side mutations only; Effective
// is not).
func (s *Set) LockWithin(rel string) *model.AppPluginLock {
	if s == nil || len(s.locks) == 0 {
		return nil
	}
	rel = CleanRel(rel)
	if l := s.locks[rel]; l != nil {
		return l
	}
	for lrel, l := range s.locks {
		if rel == "" || strings.HasPrefix(lrel, rel+"/") {
			return l
		}
	}
	return nil
}

// Lock returns the live app-plugin lock on rel, or nil. Exact path only: a
// lock on a file does not freeze its folder (the folder can still gain
// siblings), and a lock on a folder freezes only the folder entry itself.
func (s *Set) Lock(rel string) *model.AppPluginLock {
	if s == nil || len(s.locks) == 0 {
		return nil
	}
	return s.locks[CleanRel(rel)]
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

// NeedLevel is the path capability a file-scoped permission also requires:
// holding files.delete lets a user delete only where acl already lets them
// write. Permissions that are not about a path answer LevelNone.
//
// The levels are the ones the handlers demanded before permissions existed:
// reads are viewer, every mutation is editor, sharing a public link is editor
// (handlers/share.go) and sharing with a person is owner (handlers/grants.go).
func NeedLevel(p perm.Perm) Level {
	switch p {
	case perm.FilesDownload, perm.FilesTag:
		return LevelViewer
	case perm.FilesCreate, perm.FilesModify, perm.FilesRename, perm.FilesMove, perm.FilesDelete, perm.FilesPurge,
		perm.ShareLinks, perm.ShareUploadLinks:
		return LevelEditor
	case perm.ShareUsers:
		return LevelOwner
	default:
		return LevelNone
	}
}

// Can reports whether the caller may take action p on rel: the path level p
// needs (NeedLevel, which includes the app-plugin lock cap) AND the per-user
// permission itself.
//
// A file type a permission rule blocks (blocked_extensions) is refused here
// for every action that would produce a file of that name — adding, changing
// or renaming/moving to it — so every door that asks Can refuses it without
// knowing rules exist. What such a file already there can still do is be
// read and deleted.
func (s *Set) Can(rel string, p perm.Perm) bool {
	if s == nil || s.user == nil {
		return false
	}
	if need := NeedLevel(p); need > LevelNone && s.Effective(rel) < need {
		return false
	}
	switch p {
	case perm.FilesCreate, perm.FilesModify, perm.FilesRename, perm.FilesMove:
		if s.BlockedExtension(rel) != "" {
			return false
		}
	}
	return s.AllowsAt(rel, p)
}

// AllowsAt is Allows for an action on rel: the same answer, except where a
// permission rule limited to storages or paths (perm.Result.CanAt) matches
// this storage and rel. Every check that has the path should ask this one.
func (s *Set) AllowsAt(rel string, p perm.Perm) bool {
	if s == nil || s.user == nil {
		return false
	}
	if s.perms == nil {
		return true
	}
	return s.perms.CanAt(s.storageID(), CleanRel(rel), p)
}

// WhyAt is where AllowsAt's answer came from.
func (s *Set) WhyAt(rel string, p perm.Perm) perm.Source {
	if s == nil || s.perms == nil {
		return perm.Source{}
	}
	return s.perms.WhyAt(s.storageID(), CleanRel(rel), p)
}

func (s *Set) storageID() int64 {
	if s.storage == nil {
		return 0
	}
	return s.storage.ID
}

// BlockedExtension returns the extension of rel's name that a permission
// rule binding this caller blocks ("exe", "tar.gz"), or "". Compared on the
// lowercase name, so "Setup.EXE" is caught by "exe".
func (s *Set) BlockedExtension(rel string) string {
	if s == nil || s.perms == nil || len(s.perms.Settings.BlockedExtensions) == 0 {
		return ""
	}
	name := strings.ToLower(path.Base(CleanRel(rel)))
	for _, ext := range s.perms.Settings.BlockedExtensions {
		if strings.HasSuffix(name, "."+ext) {
			return ext
		}
	}
	return ""
}

// Allows reports whether the caller holds per-user permission p, regardless
// of path. Nil permissions (no loader wired — tests only) allow.
func (s *Set) Allows(p perm.Perm) bool {
	if s == nil || s.user == nil {
		return false
	}
	if s.perms == nil {
		return true
	}
	return s.perms.Can(p)
}

// Perms returns the caller's resolved permissions (nil when unwired).
func (s *Set) Perms() *perm.Result {
	if s == nil {
		return nil
	}
	return s.perms
}
