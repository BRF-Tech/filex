package model

import "time"

// Where a sign-in provider instance came from (auth_instances.origin,
// migration 00076, docs/TENANT-ADMIN.md).
const (
	// AuthOriginEnvironment: built from FILEX_AUTH_DRIVERS / the config file.
	// The row carries only its bindings; its configuration is the
	// environment's and is never stored.
	AuthOriginEnvironment = "environment"
	// AuthOriginPage: the platform operator made it on Admin → Identity
	// providers (or through the API / MCP).
	AuthOriginPage = "page"
	// AuthOriginTenant: a tenant's administrator made it for their own tenant
	// (OwnerProviderID). It is bound to that tenant only, ever.
	AuthOriginTenant = "tenant"
)

// Why a tenant is bound to an instance (provider_auth_instances.source).
const (
	AuthBindExplicit = "explicit"
	// AuthBindUpgrade: the one-time boot step bound every instance that existed
	// to every tenant that existed, so the upgrade changed nobody's sign-in.
	AuthBindUpgrade = "upgrade"
	// AuthBindPin: a 0.50 `provider` pin (FILEX_LDAP_PROVIDER,
	// FILEX_HEADER_PROVIDER), read as an implicit binding.
	AuthBindPin = "pin"
)

// AuthInstance is one configured sign-in provider: a driver (oidc, ldap, pam,
// windows, proxy-header) with its configuration, bound to the tenants it signs
// people in for (AuthBinding).
type AuthInstance struct {
	ID int64 `json:"id"`
	// Slug is the stable name. The first instance of each driver keeps the
	// driver's name, so the routes that addressed "the ldap" still do.
	Slug   string `json:"slug"`
	Driver string `json:"driver"`
	// Label is what the sign-in page calls it; "" = the driver's own words.
	Label  string `json:"label"`
	Origin string `json:"origin"`
	// OwnerProviderID is the tenant that made it (Origin == tenant), else nil.
	OwnerProviderID *int64 `json:"owner_provider_id,omitempty"`
	Enabled         bool   `json:"enabled"`
	// Legacy: saved before v0.43.0 and never applied; imported switched off.
	Legacy bool `json:"legacy,omitempty"`
	// ConfigJSON holds the driver's fields: field → value, every value a
	// string as the settings rows held them, a secret SEALED. Never sent to a
	// client as it is (authsetup redacts it).
	ConfigJSON string `json:"-"`
	// Promoted lists the scopes (tenant ids; 0 = the platform's own tenant) in
	// which an operating-system instance has already promoted its test
	// account: only the first switch-on in a scope promotes.
	Promoted  []int64   `json:"promoted,omitempty"`
	CreatedBy *int64    `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PromotedIn reports whether the instance already promoted a test account in
// a scope (0 = the platform's own tenant).
func (a *AuthInstance) PromotedIn(scope int64) bool {
	if a == nil {
		return false
	}
	for _, s := range a.Promoted {
		if s == scope {
			return true
		}
	}
	return false
}

// AuthBinding is one row of provider_auth_instances: a tenant signs in through
// an instance.
type AuthBinding struct {
	ProviderID int64     `json:"provider_id"`
	InstanceID int64     `json:"instance_id"`
	Source     string    `json:"source"`
	CreatedAt  time.Time `json:"created_at"`
}
