package model

import (
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/tokenperm"
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

// TimezoneUnset is the account time zone of somebody who has not chosen one.
//
// ⚠⚠ It is the EMPTY string, and that is not a placeholder for "UTC". A
// timestamp is an instant; the reader picks the clock (packages/core
// lib/timezone). An empty zone tells every client "use the clock of the device
// this person is looking through" — the owner's rule, verbatim: "ben 03'te
// video yükledim, GMT 0 eleman o videonun yüklenme saatini kendi zaman
// diliminde görecek."
//
// Every account used to be created with the literal "UTC" (six call sites, and
// the column default), so a person who had never opened the setting could not
// be told apart from one who had deliberately picked UTC. The web app honours
// the account's zone and therefore drew every date in UTC, while the same
// explorer embedded on another page — which cannot see the account — drew the
// browser's clock. Measured 2026-09-14 on one fresh account: 11:57 PM UTC in
// the app beside 4:57 PM PDT in the embed, for the same file.
// Migration 00040 clears the rows that default wrote.
const TimezoneUnset = ""

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
	// OIDCIssuer is the `iss` of the SSO identity the account is bound to,
	// with OIDCSubject its `sub` (migration 00079): once bound, an SSO sign-in
	// finds the account by that pair, and the same e-mail address arriving
	// with another identity is refused (docs/SSO.md).
	OIDCIssuer string `json:"-"`
	// SSOLinked: the account is bound to an SSO identity (it has a subject).
	// Filled by the store from OIDCIssuer/OIDCSubject, which are never sent:
	// the users page offers "Remove SSO bind" on such an account.
	SSOLinked bool `json:"sso_linked,omitempty"`
	// Storage accounting (migration 00003). QuotaBytes == 0 means unlimited.
	// Carried on the user so an admin table costs one call instead of one
	// /quota request per row.
	QuotaBytes int64 `json:"quota_bytes"`
	UsageBytes int64 `json:"used_bytes"`
	// Enabled (migration 00022) gates whether the account may start a
	// session — local login, OIDC and /dav alike. Disabling is not a soft
	// delete: files, quota and grants are untouched.
	Enabled bool `json:"enabled"`
	// DisabledReason says why the SERVER switched the account off (migration
	// 00079): DisabledPendingApproval for an account an SSO sign-in opened
	// whose address the identity provider did not confirm, DisabledByDirectory
	// for one LDAP directory sync switched off. "" for an account that is on,
	// or that an administrator switched off; switching it on or off clears it.
	// The users page shows it beside the disabled state.
	DisabledReason string `json:"disabled_reason,omitempty"`
	// AuthSource says where the account comes from (migration 00084): one of
	// the AuthSource* values, or "" for an account from before it with no
	// password here and no SSO identity (its next sign-in labels it; any
	// LDAP directory may take it until then - ldap.notMine). A label for the
	// Users page; it decides only which LDAP directory signs the account in.
	AuthSource string `json:"auth_source"`
	// AuthDirectory names the LDAP provider that made the account — its
	// slug, "ldap" or another instance's (migration 00084); "" for any other
	// account. Only that directory signs it in (DirectoryOwner).
	AuthDirectory string `json:"auth_directory,omitempty"`
	// DirectoryID is the person's permanent id in that directory —
	// "<directory>:<entryUUID or objectGUID>" (migration 00085); "" when the
	// directory has none, or for any other account. A sign-in or sync finds
	// the account by it before the e-mail.
	DirectoryID string `json:"-"`
	// AdminByGroup: an administrator because a group gives its members
	// Administrator (migration 00086); back to their earlier level when no
	// group does. Any other change of role clears it.
	AdminByGroup bool `json:"admin_by_group,omitempty"`
}

// DisabledPendingApproval is the DisabledReason of an account opened, switched
// off, by an SSO sign-in whose identity provider did not say the e-mail
// address was verified: an administrator switches it on to approve it.
const DisabledPendingApproval = "pending_approval"

// DisabledByDirectory is the DisabledReason of an account LDAP directory sync
// switched off — its person switched off in the directory, or no longer
// listed there (sync_disable_missing). Sync switches it back on when the
// directory lets the person back in.
const DisabledByDirectory = "directory"

// DisabledByDirectory reports whether directory sync switched the account
// off (and may switch it back on).
func (u *User) DisabledByDirectory() bool {
	return u != nil && !u.Enabled && u.DisabledReason == DisabledByDirectory
}

// MainDirectory is the provider name of the first LDAP directory.
const MainDirectory = "ldap"

// DirectoryOwner is the LDAP directory an account belongs to: the one that
// made it, and the main directory for an LDAP account from before
// users.auth_directory. "" for an account no directory made.
func (u *User) DirectoryOwner() string {
	if u == nil {
		return ""
	}
	if u.AuthDirectory != "" {
		return u.AuthDirectory
	}
	if u.AuthSource == AuthSourceLDAP {
		return MainDirectory
	}
	return ""
}

