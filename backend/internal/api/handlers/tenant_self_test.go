package handlers_test

// A tenant runs itself (handlers/tenant_self.go, docs/TENANT-ADMIN.md):
//
//   - its administrator adds their OWN OIDC or LDAP and no other kind; it is
//     the tenant's alone (another tenant's administrator gets 404), the
//     operator sees it with its owner, and a file on the server (`ca_file`)
//     is never one of its fields;
//   - allowing insecure and internal-network providers is the operator's
//     switch, audited;
//   - an own domain is pending until its CNAME points at the tenant's
//     platform subdomain, then routes; one tenant at a time;
//   - the proxy's certificate questions are answered to the proxy itself only.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/tenantdomain"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type tableDNS struct {
	mu sync.Mutex
	m  map[string]string
}

func (d *tableDNS) set(host, target string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.m[host] = target
}

func (d *tableDNS) LookupCNAME(_ context.Context, host string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if v, ok := d.m[host]; ok {
		return v + ".", nil
	}
	return "", &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// selfServer is a multi-tenant server with a tenant domain, a fake DNS and a
// secret key; acme and beta each have an administrator.
func selfServer(t *testing.T) (*httptest.Server, db.Store, *tableDNS, *model.Provider, *model.Provider) {
	t.Helper()
	db.SetTenantDomain("tenants.files.example")
	t.Cleanup(func() { db.SetTenantDomain("") })
	dns := &tableDNS{m: map[string]string{
		"acme.tenants.files.example": "acme.tenants.files.example",
		"beta.tenants.files.example": "beta.tenants.files.example",
	}}
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.MultiTenant = true
		c.SecretKey = liveTestKey
		c.TenantDomain = "tenants.files.example"
	}, func(d *api.Deps) {
		d.Domains = &tenantdomain.Service{Store: d.Store, Resolver: dns, PlatformHosts: []string{"test.local"}}
	})
	ctx := context.Background()
	acmeID, _, _ := seedTenant(t, store, "acme", "admin@acme.test", false)
	betaID, _, _ := seedTenant(t, store, "beta", "admin@beta.test", false)
	acme, err := store.GetProvider(ctx, acmeID)
	require.NoError(t, err)
	beta, err := store.GetProvider(ctx, betaID)
	require.NoError(t, err)
	return srv, store, dns, acme, beta
}

// tenantClient is a browser signed in as one of seedTenant's administrators.
func tenantClient(t *testing.T, srv *httptest.Server, email string) *http.Client {
	t.Helper()
	c := browser(srv.URL)
	testutil.LoginAs(t, srv, c, email, "TenantAdmin!1")
	return c
}

