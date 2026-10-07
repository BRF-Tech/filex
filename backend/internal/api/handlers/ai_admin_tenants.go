package handlers

import (
	"net/http"
	"net/url"
)

// The admin_tenants_* MCP tools: the tenant lifecycle of Admin → Tenants
// (docs/TENANT-ADMIN.md), over the same handler (Providers) the screen and
// /api/admin/providers use. Every guard is the handler's: platform operator
// only (requireSupertenant), the realm given at creation and refused on a
// change, the platform's own tenant never suspended or deleted, a demo's
// writes refused.
//
// ⚠ Moving the platform flag (is_supertenant) has no tool. It changes whose
// administrators administer the whole platform; it stays a deliberate
// request on /api/admin/providers.

// tenantFieldsIn are the fields a tenant is created or changed with. Every one
// is optional on an update: an absent field is left as it is.
type tenantFieldsIn struct {
	Slug             *string `json:"slug,omitempty" jsonschema:"the tenant's short name (unique)"`
	Name             *string `json:"name,omitempty" jsonschema:"display name"`
	Host             *string `json:"host,omitempty" jsonschema:"the tenant's own address (files.acme.example), empty for none: then its people sign in on the platform's address with the realm"`
	CookieDomain     *string `json:"cookie_domain,omitempty" jsonschema:"session cookie Domain for the tenant's subdomains (.acme.example); empty derives it from host"`
	AuthType         *string `json:"auth_type,omitempty" jsonschema:"oidc or local"`
	OIDCIssuer       *string `json:"oidc_issuer,omitempty" jsonschema:"the tenant's own OIDC issuer URL"`
	OIDCClientID     *string `json:"oidc_client_id,omitempty"`
	OIDCClientSecret *string `json:"oidc_client_secret,omitempty" jsonschema:"stored, never sent back; empty keeps the stored one"`
	OIDCRedirectURL  *string `json:"oidc_redirect_url,omitempty" jsonschema:"empty = https://<host>/api/auth/oidc/callback"`
	RoleClaim        *string `json:"role_claim,omitempty"`
	AdminGroup       *string `json:"admin_group,omitempty"`
	Enabled          *bool   `json:"enabled,omitempty" jsonschema:"false suspends the tenant: nobody of it can sign in, nothing is deleted"`
	OIDCTrustEmail   *bool   `json:"oidc_trust_email,omitempty" jsonschema:"trust every email address the tenant's own OIDC sends as verified, email_verified or not (default off). Off, an address it does not mark verified signs in to no account not yet bound to the person's SSO identity, and opens a new account switched off"`
}

func (f tenantFieldsIn) body() map[string]any {
	b := map[string]any{}
	for k, v := range map[string]*string{
		"slug": f.Slug, "name": f.Name, "host": f.Host, "cookie_domain": f.CookieDomain,
		"auth_type": f.AuthType, "oidc_issuer": f.OIDCIssuer, "oidc_client_id": f.OIDCClientID,
		"oidc_client_secret": f.OIDCClientSecret, "oidc_redirect_url": f.OIDCRedirectURL,
		"role_claim": f.RoleClaim, "admin_group": f.AdminGroup,
	} {
		if v != nil {
			b[k] = *v
		}
	}
	if f.Enabled != nil {
		b["enabled"] = *f.Enabled
	}
	if f.OIDCTrustEmail != nil {
		b["oidc_trust_email"] = *f.OIDCTrustEmail
	}
	return b
}

type tenantCreateIn struct {
	tenantFieldsIn
	Realm string `json:"realm,omitempty" jsonschema:"the tenant's sign-in name; default: the slug. Lower-case letters, digits and dashes. It can NEVER be changed afterwards - ask admin_tenants_suggest_realm first"`
}

type tenantUpdateIn struct {
	ID int64 `json:"id" jsonschema:"the tenant's numeric id"`
	tenantFieldsIn
}

type tenantDeleteIn struct {
	ID    int64 `json:"id" jsonschema:"the tenant's numeric id"`
	Force bool  `json:"force,omitempty" jsonschema:"also delete the tenant's accounts (their public links close with them); storages and files are never deleted, only unlinked"`
}

type tenantStorageIn struct {
	ID        int64 `json:"id" jsonschema:"the tenant's numeric id"`
	StorageID int64 `json:"storage_id" jsonschema:"the storage's numeric id (admin_storages_list)"`
}

type tenantSlugIn struct {
	Slug string `json:"slug" jsonschema:"the slug (or the name) of the tenant about to be created"`
}

const tenantModel = " Platform operator only: an administrator of a tenant is refused (403 supertenant_only). " +
	"A tenant's realm is its sign-in name (the Realm field, realm/name over SFTP, alex@<realm>.local addresses): " +
	"given when it is created and never changed."

