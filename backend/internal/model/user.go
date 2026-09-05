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
	ID    int64  `json:"id"`
	Email string `json:"email"`
	// Username is the short login name (migration 00025), unique across the
	// install. It exists because the connection protocols cannot carry an
	// e-mail comfortably — `sftp://ada@example.com@host` needs escaping, and
	// rclone/WinSCP config files split on `@`. Every login surface accepts
	// EITHER this or the e-mail; internal/identity owns that rule and is the
	// only place allowed to decide which one an identifier is.
	Username          string   `json:"username"`
	DisplayName       string   `json:"display_name"`
	PasswordHash      string   `json:"-"`
	Role              string   `json:"role"`
	TOTPSecret        string   `json:"-"`
	TOTPPendingSecret string   `json:"-"`
	TOTPEnabled       bool     `json:"totp_enabled"`
	TOTPRecoveryCodes []string `json:"-"`
	Locale            string   `json:"locale"`
	Timezone          string   `json:"timezone"`
	// AvatarURL is the profile picture: a small data: URI or an http(s) /
	// site-relative URL (migration 00023). It is the face the collaboration
	// presence strip draws instead of initials, for every client of this
	// account — browser session, desktop app, and any API key minted under it.
	AvatarURL   string     `json:"avatar_url,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	// Multi-tenancy (docs/MULTI-TENANCY.md): the tenant (provider) this user
	// belongs to. Nil only on rows predating the 00014 backfill; assigned
	// immutably at JIT login. OIDCSubject pins the OIDC identity per provider.
	ProviderID  *int64 `json:"provider_id,omitempty"`
	OIDCSubject string `json:"-"`
	// Storage accounting (migration 00003). QuotaBytes == 0 means unlimited.
	// Carried on the user so an admin table costs one call instead of one
	// /quota request per row.
	QuotaBytes int64 `json:"quota_bytes"`
	UsageBytes int64 `json:"used_bytes"`
	// Enabled (migration 00022) gates whether the account may start a
	// session — local login, OIDC and /dav alike. Disabling is not a soft
	// delete: files, quota and grants are untouched.
	Enabled bool `json:"enabled"`
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
	ID        int64  `json:"id"`
	UserID    int64  `json:"user_id"`
	Label     string `json:"label"`
	TokenHash string `json:"-"`
	Scopes    string `json:"scopes"` // comma-separated allow-list; "" == all
	// Usernames is the comma-separated allow-list of identities a caller may
	// act under (X-Filex-Token-User); the FIRST entry is the default. One
	// durable token often serves several consumers (work panel, PWA, a PC MCP
	// client) — the chosen username is what audit, shares and presence show.
	// Empty == only the token's label is usable (legacy behavior).
	Usernames string `json:"usernames"`
	// Kind is what this credential IS — TokenKindUser (a person's own: CLI,
	// WebDAV, SFTP, S3, `filex mount`) or TokenKindApp (an integration: a host
	// app's proxy, a bot, an MCP client). Every token acts as its owner either
	// way; kind only decides whether the identity-bearing surfaces are drawn
	// for it. See NormalizeTokenKind for why "" reads as app.
	Kind       string     `json:"kind"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Token kinds. A token declares what it is, because "who is calling" and
// "is there a person behind this call" are different questions and filex only
// ever answered the first one.
const (
	// TokenKindUser — a person's own credential. Acts as that person and hides
	// nothing: they may list and revoke their own tokens, and the explorer
	// draws Recent / Starred / Shared with me / API keys as usual.
	TokenKindUser = "user"
	// TokenKindApp — an integration. Identity-bearing surfaces are suppressed
	// because there is no single person behind the call: in the embeds we run,
	// ONE shared token injected by the host's proxy serves every visitor, so
	// "your API keys" would mean the proxy's own credential and "your Recent"
	// would mean the token owner's history shown to a stranger.
	TokenKindApp = "app"
)

// NormalizeTokenKind maps a stored/incoming value to a canonical kind.
//
// ⚠ Anything that is not exactly "user" is app — including the empty string.
// Rows written before migration 00030, and any caller that forgets the field,
// must land on the restricting side: the failure mode of guessing "user" is an
// embed visitor listing and revoking the token the embed itself runs on, while
// the failure mode of guessing "app" is one admin edit.
func NormalizeTokenKind(s string) string {
	if strings.TrimSpace(s) == TokenKindUser {
		return TokenKindUser
	}
	return TokenKindApp
}

// IsApp reports whether this token is an integration rather than a person.
// A nil token is NOT an app: no token at all means a cookie/OIDC session,
// which is always a person.
func (t *APIToken) IsApp() bool {
	return t != nil && NormalizeTokenKind(t.Kind) == TokenKindApp
}

// UsernameList returns the parsed, trimmed username allow-list (may be empty).
func (t *APIToken) UsernameList() []string {
	if t == nil || strings.TrimSpace(t.Usernames) == "" {
		return nil
	}
	parts := strings.Split(t.Usernames, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// DefaultUsername is the identity used when the caller doesn't pick one: the
// first configured username, else the token's label.
func (t *APIToken) DefaultUsername() string {
	if t == nil {
		return ""
	}
	if l := t.UsernameList(); len(l) > 0 {
		return l[0]
	}
	return strings.TrimSpace(t.Label)
}

// ResolveUsername maps the caller's requested identity to an effective one.
// Empty request → the default (ok). A request matching the allow-list (or the
// default itself) → that name (ok). Anything else → not ok; callers must
// reject the request so a typo'd integration surfaces immediately instead of
// silently blending into the default identity.
func (t *APIToken) ResolveUsername(requested string) (string, bool) {
	requested = strings.TrimSpace(requested)
	def := t.DefaultUsername()
	if requested == "" {
		return def, true
	}
	if requested == def {
		return requested, true
	}
	for _, u := range t.UsernameList() {
		if requested == u {
			return requested, true
		}
	}
	return "", false
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
