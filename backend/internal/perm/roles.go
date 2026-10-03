package perm

import "github.com/brf-tech/filex/backend/internal/model"

// Custom roles are stand-alone (migration 00069): a role is its own list of
// permissions, like the built-in User and Viewer roles, plus an optional
// "different in some folders" part — Allow/Deny for file actions where its
// Conditions match (Effects + Conditions).

// viewerCapped is every permission a viewer account can never hold: adding,
// changing, deleting and sharing.
var viewerCapped = filter(func(d Def) bool { return d.ViewerCapped })

// RoleSet is what a custom role allows everywhere: its own list.
func RoleSet(r *model.PermissionRule) Set {
	if r == nil {
		return 0
	}
	return FromStrings(r.Permissions).Without(AdminFull)
}

// HolderRole is the built-in role the people holding r get. It is not a
// choice: a role that can add, change, delete or share anything — everywhere
// or in some folders — needs the User level to do it; any other role is a
// Viewer's, which keeps a read-only role read-only on every door, even if an
// exception later allows a write.
func HolderRole(r *model.PermissionRule, set Set) string {
	if set&viewerCapped != 0 {
		return model.RoleUser
	}
	if r != nil && !r.Conditions.Empty() {
		for k, eff := range r.Effects {
			if d, ok := Lookup(Perm(k)); ok && eff == model.PermAllow && d.ViewerCapped {
				return model.RoleUser
			}
		}
	}
	return model.RoleViewer
}

// RoleHolder is HolderRole for a role as stored.
func RoleHolder(r *model.PermissionRule) string {
	return HolderRole(r, RoleSet(r))
}

// PreviewHolderRole is RoleHolder for a role still being edited: the body as
// the role editor has it now, which may not be valid yet (no name, a path
// half typed). It answers what the role's people would be on if the body were
// saved as it stands — with the folder conditions a save stores (a blank path
// names no folder) — and refuses nothing. The custom role editor asks it
// (POST /api/admin/roles/preview) for each app permission's "Default"
// instead of keeping its own copy of this rule, which 0.49.0's did.
func PreviewHolderRole(r model.PermissionRule) string {
	r.Conditions = model.PermRuleConditions{
		StorageIDs: append([]int64(nil), r.Conditions.StorageIDs...),
		Paths:      append([]string(nil), r.Conditions.Paths...),
	}
	// A body normalizeConditions refuses cannot be saved at all; the answer
	// is then for its conditions as written.
	_ = normalizeConditions(&r)
	return RoleHolder(&r)
}
