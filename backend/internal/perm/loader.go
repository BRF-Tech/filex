package perm

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Loader reads what Resolve needs from the store.
//
// ⚠ Any read failure is returned, never papered over with a default: the
// caller denies on error (the same rule acl.Resolver follows). Falling back
// to the Standard preset when the defaults row is unreadable would hand an
// install that had narrowed its defaults the full set back.
type Loader struct {
	Store db.Store

	mu   sync.Mutex
	snap *installSnapshot
}

// installSnapshot is the install-wide half of an Input — the defaults and
// the rules — which is the same for every account and changes rarely.
type installSnapshot struct {
	defaults Set
	viewer   Set
	// apps are the built-in roles' app decisions (perm/app.go).
	apps  AppDecisions
	rules []*model.PermissionRule
	// members is user id → the one custom role they hold.
	members map[int64]int64
	// groups is user id → the groups they are in, in group id order — for
	// the role of an account with none of its own (EffectiveRole).
	groups map[int64][]*model.Group
	gen    uint64
	at     time.Time
}

// SnapshotTTL bounds how stale the cached defaults and rules may be. Writes
// in this process call Invalidate and are seen at once; the TTL is what
// carries a write made by ANOTHER filex process on the same database.
const SnapshotTTL = 3 * time.Second

// generation is bumped by Invalidate; a snapshot from an older generation is
// discarded even inside its TTL. Package-level because acl.New builds a
// Loader in several places and a write must reach all of them.
var generation atomic.Uint64

// Invalidate drops every Loader's cached defaults and rules. Call it after
// writing either.
func Invalidate() { generation.Add(1) }

// NewLoader returns a Loader backed by store.
func NewLoader(store db.Store) *Loader { return &Loader{Store: store} }

// memoKey carries a per-request memo of resolved results (see WithMemo).
type memoKey struct{}

type memo struct {
	mu  sync.Mutex
	res map[int64]*Result
}

// WithMemo returns ctx carrying a memo so a request that consults
// permissions several times (one acl.LoadSet per storage it touches)
// resolves each account once. Install it once per request, at the router.
func WithMemo(ctx context.Context) context.Context {
	return context.WithValue(ctx, memoKey{}, &memo{res: map[int64]*Result{}})
}

// Middleware installs WithMemo on every request.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithMemo(r.Context())))
	})
}

// Load resolves u's permissions. A nil user holds nothing. An administrator
// is answered without touching the store. Within a request carrying a memo
// (WithMemo), an account is resolved once.
func (l *Loader) Load(ctx context.Context, u *model.User) (*Result, error) {
	if u == nil {
		return Resolve(Input{}), nil
	}
	in := Input{UserID: u.ID, Role: u.Role, ProviderID: u.ProviderID}
	if u.Role == model.RoleAdmin {
		return Resolve(in), nil
	}

	m, _ := ctx.Value(memoKey{}).(*memo)
	if m != nil {
		m.mu.Lock()
		defer m.mu.Unlock()
		// Keyed by id AND checked against the role the memo saw: a request
		// that changes an account's role and then consults it must not be
		// answered for the old role.
		// The generation check makes a write earlier in the same request
		// (an admin editing this account's overrides) visible to it.
		if res, ok := m.res[u.ID]; ok && res.role == u.Role && res.gen == generation.Load() {
			return res, nil
		}
	}
	gen := generation.Load()

	snap, err := l.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	in.Defaults, in.Rules = snap.defaults, snap.rules
	roleID, via := EffectiveRole(snap.members[u.ID], snap.groups[u.ID], snap.rules, u.ProviderID)
	in.CustomRoleID = roleID
	if via != nil {
		in.ViaGroup = &GroupRef{ID: via.ID, Name: via.Name}
	}
	in.AppDecisions = snap.apps[u.Role]
	viewer := snap.viewer
	in.ViewerBase = &viewer
	if in.Overrides, err = l.Store.GetUserPermissionOverrides(ctx, u.ID); err != nil {
		return nil, fmt.Errorf("perm: overrides: %w", err)
	}
	res := Resolve(in)
	res.role, res.gen = u.Role, gen
	if m != nil {
		m.res[u.ID] = res
	}
	return res, nil
}

