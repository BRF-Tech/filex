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