// registerTenantTools wires the admin_tenants_* tools.
func registerTenantTools(r *adminReg, a *AIAdmin) {
	base := "/api/ai/admin/providers"
	regAdminTool(r, "admin_tenants_list", "List the tenants (providers): slug, name, realm, address (host), whether the tenant is the "+
		"platform's own (is_supertenant) and enabled, the storages linked to it (storage_ids) and its number of accounts "+
		"(user_count); multi_tenant says whether multi-tenant mode is in force (FILEX_MULTI_TENANT, or the Multi-tenant mode page of the admin panel; off: only the platform's own tenant can sign in)."+tenantModel,
		func(_ adminVoidIn) reqSpec {
			return reqSpec{handler: a.tenants.List, method: http.MethodGet, path: base}
		})
	regAdminTool(r, "admin_tenants_get", "One tenant by id, as admin_tenants_list shows it."+tenantModel,
		func(in adminIDIn) reqSpec {
			return reqSpec{handler: a.tenants.Get, method: http.MethodGet, path: base + "/" + itoa(in.ID), urlParams: idParam(in.ID)}
		})
	regAdminTool(r, "admin_tenants_suggest_realm", "The realm the tenant screen would offer for a slug: {realm, base, available}. "+
		"base is the slug made realm-shaped (marks dropped, runs of other characters one dash); realm is the first variant "+
		"of it (base, base-2, ...) that is not reserved or taken, empty when nothing realm-shaped is left. Nothing is reserved "+
		"by asking."+tenantModel,
		func(in tenantSlugIn) reqSpec {
			return reqSpec{handler: a.tenants.RealmSuggestion, method: http.MethodGet, path: base + "/realm-suggestion",
				query: url.Values{"slug": {in.Slug}}}
		})
	regAdminTool(r, "admin_tenants_create", "Create a tenant. slug is required; realm defaults to the slug and is refused when it is "+
		"not realm-shaped (realm_invalid), reserved (realm_reserved) or another tenant's (realm_taken). A slug or a host "+
		"another tenant has is refused (slug_taken, host_taken). Link its storages with admin_tenants_link_storage."+tenantModel,
		func(in tenantCreateIn) reqSpec {
			b := in.body()
			if in.Realm != "" {
				b["realm"] = in.Realm
			}
			return reqSpec{handler: a.tenants.Create, method: http.MethodPost, path: base, body: b}
		})
	regAdminTool(r, "admin_tenants_update", "Change a tenant by id: only the fields sent change. The realm is not a field - it never "+
		"changes. enabled:false suspends the tenant (nobody of it signs in, nothing is deleted); the platform's own tenant "+
		"cannot be suspended."+tenantModel,
		func(in tenantUpdateIn) reqSpec {
			return reqSpec{handler: a.tenants.Update, method: http.MethodPatch, path: base + "/" + itoa(in.ID),
				urlParams: idParam(in.ID), body: in.body()}
		})
	regAdminTool(r, "admin_tenants_delete", "Delete a tenant by id. A tenant that still has accounts is refused unless force is "+
		"true, which deletes the accounts too. Its storages are unlinked, never deleted, and their files are untouched. The "+
		"platform's own tenant cannot be deleted."+tenantModel,
		func(in tenantDeleteIn) reqSpec {
			var q url.Values
			if in.Force {
				q = url.Values{"force": {"1"}}
			}
			return reqSpec{handler: a.tenants.Delete, method: http.MethodDelete, path: base + "/" + itoa(in.ID),
				urlParams: idParam(in.ID), query: q}
		})
	regAdminTool(r, "admin_tenants_link_storage", "Link a storage to a tenant: its people reach that storage and no other "+
		"the tenant is not linked to."+tenantModel,
		func(in tenantStorageIn) reqSpec {
			return reqSpec{handler: a.tenants.LinkStorage, method: http.MethodPost, path: base + "/" + itoa(in.ID) + "/storages",
				urlParams: idParam(in.ID), body: map[string]any{"storage_id": in.StorageID}}
		})
	regAdminTool(r, "admin_tenants_unlink_storage", "Unlink a storage from a tenant (the storage and its files stay; the "+
		"tenant's people no longer reach it)."+tenantModel,
		func(in tenantStorageIn) reqSpec {
			return reqSpec{handler: a.tenants.UnlinkStorage, method: http.MethodDelete,
				path:      base + "/" + itoa(in.ID) + "/storages/" + itoa(in.StorageID),
				urlParams: map[string]string{"id": itoa(in.ID), "storageID": itoa(in.StorageID)}}
		})
}
