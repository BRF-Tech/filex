package model

import (
	"strings"
	"time"
)

// Role names — also stored in DB roles table.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
	// RoleViewer is a read-only account: it may only ever hold viewer-level
	// item grants and can view/download but never mutate (convert/edit/upload/
	// delete). Added with the RBAC/ACL feature (migration 00012).
	RoleViewer = "viewer"
)

// ValidRole reports whether name is one of the known, seeded roles. The
// roles table ships `admin`, `user` and `viewer` (see 00001_init.sql +
// 00012_rbac_acl.sql) and there is no dynamic role-creation surface, so
// anything else is rejected at the API boundary rather than silently writing
// an unresolvable role.
func ValidRole(name string) bool {
	switch name {
	case RoleAdmin, RoleUser, RoleViewer:
		return true
	default:
		return false
	}
}

// User represents an authenticated principal.
type User struct {
	ID                int64      `json:"id"`
	Email             string     `json:"email"`
	DisplayName       string     `json:"display_name"`
	PasswordHash      string     `json:"-"`
	Role              string     `json:"role"`
	TOTPSecret        string     `json:"-"`
	TOTPPendingSecret string     `json:"-"`
	TOTPEnabled       bool       `json:"totp_enabled"`
	TOTPRecoveryCodes []string   `json:"-"`
	Locale            string     `json:"locale"`
	Timezone          string     `json:"timezone"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastLoginAt       *time.Time `json:"last_login_at,omitempty"`
}

// IsAdmin returns true if the user has the admin role.
func (u *User) IsAdmin() bool {
	if u == nil {
		return false
	}
	return u.Role == RoleAdmin
}

// IsViewer returns true if the user has the read-only viewer role.
func (u *User) IsViewer() bool {
	if u == nil {
		return false
	}
	return u.Role == RoleViewer
}

// HasPermission checks whether the user's role permits an action.
// `*` is a wildcard. Tested in roles.permissions_json.
func (u *User) HasPermission(perm string, granted []string) bool {
	if u == nil {
		return false
	}
	for _, p := range granted {
		if p == "*" || p == perm {
			return true
		}
	}
	return false
}

// Session is a server-side login session.
type Session struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	IP        string    `json:"ip,omitempty"`
	UserAgent string    `json:"user_agent,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// APIToken is a long-lived bearer credential for non-interactive callers
// (AI agents, the work.example.com FilexClient, the MCP server). It is bound to
// a user so every authenticated call inherits that user's role. The
// plaintext value is shown only once at creation; only TokenHash (sha256
// hex) is persisted.
type APIToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Label      string     `json:"label"`
	TokenHash  string     `json:"-"`
	Scopes     string     `json:"scopes"` // comma-separated allow-list; "" == all
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// HasScope reports whether the token grants `want`. An empty Scopes field
// means "all scopes" (full access for the bound user's role).
func (t *APIToken) HasScope(want string) bool {
	if t == nil {
		return false
	}
	if strings.TrimSpace(t.Scopes) == "" {
		return true
	}
	for _, s := range strings.Split(t.Scopes, ",") {
		if strings.TrimSpace(s) == want {
			return true
		}
	}
	return false
}

// Role definition (DB row).
type Role struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

// AuditEntry is a record in audit_log.
type AuditEntry struct {
	ID         int64                  `json:"id"`
	UserID     *int64                 `json:"user_id,omitempty"`
	Action     string                 `json:"action"`
	TargetType string                 `json:"target_type,omitempty"`
	TargetID   string                 `json:"target_id,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
	IP         string                 `json:"ip,omitempty"`
	CreatedAt  time.Time              `json:"created_at"`
}
