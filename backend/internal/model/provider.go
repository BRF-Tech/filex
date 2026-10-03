package model

import "time"

// Auth types a provider (tenant) can use to authenticate its users.
const (
	AuthTypeOIDC  = "oidc"
	AuthTypeLocal = "local"
)

// DefaultProviderSlug is the always-present "original org" tenant. It is created
// by migration 00014 and is inert while multi-tenant mode is off. On a
// multi-tenant install it doubles as the platform supertenant by default (see
// docs/MULTI-TENANCY.md); operators may add further providers as tenants.
const DefaultProviderSlug = "default"

// Provider is a tenant: an auth realm (OIDC or local) bound to a host and
// linked to one or more storages via provider_storages. Users carry a
// provider_id tag assigned at (JIT) login.
//
// Isolation is enforced in TWO independent layers (docs/MULTI-TENANCY.md):
//  1. file data — storage confinement (a provider only reaches its linked
//     storages); a bug here is the only way file data could cross a tenant.
//  2. directory — user/share/grant scoping by provider_id; a bug here leaks at
//     most a name, never file data.
//
// A provider with IsSupertenant is confine-EXEMPT and platform-scoped: its
// admins see every tenant. There must be at most one, it should be a hardened
// realm, and a local bootstrap admin remains the break-glass path.
type Provider struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	// Realm is the tenant's sign-in name (migration 00073, package tenant):
	// the sign-in form's Realm field and `realm/name` over SFTP. Set when the
	// tenant is created and never changed — UpdateProvider does not write it.
	// Empty for the supertenant, which is signed in to with an empty realm.
	Realm    string `json:"realm"`
	Name     string `json:"name"`
	Host     string `json:"host,omitempty"`
	AuthType string `json:"auth_type"`

	OIDCIssuer       string `json:"oidc_issuer,omitempty"`
	OIDCClientID     string `json:"oidc_client_id,omitempty"`
	OIDCClientSecret string `json:"-"` // never serialized to the client
	OIDCRedirectURL  string `json:"oidc_redirect_url,omitempty"`
	RoleClaim        string `json:"role_claim,omitempty"`
	AdminGroup       string `json:"admin_group,omitempty"`

	// CookieDomain, when set (e.g. ".example.com"), is stamped as the Domain
	// attribute on this tenant's session cookie so the tenant's subdomains
	// share the session. Empty = derive from Host by dropping its first
	// label (files.example.com → .example.com), falling back to the global
	// FILEX_COOKIE_DOMAIN. Only consulted in multi-tenant mode.
	CookieDomain string `json:"cookie_domain,omitempty"`

	IsSupertenant bool `json:"is_supertenant"`
	Enabled       bool `json:"enabled"`

	// AllowInsecureAuth lets this tenant's own sign-in providers reach an
	// internal address and plain ldap:// (migration 00076,
	// docs/TENANT-ADMIN.md). Off by default; only a platform super
	// administrator sets it (db.Store.SetProviderAllowInsecureAuth), and
	// UpdateProvider never writes it.
	AllowInsecureAuth bool `json:"allow_insecure_auth"`

	// OIDCTrustEmail is the tenant's own OIDC's "trust this provider's email
	// addresses" (migration 00079, docs/SSO.md): every address it sends counts
	// as verified, `email_verified` or not. Off for a tenant made from 0.50 on;
	// ON for the ones an upgrade found (OIDCTrustEmailByUpgrade). Written by
	// db.Store.SetProviderOIDCTrustEmail only; UpdateProvider never writes it.
	OIDCTrustEmail bool `json:"oidc_trust_email"`
	// OIDCTrustEmailByUpgrade: the value above is the one the upgrade to 0.50
	// set, to keep the old behaviour, and nobody has saved it since. Filled
	// by the API from the upgrade's record, not a column.
	OIDCTrustEmailByUpgrade bool `json:"oidc_trust_email_by_upgrade,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LoginRealm is the realm this tenant's accounts are addressed by: its Realm,
// or "" for the supertenant — the platform's own tenant is signed in to with
// an empty realm, and a tenant that became the supertenant (the flag moved to
// it) keeps its old realm only as a second name.
func (p *Provider) LoginRealm() string {
	if p == nil || p.IsSupertenant {
		return ""
	}
	return p.Realm
}

// ProviderStorage links a tenant to a storage (M:N; 1:1 in the current UI).
// A storage reachable through no provider link is only visible to a
// supertenant / to single-tenant mode.
type ProviderStorage struct {
	ProviderID int64 `json:"provider_id"`
	StorageID  int64 `json:"storage_id"`
}
