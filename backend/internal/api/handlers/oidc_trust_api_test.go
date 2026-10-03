package handlers_test

// The settings around which account an SSO sign-in opens (0.50, docs/SSO.md),
// on every surface that sets them:
//
//   - "trust this provider's email addresses": `trust_email` of a page or row
//     OIDC (Admin → Identity providers, a tenant's own providers, MCP), and
//     `oidc_trust_email` of a tenant's own OIDC on its row (the tenants API,
//     MCP). Off by default; a change is in the audit row;
//   - the page is told when the value is the one the upgrade to 0.50 set, and
//     stops being told once somebody saves it;
//   - removing an account's SSO bind (`sso_unlink`), audited.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func TestOIDCTrust_APageOIDCsSettingIsOffUntilSavedAndAudited(t *testing.T) {
	base, client, store, _, _ := ssoTenantSetup(t, nil)
	ctx := context.Background()
	_, list := listProviders(t, client, base)
	sso := list["corp-sso"]
	require.NotNil(t, sso)
	fields, err := json.Marshal(sso["fields"])
	require.NoError(t, err)
	assert.Contains(t, string(fields), `{"default":"false","key":"trust_email","kind":"bool"}`)
	assert.NotEqual(t, true, sso["config_redacted"].(map[string]any)["trust_email"], "off for a provider made on 0.50")
	assert.Nil(t, sso["set_by_upgrade"])

	status, body := patchProvider(t, client, base, "corp-sso", map[string]any{"config": map[string]any{"trust_email": true}})
	require.Equal(t, http.StatusOK, status, "%v", body)
	_, list = listProviders(t, client, base)
	assert.Equal(t, true, list["corp-sso"]["config_redacted"].(map[string]any)["trust_email"])
	rows, _, err := store.ListAuditFiltered(ctx, nil, "auth_provider.update", nil, nil, 10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	assert.Contains(t, rows[0].Entry.Metadata["changed_fields"], "trust_email", "the change is in the audit row")
}

func TestOIDCTrust_ThePageIsToldTheUpgradeSetIt(t *testing.T) {
	base, client, store, _, _ := ssoTenantSetup(t, map[string]any{"trust_email": true})
	ctx := context.Background()
	// What authsetup.UpgradeOIDCTrust records for a provider that existed.
	require.NoError(t, store.UpsertSetting(ctx, "auth.oidc_trust.by_upgrade", `{"instances":["corp-sso"]}`))
	_, list := listProviders(t, client, base)
	assert.Equal(t, []any{"trust_email"}, list["corp-sso"]["set_by_upgrade"])

	// Saved with the setting in hand: the operator's now.
	status, body := patchProvider(t, client, base, "corp-sso", map[string]any{"config": map[string]any{"trust_email": true}})
	require.Equal(t, http.StatusOK, status, "%v", body)
	_, list = listProviders(t, client, base)
	assert.Nil(t, list["corp-sso"]["set_by_upgrade"])
	assert.Equal(t, true, list["corp-sso"]["config_redacted"].(map[string]any)["trust_email"])
}

func TestOIDCTrust_ATenantsOwnOIDCOnItsRow(t *testing.T) {
	base, client, store := multiLiveServer(t)
	ctx := context.Background()
	status, raw := doReq(t, client, http.MethodPost, base+"/api/admin/providers", map[string]any{
		"slug": "acme", "name": "Acme", "auth_type": "oidc", "oidc_issuer": "https://acme.example", "oidc_client_id": "acme",
	})
	require.Equal(t, http.StatusCreated, status, string(raw))
	var created struct {
		ID             int64 `json:"id"`
		OIDCTrustEmail bool  `json:"oidc_trust_email"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	assert.False(t, created.OIDCTrustEmail, "off for a tenant made on 0.50")

	status, raw = doReq(t, client, http.MethodPatch, base+"/api/admin/providers/"+itoa(created.ID), map[string]any{"oidc_trust_email": true})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"oidc_trust_email":true`)
	p, err := store.GetProvider(ctx, created.ID)
	require.NoError(t, err)
	assert.True(t, p.OIDCTrustEmail)
	rows, _, err := store.ListAuditFiltered(ctx, nil, "", nil, nil, 20, 0)
	require.NoError(t, err)
	audited := false
	for _, r := range rows {
		if r.Entry.Metadata["oidc_trust_email_after"] == true {
			audited = true
		}
	}
	assert.True(t, audited, "the change is in the audit row")

	// Set by the upgrade: the screen is told, until it is saved.
	require.NoError(t, store.UpsertSetting(ctx, "auth.oidc_trust.by_upgrade", `{"tenants":[`+itoa(created.ID)+`]}`))
	status, raw = doReq(t, client, http.MethodGet, base+"/api/admin/providers/"+itoa(created.ID), nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"oidc_trust_email_by_upgrade":true`)
	status, raw = doReq(t, client, http.MethodPatch, base+"/api/admin/providers/"+itoa(created.ID), map[string]any{"oidc_trust_email": false})
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"oidc_trust_email":false`)
	assert.NotContains(t, string(raw), "oidc_trust_email_by_upgrade")
}

func TestSSOUnlink_RemovesTheBindAndIsAudited(t *testing.T) {
	base, client, store := multiLiveServer(t)
	ctx := context.Background()
	u, err := store.CreateUser(ctx, "ada@x.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserOIDCIdentity(ctx, u.ID, "https://idp.example", "ada-1"))
	status, raw := doReq(t, client, http.MethodGet, base+"/api/admin/users/"+itoa(u.ID), nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"sso_linked":true`, "the users page knows it is bound")
	assert.NotContains(t, string(raw), "ada-1", "the identity itself is never sent")

	status, raw = doReq(t, client, http.MethodPatch, base+"/api/admin/users/"+itoa(u.ID), map[string]any{"sso_unlink": true})
	require.Equal(t, http.StatusOK, status, string(raw))
	again, err := store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	assert.False(t, again.SSOLinked)
	assert.Empty(t, again.OIDCIssuer)
	rows, _, err := store.ListAuditFiltered(ctx, nil, "", nil, nil, 20, 0)
	require.NoError(t, err)
	audited := false
	for _, r := range rows {
		if r.Entry.Metadata["sso_unlinked"] == true {
			audited = true
		}
	}
	assert.True(t, audited, "the removal is in the audit row")

	// A tenant's administrator removes binds in their own tenant only.
	betaID, betaAdmin, betaPassword := seedTenant(t, store, "beta", "admin@beta.test", false)
	_ = betaID
	require.NoError(t, store.SetUserOIDCIdentity(ctx, u.ID, "https://idp.example", "ada-1"))
	tc := browser(base)
	loginIn(t, base, tc, "beta", betaAdmin, betaPassword)
	status, _ = doReq(t, tc, http.MethodPatch, base+"/api/admin/users/"+itoa(u.ID), map[string]any{"sso_unlink": true})
	assert.NotEqual(t, http.StatusOK, status, "another tenant's account")
	again, err = store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, again.SSOLinked, "untouched")
}
