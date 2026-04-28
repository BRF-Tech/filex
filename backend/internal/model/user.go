package model

import "time"

// Role names — also stored in DB roles table.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// User represents an authenticated principal.
type User struct {
	ID                 int64      `json:"id"`
	Email              string     `json:"email"`
	PasswordHash       string     `json:"-"`
	Role               string     `json:"role"`
	TOTPSecret         string     `json:"-"`
	TOTPPendingSecret  string     `json:"-"`
	TOTPEnabled        bool       `json:"totp_enabled"`
	TOTPRecoveryCodes  []string   `json:"-"`
	Locale             string     `json:"locale"`
	Timezone           string     `json:"timezone"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	LastLoginAt        *time.Time `json:"last_login_at,omitempty"`
}

// IsAdmin returns true if the user has the admin role.
func (u *User) IsAdmin() bool {
	if u == nil {
		return false
	}
	return u.Role == RoleAdmin
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