// Preview resolves u as Load would after change has been applied to the
// input: what the account WOULD hold with other exceptions, another custom
// role or another built-in role. u may be a new account that is not stored
// yet (id 0: no exceptions, no custom role). The per-request memo is neither
// read nor filled — the answer is about a state that does not exist.
//
// It is how a delegated administrator's change is judged by its RESULT
// rather than by the words of the request: lifting a Deny, ending a
// restrictive custom role or picking a built-in role all hand out
// permissions without the request naming a single one.
func (l *Loader) Preview(ctx context.Context, u *model.User, change func(*Input)) (*Result, error) {
	in := Input{}
	if u != nil {
		in.UserID, in.Role, in.ProviderID = u.ID, u.Role, u.ProviderID
	}
	snap, err := l.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	in.Defaults, in.Rules = snap.defaults, snap.rules
	viewer := snap.viewer
	in.ViewerBase = &viewer
	if u != nil && u.ID != 0 {
		in.CustomRoleID = snap.members[u.ID]
		in.Groups = append([]*model.Group(nil), snap.groups[u.ID]...)
		if in.Overrides, err = l.Store.GetUserPermissionOverrides(ctx, u.ID); err != nil {
			return nil, fmt.Errorf("perm: overrides: %w", err)
		}
	}
	if change != nil {
		change(&in)
	}
	// The custom role as Load picks it, AFTER the change: the account's own
	// (as the change left it), else its groups' (EffectiveRole). A change
	// that ends a person's own role hands them their group's, and a preview
	// that left it out would judge a state the account never reaches — both
	// ways: a gain through the group unseen, a gain it never gets refused.
	if u != nil && u.ID != 0 {
		roleID, via := EffectiveRole(in.CustomRoleID, in.Groups, snap.rules, in.ProviderID)
		in.CustomRoleID, in.ViaGroup = roleID, nil
		if via != nil {
			in.ViaGroup = &GroupRef{ID: via.ID, Name: via.Name}
			// And the level that role sets: group.SyncLevels runs after every
			// such change and moves the account to it. Judged at the level the
			// request left, a Viewer's ceiling would hide every write the
			// group's role allows — and the account holds them once synced.
			if in.Role != model.RoleAdmin {
				for _, r := range snap.rules {
					if r != nil && r.ID == roleID {
						in.Role = LevelUnderGroups(in.Role, r, "")
						break
					}
				}
			}
		}
	}
	// After the change: it may be a role change, and the built-in role's
	// app decisions follow the role.
	if in.AppDecisions == nil {
		in.AppDecisions = snap.apps[in.Role]
	}
	return Resolve(in), nil
}

// snapshot returns the cached defaults and rules, reloading them when the
// cache is older than SnapshotTTL or than the last Invalidate.
func (l *Loader) snapshot(ctx context.Context) (*installSnapshot, error) {
	gen := generation.Load()
	l.mu.Lock()
	s := l.snap
	l.mu.Unlock()
	if s != nil && s.gen == gen && time.Since(s.at) < SnapshotTTL {
		return s, nil
	}

	defaults, err := LoadDefaults(ctx, l.Store)
	if err != nil {
		return nil, err
	}
	viewer, err := LoadRoleBase(ctx, l.Store, model.RoleViewer)
	if err != nil {
		return nil, err
	}
	rules, err := l.Store.ListPermissionRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("perm: rules: %w", err)
	}
	members, err := l.Store.ListUserCustomRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("perm: custom roles: %w", err)
	}
	apps, err := LoadAppDecisions(ctx, l.Store)
	if err != nil {
		return nil, err
	}
	groups, err := loadUserGroups(ctx, l.Store)
	if err != nil {
		return nil, err
	}
	s = &installSnapshot{defaults: defaults, viewer: viewer, apps: apps, rules: rules, members: members, groups: groups, gen: gen, at: time.Now()}
	l.mu.Lock()
	l.snap = s
	l.mu.Unlock()
	return s, nil
}

