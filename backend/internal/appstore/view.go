package appstore

// Who sees the embedded store screen (#162): an administrator turns it on,
// picks which trusted stores it shows, and who sees it - everybody, people
// of some built-in roles, members of some groups. In multi-tenant mode each
// tenant has a setting of its own (Scope); a tenant with none sees nothing.
//
// Seeing the screen gives a person two things only: reading the catalog and
// leaving a request (pluginreq, kind app, source "store"). Installing stays
// an administrator's review, exactly as before.

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// keyViewPrefix + a scope holds that scope's view settings.
const keyViewPrefix = "view:"

// ScopeDefault is the single-tenant scope (no tenancy, or its main tenant
// before multi-tenant mode was turned on).
const ScopeDefault = "default"

// Audiences: who in the scope sees the screen.
const (
	AudienceEveryone = "everyone"
	AudienceRoles    = "roles"
	AudienceGroups   = "groups"
)

// builtInRoles a view may name (model.RoleAdmin, RoleUser, RoleViewer).
var builtInRoles = []string{"admin", "user", "viewer"}

// ViewSettings is one scope's embedded store screen.
type ViewSettings struct {
	Enabled bool `json:"enabled"`
	// Stores are the trusted stores it shows, in this order; a store no
	// longer trusted is left out when it is read.
	Stores   []string `json:"stores"`
	Audience string   `json:"audience"`
	// Roles (AudienceRoles) are built-in role names; Groups (AudienceGroups)
	// group ids. A person sees the screen when they hold one of the roles or
	// are in one of the groups.
	Roles         []string  `json:"roles"`
	Groups        []int64   `json:"groups"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
	UpdatedByName string    `json:"updated_by_name,omitempty"`
}

// Scope is the key of a tenant's settings: ScopeDefault without tenancy,
// "t<provider id>" with it.
func Scope(multiTenant bool, providerID int64) string {
	if !multiTenant {
		return ScopeDefault
	}
	return "t" + strconv.FormatInt(providerID, 10)
}

// Normalize cleans settings an administrator sent: known roles only, no
// repeats, an audience it understands. stores are the origins trusted now.
func (v *ViewSettings) Normalize(trusted []string) error {
	if v.Audience == "" {
		v.Audience = AudienceEveryone
	}
	switch v.Audience {
	case AudienceEveryone, AudienceRoles, AudienceGroups:
	default:
		return errf(CodeBadStore, "audience is everyone, roles or groups")
	}
	seen := map[string]bool{}
	stores := []string{}
	for _, o := range v.Stores {
		if seen[o] {
			continue
		}
		if !slices.Contains(trusted, o) {
			return errf(CodeTrustRequired, "%s is not a trusted store: trust it first (Apps, Trusted stores)", o)
		}
		seen[o] = true
		stores = append(stores, o)
	}
	v.Stores = stores
	roles := []string{}
	for _, r := range v.Roles {
		r = strings.TrimSpace(r)
		if !slices.Contains(builtInRoles, r) {
			return errf(CodeBadStore, "role %q is not one of admin, user, viewer", r)
		}
		if !slices.Contains(roles, r) {
			roles = append(roles, r)
		}
	}
	sort.Strings(roles)
	v.Roles = roles
	groups := []int64{}
	for _, g := range v.Groups {
		if g > 0 && !slices.Contains(groups, g) {
			groups = append(groups, g)
		}
	}
	slices.Sort(groups)
	v.Groups = groups
	if v.Enabled && len(v.Stores) == 0 {
		return errf(CodeBadStore, "the store screen shows at least one trusted store")
	}
	if v.Enabled && v.Audience == AudienceRoles && len(v.Roles) == 0 {
		return errf(CodeBadStore, "name at least one role, or show the screen to everyone")
	}
	if v.Enabled && v.Audience == AudienceGroups && len(v.Groups) == 0 {
		return errf(CodeBadStore, "name at least one group, or show the screen to everyone")
	}
	return nil
}

// Allows reports whether a person with role and these groups sees the
// screen.
func (v *ViewSettings) Allows(role string, groups []int64) bool {
	if v == nil || !v.Enabled || len(v.Stores) == 0 {
		return false
	}
	switch v.Audience {
	case AudienceEveryone:
		return true
	case AudienceRoles:
		return slices.Contains(v.Roles, role)
	case AudienceGroups:
		for _, g := range groups {
			if slices.Contains(v.Groups, g) {
				return true
			}
		}
	}
	return false
}

// View answers a scope's settings, the stores narrowed to those still
// trusted (nil settings: never set, the screen is off).
func (s *Service) View(ctx context.Context, scope string) (*ViewSettings, error) {
	var v ViewSettings
	ok, err := s.getJSON(ctx, keyViewPrefix+scope, &v)
	if err != nil || !ok {
		return nil, err
	}
	kept := []string{}
	for _, o := range v.Stores {
		if s.TrustStatus(ctx, o) != "" {
			kept = append(kept, o)
		}
	}
	v.Stores = kept
	if v.Roles == nil {
		v.Roles = []string{}
	}
	if v.Groups == nil {
		v.Groups = []int64{}
	}
	return &v, nil
}

// Views answers every scope's settings (the panel's list in multi-tenant
// mode).
func (s *Service) Views(ctx context.Context) (map[string]*ViewSettings, error) {
	rows, err := s.opts.Store.ListAppStoreState(ctx, keyViewPrefix)
	if err != nil {
		return nil, err
	}
	out := map[string]*ViewSettings{}
	for k := range rows {
		scope := strings.TrimPrefix(k, keyViewPrefix)
		v, err := s.View(ctx, scope)
		if err != nil {
			return nil, err
		}
		if v != nil {
			out[scope] = v
		}
	}
	return out, nil
}

// SetView saves a scope's settings (Normalize first) and writes the audit
// row.
func (s *Service) SetView(ctx context.Context, scope string, v ViewSettings, actorID *int64, actorName string) (*ViewSettings, error) {
	list, err := s.ListTrust(ctx)
	if err != nil {
		return nil, err
	}
	trusted := make([]string, 0, len(list))
	for _, t := range list {
		trusted = append(trusted, t.Origin)
	}
	if err := v.Normalize(trusted); err != nil {
		return nil, err
	}
	v.UpdatedAt = s.opts.Now().UTC()
	v.UpdatedByName = actorName
	if err := s.putJSON(ctx, keyViewPrefix+scope, v); err != nil {
		return nil, err
	}
	s.audit(ctx, actorID, "app_store.view", "app_store", scope, map[string]any{
		"scope": scope, "enabled": v.Enabled, "stores": v.Stores, "audience": v.Audience, "roles": v.Roles, "groups": v.Groups,
	})
	return &v, nil
}