// Where an account comes from (users.auth_source, migration 00084).
const (
	// AuthSourceLocal: made here — on the Users page, by an invitation, at
	// first run.
	AuthSourceLocal = "local"
	// AuthSourceSSO: an OpenID Connect sign-in made it.
	AuthSourceSSO = "sso"
	// AuthSourceLDAP: an LDAP / Active Directory sign-in made it.
	AuthSourceLDAP = "ldap"
	// AuthSourceProxy: a trusted reverse proxy's headers made it.
	AuthSourceProxy = "proxy"
)

// IsAdmin returns true if the user has the admin role.
// PersonLabel is how filex names a person to anybody reading a screen: the
// display name, else the username, else the e-mail address — trimmed, the
// first one that is not empty.
//
// ⚠⚠ ONE rule, everywhere a person is shown (QA, 2026-09-21): the same
// administrator was "admin2" in the Owner column, "admin@local" in
// notifications and the signing app, and a full name on signatures — the
// Owner column's lookup said display → username → e-mail, the presence strip
// display → e-mail local part, the permissions panel display → e-mail. This
// is the rule; the browser's twin is personName()
// (packages/core/src/lib/personName.ts) and web/tests/lib/personName.test.ts
// holds the two to the same cases. docs/CONTRIBUTING.md → "A person is named
// one way".
func PersonLabel(displayName, username, email string) string {
	for _, s := range []string{displayName, username, email} {
		if v := strings.TrimSpace(s); v != "" {
			return v
		}
	}
	return ""
}

// Label is PersonLabel for this account ("" for a nil user).
func (u *User) Label() string {
	if u == nil {
		return ""
	}
	return PersonLabel(u.DisplayName, u.Username, u.Email)
}

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
	// Scopes is the comma-separated allow-list of what this token may do.
	// ⚠⚠ An EMPTY list grants NOTHING (HasScope answers false). Until
	// v0.43.0 it meant "every scope", `admin` included, and a token created
	// with nothing ticked could read the admin API; every door that issues a
	// token now refuses an empty list (400 scopes_required), and a stored
	// empty row fails closed. Do not restore the old reading — the reasoning
	// is in auth/drivers/apitoken/issue.go.
	Scopes string `json:"scopes"`
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
	Kind string `json:"kind"`
	// Source is which door minted the token (migration 00060): TokenSourceDesktop
	// for a desktop pairing, "" for every other. It decides which permission
	// USING the token falls under — access.desktop or access.api (package perm).
	Source string `json:"source,omitempty"`
	// Permissions is the level this token holds of each permission of the
	// catalogue (package tokenperm): the level its Scopes name, the
	// permission's default for the rest. Never stored - the token lists and
	// mint answers fill it (WithPermissions), so a screen draws a token's
	// levels without a copy of the default rule.
	Permissions map[string]tokenperm.Level `json:"permissions,omitempty"`
	LastUsedAt  *time.Time                 `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time                 `json:"expires_at,omitempty"`
	CreatedAt   time.Time                  `json:"created_at"`
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

// TokenSourceDesktop marks a token minted by the desktop app's pairing
// (handlers/desktop_auth.go).
const TokenSourceDesktop = "desktop"

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

// HasScope reports whether the token grants `want` — only when `want` is
// in its list.
//
// ⚠⚠ An EMPTY list grants NOTHING (fail closed). Until v0.43.0 it granted
// everything, admin included, and a token minted on the admin screen with
// nothing ticked read /api/ai/admin/users (release-candidate sweep,
// 2026-09-21). No door issues an empty list any more (apitoken.ParseIssued)
// and migration 00054 wrote every old empty list out as the explicit full
// one, so a row that is empty now is a mistake — and a mistake in an
// authorization check must fall towards "no".
func (t *APIToken) HasScope(want string) bool {
	if t == nil {
		return false
	}
	if strings.TrimSpace(t.Scopes) == "" {
		return false
	}
	for _, s := range strings.Split(t.Scopes, ",") {
		if strings.TrimSpace(s) == want {
			return true
		}
	}
	return false
}

// PermLevel is the level this token holds of the permission key (package
// tokenperm): the level its list names, else the permission's default - the
// level of every token minted before the permission existed. A nil token
// holds nothing; so does an empty list (HasScope).
func (t *APIToken) PermLevel(key string) tokenperm.Level {
	if t == nil {
		return tokenperm.None
	}
	return tokenperm.LevelIn(t.Scopes, key)
}

// WithPermissions fills Permissions from Scopes and returns t.
func (t *APIToken) WithPermissions() *APIToken {
	if t != nil {
		t.Permissions = tokenperm.LevelsIn(t.Scopes)
	}
	return t
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
