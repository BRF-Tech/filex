package perm

import (
	"sort"

	"github.com/brf-tech/filex/backend/internal/model"
)

// Groups and roles (migration 00072). A group can hold a custom role; it is
// the role of every member who has none of their own. So an account's one
// custom role is:
//
//  1. its own (user_custom_roles), whatever its groups hold; else
//  2. the role of the first of its groups — highest priority first, then
//     lowest id — that holds one in its tenant, skipping a switched-off
//     role while a later group's role is on; else
//  3. none: its built-in role.
//
// If every role its groups hold is switched off, it gets the first of those
// — which gives nothing, as a switched-off role does. Falling to the built-in
// User role instead would be MORE access than any of its groups gives.
//
// Only the choice is made here. What a role then does — its list replacing
// the built-in role's, a switched-off role giving nothing — is the same
// whichever way the account came to hold it.

// EffectiveRole is the custom role an account holds: own when it is not 0,
// else its groups' (see above). groups are the account's; rules are every
// custom role (a group's role missing from them, or of another tenant, is
// no role). via is the group it came from, nil for the account's own role
// or none.
func EffectiveRole(own int64, groups []*model.Group, rules []*model.PermissionRule, providerID *int64) (roleID int64, via *model.Group) {
	if own != 0 {
		return own, nil
	}
	byID := make(map[int64]*model.PermissionRule, len(rules))
	for _, r := range rules {
		if r != nil {
			byID[r.ID] = r
		}
	}
	ordered := make([]*model.Group, 0, len(groups))
	for _, g := range groups {
		if g != nil && g.RoleID != nil && *g.RoleID != 0 && GroupInScope(g, providerID) {
			ordered = append(ordered, g)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return GroupBefore(ordered[i], ordered[j]) })
	var off *model.Group
	for _, g := range ordered {
		r := byID[*g.RoleID]
		if r == nil || !inScope(r, providerID) {
			continue
		}
		if r.Enabled {
			return r.ID, g
		}
		if off == nil {
			off = g
		}
	}
	if off != nil {
		return *off.RoleID, off
	}
	return 0, nil
}

// GroupBefore is the order a member's role is looked for in: the higher
// priority first, then the lower id.
func GroupBefore(a, b *model.Group) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	return a.ID < b.ID
}

// GroupInScope reports whether a group reaches an account of the given
// tenant: an install-wide group reaches everyone, a tenant's group only its
// own people.
func GroupInScope(g *model.Group, providerID *int64) bool {
	return g.ProviderID == nil || (providerID != nil && *providerID == *g.ProviderID)
}
