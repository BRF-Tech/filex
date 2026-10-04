// Package group is what groups of people (migration 00074, docs/GROUPS.md)
// do beyond storing rows: checking a group before it is stored, keeping the
// memberships an outside directory decides in step with it, and keeping each
// member's built-in level in step with the role their group gives them.
//
// What a group REACHES is decided elsewhere: its folder grants by
// acl.UserGrants, its role by perm.EffectiveRole.
package group

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

// MaxNameLen bounds a group's name, MaxDescriptionLen its description.
const (
	MaxNameLen        = 100
	MaxDescriptionLen = 1000
	// MaxLinks bounds how many outside groups one group names.
	MaxLinks = 50
	// MaxPriority bounds a group's priority either way.
	MaxPriority = 1000
)

// ErrInvalid wraps every validation failure so callers can map it to a 400.
var ErrInvalid = errors.New("group: invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

// linkKinds are the outside directories a group can be linked to.
var linkKinds = map[string]bool{model.GroupLinkSSO: true, model.GroupLinkLDAP: true}

// ldapSpace is the space a DN may carry around its separators.
var ldapSpace = regexp.MustCompile(`\s*([,=+])\s*`)

// LDAPValue is the form an LDAP group's name is compared in, on both sides
// (a group's link, and the groups a directory sign-in reports): lower case,
// and a DN without the space around its commas, equals and plus signs — a
// directory is free to write "CN=Finance, OU=Groups" for what an
// administrator typed as "cn=finance,ou=groups". LDAP compares these names
// without case.
func LDAPValue(s string) string {
	return ldapSpace.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "$1")
}

// Normalize validates g in place and canonicalizes what it can: trimmed name
// and description, trimmed and de-duplicated links. It does not check that
// the role exists or is in the group's tenant — that needs the store and is
// the handler's job.
func Normalize(g *model.Group) error {
	g.Name = strings.TrimSpace(g.Name)
	if g.Name == "" {
		return invalid("group name is required")
	}
	if len([]rune(g.Name)) > MaxNameLen {
		return invalid("group name is longer than %d characters", MaxNameLen)
	}
	g.Description = strings.TrimSpace(g.Description)
	if len([]rune(g.Description)) > MaxDescriptionLen {
		return invalid("group description is longer than %d characters", MaxDescriptionLen)
	}
	if g.RoleID != nil && *g.RoleID <= 0 {
		g.RoleID = nil
	}
	if g.Priority < -MaxPriority || g.Priority > MaxPriority {
		return invalid("priority must be between %d and %d", -MaxPriority, MaxPriority)
	}
	seen := map[model.GroupLink]bool{}
	links := make([]model.GroupLink, 0, len(g.Links))
	for _, l := range g.Links {
		l.Kind = strings.TrimSpace(l.Kind)
		l.Value = strings.TrimSpace(l.Value)
		if l.Kind == model.GroupLinkLDAP {
			l.Value = LDAPValue(l.Value)
		}
		if !linkKinds[l.Kind] {
			return invalid("unknown link kind %q", l.Kind)
		}
		if l.Value == "" {
			return invalid("a %s link needs a group name", l.Kind)
		}
		if !seen[l] {
			seen[l] = true
			links = append(links, l)
		}
	}
	if len(links) > MaxLinks {
		return invalid("a group can name at most %d outside groups", MaxLinks)
	}
	g.Links = links
	return nil
}

// SyncLinked makes a person's memberships through one kind of link (kind,
// e.g. model.GroupLinkSSO) match the outside groups a sign-in says they are
// in (values, compared exactly — LDAP names in LDAPValue's form): they join every group of their tenant — and
// install-wide groups only if they are the supertenant's — that names one of
// those values, and leave every group they were in only
// through such a link that no longer does. A member added by hand stays
// whatever the directory says. It reports whether anything changed.
func SyncLinked(ctx context.Context, store db.Store, u *model.User, kind string, values []string) (bool, error) {
	if u == nil {
		return false, nil
	}
	groups, super, err := linkContext(ctx, store)
	if err != nil {
		return false, err
	}
	return syncLinked(ctx, store, u, kind, values, groups, super)
}

