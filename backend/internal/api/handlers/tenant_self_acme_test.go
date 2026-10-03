package handlers_test

// The tenant screen says what filex's own ACME did for each own domain
// (FILEX_TLS_MODE=acme, docs/TENANT-ADMIN.md "TLS"): nothing yet, obtained
// until when, or not obtained with the authority's reason. Before this the
// certificate cell said "By filex (ACME)" over a domain whose certificate
// could not be obtained (measured against Pebble, e2e/realenv).

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/tenantdomain"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestTenantSelf_SaysWhatFilexsOwnACMEDid(t *testing.T) {
	db.SetTenantDomain("tenants.files.example")
	t.Cleanup(func() { db.SetTenantDomain("") })
	dns := &tableDNS{m: map[string]string{"files.acme.example": "acme.tenants.files.example"}}
	status := &tenantdomain.ACMEStatus{}
	srv, _, store := testutil.NewTestServerWith(t, func(c *config.Config) {
		c.MultiTenant = true
		c.SecretKey = liveTestKey
		c.TenantDomain = "tenants.files.example"
		c.TLS.Mode = config.TLSModeACME
	}, func(d *api.Deps) {
		d.Domains = &tenantdomain.Service{Store: d.Store, Resolver: dns, PlatformHosts: []string{"test.local"}, ACME: status}
	})
	seedTenant(t, store, "acme", "admin@acme.test", false)
	admin := tenantClient(t, srv, "admin@acme.test")

	code, raw := doReq(t, admin, http.MethodPost, srv.URL+"/api/admin/tenant/domains", map[string]any{"domain": "files.acme.example"})
	require.Equal(t, http.StatusCreated, code, string(raw))

	type row struct {
		Domain string                   `json:"domain"`
		ACME   *tenantdomain.ACMEResult `json:"acme"`
	}
	read := func() row {
		t.Helper()
		code, raw := doReq(t, admin, http.MethodGet, srv.URL+"/api/admin/tenant", nil)
		require.Equal(t, http.StatusOK, code, string(raw))
		var v struct {
			TLSMode string `json:"tls_mode"`
			Domains []row  `json:"domains"`
		}
		require.NoError(t, json.Unmarshal(raw, &v))
		require.Equal(t, "acme", v.TLSMode)
		require.Len(t, v.Domains, 1)
		require.NotNil(t, v.Domains[0].ACME, "every own domain under filex's own ACME says what it did: %s", raw)
		return v.Domains[0]
	}

	assert.Equal(t, tenantdomain.ACMENone, read().ACME.State, "no handshake asked for it yet")

	status.Problem("files.acme.example", "DNS problem: NXDOMAIN looking up A for files.acme.example")
	status.Failed("files.acme.example", errors.New("no viable challenge type found"))
	r := read()
	assert.Equal(t, tenantdomain.ACMEFailed, r.ACME.State)
	assert.Equal(t, "DNS problem: NXDOMAIN looking up A for files.acme.example", r.ACME.Reason)
	assert.NotNil(t, r.ACME.At)

	until := time.Now().Add(90 * 24 * time.Hour).Truncate(time.Second).UTC()
	status.Obtained("files.acme.example", until)
	r = read()
	assert.Equal(t, tenantdomain.ACMEObtained, r.ACME.State)
	require.NotNil(t, r.ACME.NotAfter)
	assert.True(t, r.ACME.NotAfter.Equal(until))
}
