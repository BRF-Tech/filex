package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// The drivers register themselves in init(); the server binary imports
	// them in server.go, this test binary has to say so itself.
	_ "github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	_ "github.com/brf-tech/filex/backend/internal/auth/drivers/ldap"
	_ "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	_ "github.com/brf-tech/filex/backend/internal/auth/drivers/proxyheader"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// "Test now" on an identity provider was a stub that answered OK for
// anything (`V0.1 stub — drivers self-test on Init`): an LDAP entry with its
// address and base DN empty got "Provider OK" (release-candidate sweep,
// 2026-09-21). It now tests the configuration on the screen, and a provider
// with nothing to reach says it cannot be tested instead of pretending.
func TestAuthProviders_TestIsRealAndSaysWhatFailed(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	type result struct {
		Testable bool `json:"testable"`
		OK       bool `json:"ok"`
		Checks   []struct {
			ID     string            `json:"id"`
			Status string            `json:"status"`
			Params map[string]string `json:"params"`
		} `json:"checks"`
	}
	test := func(name string, draft map[string]any) result {
		status, raw := doReq(t, client, http.MethodPost, srv.URL+"/api/admin/auth-providers/"+name+"/test", draft)
		require.Equal(t, http.StatusOK, status, string(raw))
		var r result
		require.NoError(t, json.Unmarshal(raw, &r), string(raw))
		return r
	}

	// Empty required fields: not OK, and the fields are named.
	r := test("ldap", map[string]any{"url": "", "base_dn": ""})
	require.True(t, r.Testable)
	assert.False(t, r.OK, "an LDAP entry with no address must not pass")
	require.NotEmpty(t, r.Checks)
	assert.Equal(t, "required", r.Checks[0].ID)
	assert.Equal(t, "fail", r.Checks[0].Status)
	assert.Equal(t, "url,base_dn", r.Checks[0].Params["fields"])

	// The redirect address defaults to <public URL>/api/auth/oidc/callback
	// (as it does in the environment), so it is not among the missing.
	r = test("oidc", map[string]any{})
	assert.False(t, r.OK)
	assert.Equal(t, "issuer,client_id", r.Checks[0].Params["fields"])

	// A directory that is not there: the connect step fails, by name.
	r = test("ldap", map[string]any{"url": "ldap://127.0.0.1:1", "base_dn": "dc=example,dc=com"})
	assert.False(t, r.OK)
	last := r.Checks[len(r.Checks)-1]
	assert.Equal(t, "connect", last.ID)
	assert.Equal(t, "fail", last.Status)

	// Local accounts have nothing to reach: not testable, and not "OK".
	r = test("local", nil)
	assert.False(t, r.Testable)
	assert.False(t, r.OK)

	// The list tells the panel where to offer the button at all.
	status, raw := doReq(t, client, http.MethodGet, srv.URL+"/api/admin/auth-providers", nil)
	require.Equal(t, http.StatusOK, status)
	var list struct {
		Providers []struct {
			Name     string `json:"name"`
			Testable bool   `json:"testable"`
		} `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))
	testable := map[string]bool{}
	for _, p := range list.Providers {
		testable[p.Name] = p.Testable
	}
	assert.True(t, testable["ldap"])
	assert.True(t, testable["oidc"])
	assert.True(t, testable["proxy-header"])
	assert.False(t, testable["local"])
	assert.False(t, testable["api-token"])
}