// linkContext is what matching links needs from the store: every group, and
// the supertenant — read once for a whole pass (SyncStoredSSO), not per person.
func linkContext(ctx context.Context, store db.Store) ([]*model.Group, *model.Provider, error) {
	groups, err := store.ListGroups(ctx)
	if err != nil {
		return nil, nil, err
	}
	super, err := store.GetSupertenant(ctx)
	if err != nil {
		return nil, nil, err
	}
	return groups, super, nil
}

func syncLinked(ctx context.Context, store db.Store, u *model.User, kind string, values []string, groups []*model.Group, super *model.Provider) (bool, error) {
	same := func(v string) string { return v }
	if kind == model.GroupLinkLDAP {
		same = LDAPValue
	}
	want := map[string]bool{}
	for _, v := range values {
		if v = same(v); v != "" {
			want[v] = true
		}
	}
	// An install-wide group's links match only the supertenant's sign-ins
	// (and an account in no tenant): on a multi-tenant install every
	// tenant's IdP sends its own claim values, and `ops` from one tenant is
	// not the supertenant's `ops`. A tenant's people are put in an
	// install-wide group only by hand — by the supertenant.
	platform := u.ProviderID == nil || (super != nil && *u.ProviderID == super.ID)
	var ids []int64
	for _, g := range groups {
		if !perm.GroupInScope(g, u.ProviderID) || (g.ProviderID == nil && !platform) {
			continue
		}
		for _, l := range g.Links {
			if l.Kind == kind && want[same(l.Value)] && (!g.GivesAdmin || adminLinkCounts(g, l, u)) {
				ids = append(ids, g.ID)
				break
			}
		}
	}
	added, removed, err := store.SetUserLinkedGroups(ctx, u.ID, kind, ids)
	if err != nil {
		return false, err
	}
	if len(added) == 0 && len(removed) == 0 {
		return false, nil
	}
	if err := SyncLevels(ctx, store, []int64{u.ID}); err != nil {
		return true, err
	}
	perm.Invalidate()
	return true, nil
}

