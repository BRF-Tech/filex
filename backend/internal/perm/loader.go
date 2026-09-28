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
	rules    []*model.PermissionRule
	// members is user id → the one custom role they hold.
	members map[int64]int64
	gen     uint64
	at      time.Time
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
	in.CustomRoleID = snap.members[u.ID]
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
	s = &installSnapshot{defaults: defaults, viewer: viewer, rules: rules, members: members, gen: gen, at: time.Now()}
	l.mu.Lock()
	l.snap = s
	l.mu.Unlock()
	return s, nil
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

// SaveRoleBase stores the base of a built-in role. A viewer's is refused if it
// names a permission a viewer can never hold, rather than silently trimmed.
func SaveRoleBase(ctx context.Context, store db.Store, role string, s Set) error {
	switch role {
	case model.RoleUser:
		return SaveDefaults(ctx, store, s)
	case model.RoleViewer:
		if extra := s &^ viewerCeiling; extra != 0 {
			return invalid("a viewer can never hold %v", extra.Strings())
		}
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
