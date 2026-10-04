package model

import "time"

// Group membership sources (user_group_members.source, migration 00074). A
// person is in a group because an administrator put them there, or because
// an outside directory the group is linked to says so. Each link kind owns
// its own rows: a sign-in through SSO replaces the "sso" rows and never
// touches a "manual" one.
const (
	GroupSourceManual = "manual"
	GroupSourceSSO    = "sso"
	GroupSourceLDAP   = "ldap"
)

// GroupLinkSSO links a group to a value of the SSO provider's role claim
// (the same claim the admin mapping and a role's starting-role targets read).
// Compared exactly.
const GroupLinkSSO = "sso"

// GroupLinkLDAP links a group to an LDAP / Active Directory group, named by
// its distinguished name (cn=finance,ou=groups,dc=example,dc=com) or its
// common name alone (finance). Compared without case, and a DN without the
// spaces around its commas and equals signs (group.LDAPValue).
const GroupLinkLDAP = "ldap"

// GroupLink names a group of an outside directory whose people are members
// of a filex group. Kind is the directory (GroupLinkSSO, GroupLinkLDAP),
// Value its group's name there.
type GroupLink struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Group is one row of user_groups (migration 00074): a named set of people
// in one tenant, which can be given folder grants (GroupFileGrant) and a
// custom role (RoleID — the role of every member who has none of their own).
//
// ProviderID scopes the group to one tenant; nil is install-wide.
type Group struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ProviderID  *int64 `json:"provider_id,omitempty"`
	RoleID      *int64 `json:"role_id"`
	// GivesAdmin: the group makes its members administrators — the
	// built-in Administrator role, full access — instead of giving a role
	// (migration 00083). RoleID is then nil.
	GivesAdmin bool `json:"gives_admin"`
	// Priority orders the groups a member's role can come from: the
	// highest first, then the lowest id (perm.EffectiveRole).
	Priority int         `json:"priority"`
	Links    []GroupLink `json:"links"`
	// A group directory sync brought in (migration 00081): DirectoryID is
	// the directory group's permanent id ("ldap:…"), DirectoryName the name
	// it last had there, DirectoryState "" while the directory has it and
	// GroupDirectoryRemoved once it does not. All empty for every other
	// group.
	DirectoryID    string    `json:"directory_id,omitempty"`
	DirectoryName  string    `json:"directory_name,omitempty"`
	DirectoryState string    `json:"directory_state,omitempty"`
	CreatedBy      *int64    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// GroupDirectoryRemoved is a synced group whose directory group is gone.
const GroupDirectoryRemoved = "removed"

// Synced reports whether directory sync brought this group in and the
// directory still has it — its LDAP link is then the directory's.
func (g *Group) Synced() bool {
	return g != nil && g.DirectoryID != "" && g.DirectoryState != GroupDirectoryRemoved
}

// GroupMember is one row of user_group_members.
type GroupMember struct {
	GroupID int64     `json:"group_id"`
	UserID  int64     `json:"user_id"`
	Source  string    `json:"source"`
	AddedAt time.Time `json:"added_at"`
}