// SyncLevels keeps each account's built-in level in step with the role its
// groups give it (perm.RoleHolder: User when the role can change or share
// anything, Viewer otherwise) — what giving a person a role on their own page
// does. Called after anything that can change which role a member gets
// through their groups: a membership, a group's role or priority, a role's
// permissions or switch.
//
// The level an account had before a group's role first moved it is kept
// (user_group_levels) and put back when no group role applies any more — so
// leaving a read-only group gives back User, and leaving a group whose role
// made a Viewer account a User gives back Viewer, never the full built-in
// User role it never had.
//
// An account with a role of its own is left alone (its own role set its
// level, and the kept level is dropped), and so is an administrator (bound
// by no role).
func SyncLevels(ctx context.Context, store db.Store, userIDs []int64) error {
	if len(userIDs) == 0 {
		return nil
	}
	all, err := store.ListGroups(ctx)
	if err != nil {
		return err
	}
	rules, err := store.ListPermissionRules(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]*model.Group, len(all))
	for _, g := range all {
		byID[g.ID] = g
	}
	ruleByID := make(map[int64]*model.PermissionRule, len(rules))
	for _, r := range rules {
		ruleByID[r.ID] = r
	}
	done := map[int64]bool{}
	changed := false
	for _, uid := range userIDs {
		if done[uid] {
			continue
		}
		done[uid] = true
		u, err := store.GetUser(ctx, uid)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && u == nil) {
			continue
		}
		if err != nil {
			return err
		}
		ms, err := store.ListUserGroupMemberships(ctx, uid)
		if err != nil {
			return err
		}
		groups := make([]*model.Group, 0, len(ms))
		for _, m := range ms {
			if g := byID[m.GroupID]; g != nil {
				groups = append(groups, g)
			}
		}
		// Administrator through a group (migration 00083) comes before any
		// role, the person's own included: it is more than any role gives.
		admin := GivesAdmin(groups, u.ProviderID)
		switch {
		case u.IsAdmin() && !u.AdminByGroup:
			// An administrator made by hand (or by the SSO admin mapping) is
			// bound by no role, and no group demotes them. A level kept from
			// before a group's role must not come back if they are demoted
			// later (the SSO admin mapping promotes and demotes without
			// passing through the role routes that forget it).
			if err := store.DeleteUserGroupLevel(ctx, uid); err != nil {
				return err
			}
			continue
		case u.IsAdmin() && admin:
			continue
		case u.IsAdmin():
			// No group makes them one any more: back to the level from
			// before — never the last administrator, who would leave
			// nobody able to administer filex.
			last, err := LastAdmin(ctx, store, u)
			if err != nil {
				return err
			}
			if last {
				slog.Warn("groups: no group makes this account an administrator any more, but it is the last one; it stays one",
					slog.Int64("user_id", uid), slog.String("email", u.Email))
				continue
			}
			before, ok, err := store.GetUserGroupLevel(ctx, uid)
			if err != nil {
				return err
			}
			level := model.RoleUser
			if ok && model.ValidRole(before) && before != model.RoleAdmin {
				level = before
			}
			if err := store.UpdateUserRole(ctx, uid, level); err != nil {
				return err
			}
			if err := store.DeleteUserGroupLevel(ctx, uid); err != nil {
				return err
			}
			slog.Info("groups: no group makes this account an administrator any more",
				slog.Int64("user_id", uid), slog.String("email", u.Email), slog.String("role", level))
			u.Role, u.AdminByGroup = level, false
			changed = true
			// On to the role their groups give now, if one does.
		case admin:
			if err := store.SetUserGroupLevel(ctx, uid, u.Role); err != nil {
				return err
			}
			if err := store.SetUserAdminByGroup(ctx, uid); err != nil {
				return err
			}
			slog.Info("groups: a group made this account an administrator",
				slog.Int64("user_id", uid), slog.String("email", u.Email))
			changed = true
			continue
		}
		own, err := store.GetUserCustomRole(ctx, uid)
		if err != nil {
			return err
		}
		if own != 0 {
			if err := store.DeleteUserGroupLevel(ctx, uid); err != nil {
				return err
			}
			continue
		}
		roleID, _ := perm.EffectiveRole(0, groups, rules, u.ProviderID)
		rule := ruleByID[roleID]
		if rule == nil {
			// No group role any more: back to the level from before one
			// (perm.LevelUnderGroups, the rule every preview judges by).
			before, ok, err := store.GetUserGroupLevel(ctx, uid)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
			if want := perm.LevelUnderGroups(u.Role, nil, before); want != u.Role {
				if err := store.UpdateUserRole(ctx, uid, want); err != nil {
					return err
				}
				changed = true
			}
			if err := store.DeleteUserGroupLevel(ctx, uid); err != nil {
				return err
			}
			continue
		}
		if want := perm.LevelUnderGroups(u.Role, rule, ""); u.Role != want {
			if err := store.SetUserGroupLevel(ctx, uid, u.Role); err != nil {
				return err
			}
			if err := store.UpdateUserRole(ctx, uid, want); err != nil {
				return err
			}
			changed = true
		}
	}
	if changed {
		perm.Invalidate()
	}
	return nil
}

// adminLinkCounts is the stricter matching of a group that makes its members
// administrators. An LDAP link counts only by the group's full DN — a common
// name matches a group of that name anywhere in any directory — and only for
// people of the group's own directory: another directory's administrator
// (a partner's) can name a group whatever they like, its DN included.
func adminLinkCounts(g *model.Group, l model.GroupLink, u *model.User) bool {
	if l.Kind != model.GroupLinkLDAP {
		return true
	}
	return IsLDAPDN(l.Value) && u.DirectoryOwner() != "" && u.DirectoryOwner() == GroupDirectory(g)
}

// IsLDAPDN reports whether an LDAP link names a group by its DN rather than
// by its common name.
func IsLDAPDN(v string) bool {
	return strings.Contains(v, "=")
}

// GroupDirectory is the LDAP directory a group belongs to: the one whose
// sync brought it in, else the main directory.
func GroupDirectory(g *model.Group) string {
	if i := strings.Index(g.DirectoryID, ":"); i > 0 {
		return g.DirectoryID[:i]
	}
	return model.MainDirectory
}

// GivesAdmin reports whether one of these groups — the account's, of its
// tenant — makes its members administrators.
func GivesAdmin(groups []*model.Group, providerID *int64) bool {
	for _, g := range groups {
		if g != nil && g.GivesAdmin && perm.GroupInScope(g, providerID) {
			return true
		}
	}
	return false
}

