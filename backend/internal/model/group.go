package model

import "time"

// Group membership sources (user_group_members.source, migration 00072). A
// person is in a group because an administrator put them there, or because
// an outside directory the group is linked to says so. Each link kind owns
// its own rows: a sign-in through SSO replaces the "sso" rows and never
// touches a "manual" one.
const (
	GroupSourceManual = "manual"
	GroupSourceSSO    = "sso"
)

// GroupLinkSSO links a group to a value of the SSO provider's role claim
// (the same claim the admin mapping and a role's starting-role targets read).
// Compared exactly.
const GroupLinkSSO = "sso"

// GroupLink names a group of an outside directory whose people are members
// of a filex group. Kind is the directory (GroupLinkSSO), Value its group's
// name there.
type GroupLink struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Group is one row of user_groups (migration 00072): a named set of people
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
	// Priority orders the groups a member's role can come from: the
	// highest first, then the lowest id (perm.EffectiveRole).
	Priority  int         `json:"priority"`
	Links     []GroupLink `json:"links"`
	CreatedBy *int64      `json:"created_by,omitempty"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// GroupMember is one row of user_group_members.
type GroupMember struct {
	GroupID int64     `json:"group_id"`
	UserID  int64     `json:"user_id"`
	Source  string    `json:"source"`
	AddedAt time.Time `json:"added_at"`
}