// loadUserGroups maps every account to the groups it is in, in group id
// order.
func loadUserGroups(ctx context.Context, store db.Store) (map[int64][]*model.Group, error) {
	all, err := store.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("perm: groups: %w", err)
	}
	byID := make(map[int64]*model.Group, len(all))
	for _, g := range all {
		byID[g.ID] = g
	}
	members, err := store.ListAllGroupMembers(ctx)
	if err != nil {
		return nil, fmt.Errorf("perm: group members: %w", err)
	}
	out := map[int64][]*model.Group{}
	// ListAllGroupMembers is in group id order, so each list is too.
	for _, m := range members {
		if g := byID[m.GroupID]; g != nil {
			out[m.UserID] = append(out[m.UserID], g)
		}
	}
	return out, nil
}

// LoadDefaults returns the install's defaults for role=user: the stored set,
// or Standard when none was ever saved.
func LoadDefaults(ctx context.Context, store db.Store) (Set, error) {
	raw, err := store.GetSetting(ctx, model.SettingPermissionDefaults)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
		return Standard, nil
	}
	if err != nil {
		return 0, fmt.Errorf("perm: defaults: %w", err)
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil {
		return 0, fmt.Errorf("perm: defaults: %w", err)
	}
	return FromStrings(keys).Without(AdminFull), nil
}

// SaveDefaults stores the install's defaults for role=user. Role-only
// permissions are refused rather than silently dropped.
func SaveDefaults(ctx context.Context, store db.Store, s Set) error {
	if s.Has(AdminFull) {
		return invalid("%q follows the account role and cannot be a default", AdminFull)
	}
	b, err := json.Marshal(s.Strings())
	if err != nil {
		return err
	}
	if err := store.UpsertSetting(ctx, model.SettingPermissionDefaults, string(b)); err != nil {
		return err
	}
	Invalidate()
	return nil
}

// LoadRoleBase returns the editable base of a built-in role: the defaults for
// role=user, the viewer defaults (capped at the viewer ceiling) for
// role=viewer. Administrators have no editable base — everything is theirs.
func LoadRoleBase(ctx context.Context, store db.Store, role string) (Set, error) {
	switch role {
	case model.RoleUser:
		return LoadDefaults(ctx, store)
	case model.RoleViewer:
		raw, err := store.GetSetting(ctx, model.SettingPermissionViewerDefaults)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && raw == "") {
			return ReadOnly, nil
		}
		if err != nil {
			return 0, fmt.Errorf("perm: viewer defaults: %w", err)
		}
		var keys []string
		if err := json.Unmarshal([]byte(raw), &keys); err != nil {
			return 0, fmt.Errorf("perm: viewer defaults: %w", err)
		}
		return FromStrings(keys) & viewerCeiling, nil
	default:
		return 0, invalid("role %q has no editable permissions", role)
	}
}

// ValidateRoleBase is SaveRoleBase's check without the write: role=user may
// not hold a role-only permission, role=viewer nothing past the viewer
// ceiling, and no other role has an editable base.
func ValidateRoleBase(role string, s Set) error {
	switch role {
	case model.RoleUser:
		if s.Has(AdminFull) {
			return invalid("%q follows the account role and cannot be a default", AdminFull)
		}
	case model.RoleViewer:
		if extra := s &^ viewerCeiling; extra != 0 {
			return invalid("a viewer can never hold %v", extra.Strings())
		}
	default:
		return invalid("role %q has no editable permissions", role)
	}
	return nil
}

// SaveRoleBase stores the base of a built-in role. A viewer's is refused if it
// names a permission a viewer can never hold, rather than silently trimmed.
func SaveRoleBase(ctx context.Context, store db.Store, role string, s Set) error {
	if err := ValidateRoleBase(role, s); err != nil {
		return err
	}
	switch role {
	case model.RoleUser:
		return SaveDefaults(ctx, store, s)
	case model.RoleViewer:
		b, err := json.Marshal(s.Strings())
		if err != nil {
			return err
		}
		if err := store.UpsertSetting(ctx, model.SettingPermissionViewerDefaults, string(b)); err != nil {
			return err
		}
		Invalidate()
		return nil
	default:
		return invalid("role %q has no editable permissions", role)
	}
}

// ViewerCeiling is every permission a viewer account can ever hold.
func ViewerCeiling() Set { return viewerCeiling }