// LastAdmin reports whether u is the only administrator of its tenant still
// switched on — taking it away would leave nobody to administer it. (An
// administrator of another tenant administers only that tenant.)
func LastAdmin(ctx context.Context, store db.Store, u *model.User) (bool, error) {
	users, err := store.ListUsers(ctx)
	if err != nil {
		return false, err
	}
	for _, o := range users {
		if o.ID != u.ID && o.IsAdmin() && o.Enabled && sameTenant(o.ProviderID, u.ProviderID) {
			return false, nil
		}
	}
	return true, nil
}

func sameTenant(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// Via is the role an account holds through a group.
type Via struct {
	RoleID    int64  `json:"role_id"`
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
}

// EffectiveRoles maps every account with no custom role of its own, whose
// groups give it one, to that role — what the Roles and Users pages count
// and show. Administrators are left out: no role binds them.
func EffectiveRoles(ctx context.Context, store db.Store, users []*model.User) (map[int64]Via, error) {
	all, err := store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := map[int64]Via{}
	if len(all) == 0 {
		return out, nil
	}
	rules, err := store.ListPermissionRules(ctx)
	if err != nil {
		return nil, err
	}
	own, err := store.ListUserCustomRoles(ctx)
	if err != nil {
		return nil, err
	}
	members, err := store.ListAllGroupMembers(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*model.Group, len(all))
	for _, g := range all {
		byID[g.ID] = g
	}
	of := map[int64][]*model.Group{}
	for _, m := range members {
		if g := byID[m.GroupID]; g != nil {
			of[m.UserID] = append(of[m.UserID], g)
		}
	}
	for _, u := range users {
		if u == nil || u.IsAdmin() || own[u.ID] != 0 || len(of[u.ID]) == 0 {
			continue
		}
		if id, via := perm.EffectiveRole(0, of[u.ID], rules, u.ProviderID); via != nil {
			out[u.ID] = Via{RoleID: id, GroupID: via.ID, GroupName: via.Name}
		}
	}
	return out, nil
}

// SyncStored re-applies every account's directory-linked memberships from
// the groups its LAST sign-in carried — SSO's (user_sso_groups) and LDAP's
// (user_ldap_groups) — what a changed link on a group does at once, instead
// of waiting for each person to sign in again. users are the accounts to
// consider (the caller's tenant's); an account that never signed in through
// a directory has no stored groups and is only taken out of groups it was in
// through one, if any.
func SyncStored(ctx context.Context, store db.Store, users []*model.User) error {
	groups, super, err := linkContext(ctx, store)
	if err != nil {
		return err
	}
	kinds := []struct {
		kind   string
		stored func(context.Context, int64) ([]string, error)
	}{
		{model.GroupLinkSSO, store.ListUserSSOGroups},
		{model.GroupLinkLDAP, store.ListUserLDAPGroups},
	}
	for _, u := range users {
		if u == nil {
			continue
		}
		var ms []*model.GroupMember
		for _, k := range kinds {
			values, err := k.stored(ctx, u.ID)
			if err != nil {
				return err
			}
			if len(values) == 0 {
				if ms == nil {
					if ms, err = store.ListUserGroupMemberships(ctx, u.ID); err != nil {
						return err
					}
				}
				via := false
				for _, m := range ms {
					via = via || m.Source == k.kind
				}
				if !via {
					continue
				}
			}
			if _, err := syncLinked(ctx, store, u, k.kind, values, groups, super); err != nil {
				return err
			}
		}
	}
	return nil
}

// MemberIDs returns the user ids of every member of the given groups.
func MemberIDs(ctx context.Context, store db.Store, groupIDs ...int64) ([]int64, error) {
	var out []int64
	for _, id := range groupIDs {
		ms, err := store.ListGroupMembers(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			out = append(out, m.UserID)
		}
	}
	return out, nil
}

// HoldingRole returns the ids of the groups that hold role roleID.
func HoldingRole(ctx context.Context, store db.Store, roleID int64) ([]int64, error) {
	all, err := store.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	var out []int64
	for _, g := range all {
		if g.RoleID != nil && *g.RoleID == roleID {
			out = append(out, g.ID)
		}
	}
	return out, nil
}