func TestTenantSelf_OwnProvidersAreTheTenantsAlone(t *testing.T) {
	srv, store, _, acme, _ := selfServer(t)
	base := srv.URL
	ctx := context.Background()
	acmeAdmin := tenantClient(t, srv, "admin@acme.test")
	betaAdmin := tenantClient(t, srv, "admin@beta.test")

	status, raw := doReq(t, acmeAdmin, http.MethodPost, base+"/api/admin/tenant/auth-providers", map[string]any{"driver": "pam"})
	assert.Equal(t, http.StatusBadRequest, status, "a tenant adds an OIDC or an LDAP, never an operating-system provider")
	assert.Contains(t, string(raw), "driver_invalid")

	status, raw = doReq(t, acmeAdmin, http.MethodPost, base+"/api/admin/tenant/auth-providers", map[string]any{
		"driver": "ldap", "label": "Acme directory", "enabled": false,
		"config": map[string]any{"url": "ldaps://dir.acme.example", "base_dn": "dc=acme", "ca_file": "/etc/shadow"},
	})
	require.Equal(t, http.StatusCreated, status, string(raw))

	row, err := store.GetAuthInstanceBySlug(ctx, "acme-ldap")
	require.NoError(t, err)
	require.NotNil(t, row, "named after the tenant's realm")
	require.NotNil(t, row.OwnerProviderID)
	assert.Equal(t, acme.ID, *row.OwnerProviderID)
	assert.Equal(t, model.AuthOriginTenant, row.Origin)
	assert.NotContains(t, row.ConfigJSON, "ca_file", "a file on the server is never a tenant's field")
	assert.Contains(t, row.ConfigJSON, "ldaps://dir.acme.example")

	// The tenant's overview has it, with its fields (no ca_file); beta's does not.
	status, raw = doReq(t, acmeAdmin, http.MethodGet, base+"/api/admin/tenant/", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	var ov map[string]any
	require.NoError(t, json.Unmarshal(raw, &ov))
	provs := ov["providers"].([]any)
	require.Len(t, provs, 1)
	assert.Equal(t, "acme-ldap", provs[0].(map[string]any)["name"])
	assert.NotContains(t, string(raw), `"ca_file"`)
	assert.Equal(t, "acme.tenants.files.example", ov["platform_subdomain"])

	status, raw = doReq(t, betaAdmin, http.MethodGet, base+"/api/admin/tenant/", nil)
	require.Equal(t, http.StatusOK, status)
	assert.NotContains(t, string(raw), "acme-ldap")
	status, _ = doReq(t, betaAdmin, http.MethodPatch, base+"/api/admin/tenant/auth-providers/acme-ldap", map[string]any{"enabled": false})
	assert.Equal(t, http.StatusNotFound, status, "another tenant's provider is unknown")
	status, _ = doReq(t, betaAdmin, http.MethodGet, base+"/api/admin/tenant/?tenant="+itoa(acme.ID), nil)
	assert.Equal(t, http.StatusNotFound, status, "a tenant's administrator names no other tenant")

	// Guarded: plain ldap:// to an internal address does not start.
	status, raw = doReq(t, acmeAdmin, http.MethodPatch, base+"/api/admin/tenant/auth-providers/acme-ldap", map[string]any{
		"enabled": true, "confirm_failed_test": true, "config": map[string]any{"url": "ldap://127.0.0.1:389"},
	})
	require.Equal(t, http.StatusOK, status, string(raw))
	status, raw = doReq(t, acmeAdmin, http.MethodGet, base+"/api/admin/tenant/", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(raw), "ldaps:// or StartTLS", "a tenant's own directory must be encrypted")

	// The operator sees it, with its owner, among every provider.
	opClient := tenantClient(t, srv, operatorEmail(t, store))
	_, list := listProviders(t, opClient, base)
	require.Contains(t, list, "acme-ldap")
	assert.Equal(t, "tenant", list["acme-ldap"]["origin"])
	assert.Equal(t, float64(acme.ID), list["acme-ldap"]["owner_provider_id"])
	fields, err := json.Marshal(list["acme-ldap"]["fields"])
	require.NoError(t, err)
	assert.Contains(t, string(fields), `"ca_pem"`)
	assert.NotContains(t, string(fields), `"ca_file"`, "the operator is offered a tenant's own fields too")
}

func TestTenantSelf_InsecureIsTheOperatorsSwitch(t *testing.T) {
	srv, store, _, acme, _ := selfServer(t)
	base := srv.URL
	ctx := context.Background()
	acmeAdmin := tenantClient(t, srv, "admin@acme.test")
	status, raw := doReq(t, acmeAdmin, http.MethodPut, base+"/api/admin/tenant/insecure", map[string]any{"allow": true})
	assert.Equal(t, http.StatusForbidden, status, string(raw))
	p, _ := store.GetProvider(ctx, acme.ID)
	assert.False(t, p.AllowInsecureAuth)

	op := tenantClient(t, srv, operatorEmail(t, store))
	status, raw = doReq(t, op, http.MethodPut, base+"/api/admin/tenant/insecure?tenant="+itoa(acme.ID), map[string]any{"allow": true})
	require.Equal(t, http.StatusOK, status, string(raw))
	p, _ = store.GetProvider(ctx, acme.ID)
	assert.True(t, p.AllowInsecureAuth)
	rows, err := store.ListAuditRecent(ctx, 50)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		if r.Action == "tenant.insecure_auth_set" {
			found = true
			assert.Equal(t, true, r.Metadata["allow_after"])
		}
	}
	assert.True(t, found, "the switch is audited")
}

