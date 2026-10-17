package handlers_test

// The admin menu leaves the pages about the INSTANCE out for a tenant's
// administrator (web/src/lib/adminNav.ts `only: 'platform'`, task #258): on a
// multi-tenant install such a page could only show that administrator a row
// of refusals. That is honest only while the server refuses them every call
// the page makes when it opens, so this table is the menu's list, each page
// with the first call its view makes. A page whose API grows a tenant form
// fails here, and goes back into the tenant administrator's menu with it.
//
// ⚠ Keep the list in step with web/tests/lib/adminNav.test.ts → "the pages a
// tenant's administrator is not offered", which pins the same page names on
// the menu's side.

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

var platformPages = []struct {
	page string
	path string
}{
	{"search", "/api/admin/search/stats"},
	{"replica", "/api/admin/replica/rules"},
	{"usage", "/api/admin/usage"},
	{"queue", "/api/admin/queue"},
	{"updates", "/api/admin/update"},
	{"appearance", "/api/admin/themes"},
	{"tenancy", "/api/admin/tenancy"},
	{"auth-providers", "/api/admin/auth-providers"},
	{"login-security", "/api/admin/login-security"},
	{"protection", "/api/admin/protection"},
	{"archives", "/api/admin/archives"},
	{"plugins", "/api/admin/plugins"},
	{"external", "/api/admin/external"},
	{"webhooks", "/api/admin/webhooks"},
	{"ai-assistant", "/api/admin/assistant"},
}

func TestAdminMenuPlatformPages_TenantAdminIsRefusedEveryOne(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	_, email, password := seedTenant(t, store, "globex", "admin@globex.test", false)
	testutil.LoginAs(t, srv, client, email, password)

	for _, p := range platformPages {
		t.Run(p.page, func(t *testing.T) {
			status, body := doJSON(t, client, http.MethodGet, srv.URL+p.path, nil)
			require.Equal(t, http.StatusForbidden, status,
				"the menu hides %s from a tenant's administrator, but %s answered them: %v", p.page, p.path, body)
			assert.Equal(t, "supertenant_only", body["error"])
		})
	}
}

// The General page (Admin → Settings → General) READS the settings map,
// which is open to a tenant's administrator (their branding is in it), but
// every key the page WRITES is the instance's: the SMTP server, the thumbnail
// limits, the search settings and the instance defaults. A page where nothing can be saved is
// left out of their menu like the others.
func TestAdminMenuPlatformPages_TenantAdminCannotSaveTheGeneralPage(t *testing.T) {
	srv, client, store := ownershipServer(t, true)
	_, email, password := seedTenant(t, store, "globex", "admin@globex.test", false)
	testutil.LoginAs(t, srv, client, email, password)

	status, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/settings",
		map[string]any{"smtp.host": "mail.example.com"})
	require.Equal(t, http.StatusForbidden, status, "smtp.host: %v", body)

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/tools/thumbnails/settings",
		map[string]any{"svg_max_mb": 4})
	require.Equal(t, http.StatusForbidden, status, "thumbnail limits: %v", body)

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/instance-settings",
		map[string]any{"instance.default_locale": "tr"})
	require.Equal(t, http.StatusForbidden, status, "instance defaults: %v", body)

	// ⚠ The test mail goes out through the INSTANCE's mail server: a tenant's
	// administrator may not use it to send mail as the platform.
	status, body = doJSON(t, client, http.MethodPost, srv.URL+"/api/admin/settings/smtp-test",
		map[string]any{"to": "alice@example.com"})
	require.Equal(t, http.StatusForbidden, status, "smtp test: %v", body)
}

// The other side: the platform's own administrator, and the administrator of
// a single-tenant install, are offered every one of those pages and reach it.
func TestAdminMenuPlatformPages_PlatformAndSingleTenantPass(t *testing.T) {
	for _, multi := range []bool{true, false} {
		srv, client, store := ownershipServer(t, multi)
		email, password := testutil.SeedAdmin(t, store)
		testutil.LoginAs(t, srv, client, email, password)
		for _, p := range platformPages {
			status, body := doJSON(t, client, http.MethodGet, srv.URL+p.path, nil)
			assert.NotEqual(t, http.StatusForbidden, status,
				"multi-tenant=%v: %s refused the platform's administrator: %v", multi, p.path, body)
		}
	}
}

// caller_platform is the capability the menu reads for those pages: true on
// a single-tenant install, true for the supertenant's people, false for a
// tenant's - the scope `requireSupertenant` lets through, asked ahead of time.
func TestCapabilities_CallerPlatform(t *testing.T) {
	platformOf := func(t *testing.T, base string, c *http.Client) any {
		t.Helper()
		res, err := c.Get(base + "/api/files/capabilities")
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		out := map[string]any{}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		return out["caller_platform"]
	}

	t.Run("single-tenant: every caller", func(t *testing.T) {
		srv, client, store := ownershipServer(t, false)
		require.Equal(t, true, platformOf(t, srv.URL, &http.Client{}))
		email, password := testutil.SeedAdmin(t, store)
		testutil.LoginAs(t, srv, client, email, password)
		require.Equal(t, true, platformOf(t, srv.URL, client))
	})

	t.Run("multi-tenant: the supertenant's administrator", func(t *testing.T) {
		srv, client, store := ownershipServer(t, true)
		email, password := testutil.SeedAdmin(t, store)
		testutil.LoginAs(t, srv, client, email, password)
		require.Equal(t, true, platformOf(t, srv.URL, client))
	})

	t.Run("multi-tenant: a tenant's administrator is not", func(t *testing.T) {
		srv, _, store := ownershipServer(t, true)
		_, email, password := seedTenant(t, store, "globex", "admin@globex.test", false)
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar}
		testutil.LoginAs(t, srv, c, email, password)
		require.Equal(t, false, platformOf(t, srv.URL, c))
	})

	t.Run("multi-tenant: nobody signed in is not", func(t *testing.T) {
		srv, _, _ := ownershipServer(t, true)
		require.Equal(t, false, platformOf(t, srv.URL, &http.Client{}))
	})
}
