package handlers_test

// The first-login rule, end to end over HTTP: the Identity providers page
// saves auto_create / allowed_groups, the provider it builds applies them at the
// browser's OIDC callback, a refusal is the same login-page bounce as any
// failed sign-in (no account/group oracle) and the operator reads the reason in
// the audit log.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
)

func TestIdentityProviders_FirstLoginSettingsAreOnThePageAndApply(t *testing.T) {
	idp := newAdaIssuer(t, "right-secret")
	srv, client, store, _, _ := liveServer(t, nil)

	// The schema the page draws its form from carries the new fields.
	_, list := listProviders(t, client, srv.URL)
	keys := func(name string) map[string]bool {
		out := map[string]bool{}
		for _, f := range list[name]["fields"].([]any) {
			out[f.(map[string]any)["key"].(string)] = true
		}
		return out
	}
	for _, name := range []string{"oidc", "ldap", "proxy-header"} {
		assert.True(t, keys(name)["auto_create"], name)
		assert.True(t, keys(name)["allowed_groups"], name)
	}
	assert.True(t, keys("ldap")["group_attr"])

	cfg := map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "right-secret",
		"redirect_url": srv.URL + "/api/auth/oidc/callback",
		"auto_create":  false,
	}
	status, body := patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": true, "config": cfg})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, true, body["test_ok"])
	found := false
	for _, c := range body["checks"].([]any) {
		if c.(map[string]any)["id"] == "first_login_closed" {
			found = true
		}
	}
	assert.True(t, found, "the test says the provider only admits existing accounts: %v", body["checks"])

	ok, _ := oidcSignIn(t, srv.URL)
	assert.False(t, ok, "auto_create off: the new person is not signed in")
	_, gerr := store.GetUserByEmail(context.Background(), "ada@idp.example")
	assert.Error(t, gerr, "and no account was made")
	rows, err := store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	var reason any
	for _, r := range rows {
		if r.Action == auth.AuditFirstLoginRefused {
			reason = r.Metadata["reason"]
		}
	}
	assert.Equal(t, auth.ReasonAutoCreateOff, reason, "the operator reads why")

	// Switched on again on the page: applied at once, the person gets in.
	status, body = patchProvider(t, client, srv.URL, "oidc", map[string]any{"config": map[string]any{"auto_create": true}})
	require.Equal(t, http.StatusOK, status, "%v", body)
	ok, who := oidcSignIn(t, srv.URL)
	require.True(t, ok)
	assert.Equal(t, "ada@idp.example", who)
}

// allowed_groups with nowhere to read groups from would refuse everybody: the
// provider test fails that step, so the save asks for a confirmation.
func TestIdentityProviders_AllowedGroupsWithoutAGroupSourceFailsTheTest(t *testing.T) {
	idp := newAdaIssuer(t, "right-secret")
	srv, client, _, _, _ := liveServer(t, nil)
	cfg := map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "right-secret",
		"redirect_url":   srv.URL + "/api/auth/oidc/callback",
		"allowed_groups": "staff",
	}
	status, body := patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": true, "config": cfg})
	require.Equal(t, http.StatusConflict, status, "%v", body)
	assert.Equal(t, "test_failed", body["error"])
	assert.Contains(t, body["failed"], "first_login")

	cfg["role_claim"] = "groups"
	status, body = patchProvider(t, client, srv.URL, "oidc", map[string]any{"enabled": true, "config": cfg})
	require.Equal(t, http.StatusOK, status, "%v", body)
}