func TestTenantSelf_DomainsProvenByCNAMEOneTenantAtATime(t *testing.T) {
	srv, store, dns, acme, _ := selfServer(t)
	base := srv.URL
	ctx := context.Background()
	acmeAdmin := tenantClient(t, srv, "admin@acme.test")
	betaAdmin := tenantClient(t, srv, "admin@beta.test")

	status, raw := doReq(t, acmeAdmin, http.MethodPost, base+"/api/admin/tenant/domains", map[string]any{"domain": "Files.Acme.Example"})
	require.Equal(t, http.StatusCreated, status, string(raw))
	var d map[string]any
	require.NoError(t, json.Unmarshal(raw, &d))
	assert.Equal(t, "files.acme.example", d["domain"])
	assert.Equal(t, "pending", d["status"])
	assert.Equal(t, "acme.tenants.files.example", d["target"])
	// What the check found, as a code and its names for the screen to say.
	assert.Equal(t, "no_record", d["last_error_code"])
	assert.Equal(t, map[string]any{"domain": "files.acme.example"}, d["last_error_params"])
	id := itoa(int64(d["id"].(float64)))

	status, raw = doReq(t, betaAdmin, http.MethodPost, base+"/api/admin/tenant/domains", map[string]any{"domain": "files.acme.example"})
	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, string(raw), "domain_taken")
	assert.NotContains(t, string(raw), "acme", "never says whose")

	status, _ = doReq(t, betaAdmin, http.MethodPost, base+"/api/admin/tenant/domains/"+id+"/check", nil)
	assert.Equal(t, http.StatusNotFound, status, "another tenant's domain is unknown")

	dns.set("files.acme.example", "acme.tenants.files.example")
	status, raw = doReq(t, acmeAdmin, http.MethodPost, base+"/api/admin/tenant/domains/"+id+"/check", nil)
	require.Equal(t, http.StatusOK, status, string(raw))
	assert.Contains(t, string(raw), `"status":"active"`)
	p, err := store.GetProviderByHost(ctx, "files.acme.example")
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.Equal(t, acme.ID, p.ID)

	// The proxy may certify it - asked by the proxy itself, not forwarded.
	resp, err := http.Get(base + "/api/tls/ask?domain=files.acme.example")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp, err = http.Get(base + "/api/tls/ask?domain=acme.tenants.files.example")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "a tenant's platform subdomain is certified too")
	resp, err = http.Get(base + "/api/tls/ask?domain=stranger.example")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	req, _ := http.NewRequest(http.MethodGet, base+"/api/tls/ask?domain=files.acme.example", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "a stranger's request the proxy forwards learns nothing")
	resp, err = http.Get(base + "/api/tls/certificate?server_name=files.acme.example")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode, "no certificate of its own: the proxy issues one")

	status, raw = doReq(t, acmeAdmin, http.MethodPut, base+"/api/admin/tenant/domains/"+id+"/certificate", map[string]any{"cert_pem": "x", "key_pem": "y"})
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, string(raw), "certificate_invalid")

	status, _ = doReq(t, acmeAdmin, http.MethodDelete, base+"/api/admin/tenant/domains/"+id, nil)
	require.Equal(t, http.StatusOK, status)
	p, err = store.GetProviderByHost(ctx, "files.acme.example")
	require.NoError(t, err)
	assert.Nil(t, p)
}

// A tenant's session cookie on its own domain is that address's alone: the
// Domain derived from its `host` (.acme.test) is one the browser refuses on
// files.acme.example, and with it the whole sign-in.
func TestTenantSelf_OwnDomainSessionCookieIsTheAddresss(t *testing.T) {
	srv, store, dns, acme, _ := selfServer(t)
	ctx := context.Background()
	acme.Host = "files.acme.test"
	require.NoError(t, store.UpdateProvider(ctx, acme))
	acmeAdmin := tenantClient(t, srv, "admin@acme.test")
	status, raw := doReq(t, acmeAdmin, http.MethodPost, srv.URL+"/api/admin/tenant/domains", map[string]any{"domain": "files.acme.example"})
	require.Equal(t, http.StatusCreated, status, string(raw))
	dns.set("files.acme.example", "acme.tenants.files.example")
	var d map[string]any
	require.NoError(t, json.Unmarshal(raw, &d))
	status, raw = doReq(t, acmeAdmin, http.MethodPost, srv.URL+"/api/admin/tenant/domains/"+itoa(int64(d["id"].(float64)))+"/check", nil)
	require.Equal(t, http.StatusOK, status, string(raw))

	cookieOn := func(host string) string {
		t.Helper()
		body := `{"email":"admin@acme.test","password":"TenantAdmin!1"}`
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", strings.NewReader(body))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, host)
		for _, c := range resp.Cookies() {
			if c.Name == "filex_session" {
				return c.Domain
			}
		}
		t.Fatalf("no session cookie on %s", host)
		return ""
	}
	assert.Equal(t, "acme.test", cookieOn("files.acme.test"), "on its host: the Domain derived from it, as before")
	assert.Equal(t, "", cookieOn("files.acme.example"), "on its own domain: host-only")
	assert.Equal(t, "", cookieOn("acme.tenants.files.example"), "on its platform subdomain: host-only")
}

// operatorEmail seeds the platform's administrator and answers its email.
func operatorEmail(t *testing.T, store db.Store) string {
	t.Helper()
	ctx := context.Background()
	if u, err := store.GetUserByEmail(ctx, "operator@platform.test"); err == nil && u != nil {
		return u.Email
	}
	_, _, _ = seedTenant(t, store, "", "operator@platform.test", true)
	return "operator@platform.test"
}
