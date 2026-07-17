package handlers_test

/* kimlik:e3 cloud */

// Cloud-preparation tests (docs/CLOUD.md). The binding contract: with
// FILEX_CLOUD off (default) NOTHING changes — no /api/cloud route exists and
// capabilities carry no cloud field. With the flag on (local test rig only)
// signup provisions a DISABLED tenant through the same provider primitive as
// /api/admin/providers, verify enables it, and billing answers 503 without
// STRIPE_SECRET.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func cloudDo(t *testing.T, client *http.Client, method, url string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rd)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// TestCloud_FlagOff_ZeroBehaviorChange — the default server (flag off) must
// expose NO cloud surface: every /api/cloud path 404s and the capabilities
// payload has no "cloud" key.
func TestCloud_FlagOff_ZeroBehaviorChange(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)

	for _, probe := range []struct{ method, path string }{
		{"GET", "/api/cloud/status"},
		{"GET", "/api/cloud/plans"},
		{"POST", "/api/cloud/signup"},
		{"POST", "/api/cloud/verify"},
		{"POST", "/api/cloud/billing/checkout"},
		{"POST", "/api/cloud/billing/webhook"},
	} {
		resp, _ := cloudDo(t, client, probe.method, srv.URL+probe.path, map[string]string{})
		assert.Equal(t, http.StatusNotFound, resp.StatusCode,
			"%s %s must not exist while FILEX_CLOUD is off", probe.method, probe.path)
	}

	resp, caps := cloudDo(t, client, "GET", srv.URL+"/api/capabilities", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, present := caps["cloud"]
	assert.False(t, present, "capabilities must carry no cloud field while FILEX_CLOUD is off")
}

// TestCloud_SignupVerify_E2E — flag on (test env only): signup provisions a
// DISABLED provider row with the plan snapshot stamped (migration 00021),
// verify flips it enabled. Mailer is unwired in the rig, so the verify token
// comes back in the response (the documented dev fallback).
func TestCloud_SignupVerify_E2E(t *testing.T) {
	plans := `[{"id":"pro","name":"Pro","price_monthly":"9.90 EUR","limits":{"storage_bytes":1024,"max_users":5}}]`
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.Cloud.Enabled = true
		c.Cloud.PlansJSON = plans
		c.Cloud.BaseHost = "filex.test"
	})

	// Flag on → capabilities advertise the surface + status answers.
	resp, caps := cloudDo(t, client, "GET", srv.URL+"/api/capabilities", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, caps, "cloud")

	resp, status := cloudDo(t, client, "GET", srv.URL+"/api/cloud/status", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, true, status["enabled"])
	assert.Equal(t, false, status["stripe_configured"])
	assert.NotContains(t, status, "plans_error")

	resp, plansOut := cloudDo(t, client, "GET", srv.URL+"/api/cloud/plans", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, plansOut["plans"], 1)

	// Validation: bad slug / unknown plan / bad email.
	resp, _ = cloudDo(t, client, "POST", srv.URL+"/api/cloud/signup",
		map[string]string{"email": "a@b.test", "slug": "Bad Slug!"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = cloudDo(t, client, "POST", srv.URL+"/api/cloud/signup",
		map[string]string{"email": "a@b.test", "slug": "acme", "plan": "nope"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp, _ = cloudDo(t, client, "POST", srv.URL+"/api/cloud/signup",
		map[string]string{"email": "not-an-email", "slug": "acme"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Happy path: provision.
	resp, out := cloudDo(t, client, "POST", srv.URL+"/api/cloud/signup",
		map[string]string{"email": "owner@acme.test", "slug": "acme", "name": "Acme Inc", "plan": "pro"})
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	assert.Equal(t, "acme", out["slug"])
	assert.Equal(t, "acme.filex.test", out["host"])
	assert.Equal(t, "pro", out["plan"])
	assert.Equal(t, false, out["mail_sent"])
	token, _ := out["verify_token"].(string)
	require.NotEmpty(t, token, "unwired mailer → token must come back in the response")

	// The tenant is a provider row: disabled until verified, plan stamped.
	p, err := store.GetProviderBySlug(context.Background(), "acme")
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.False(t, p.Enabled, "tenant must stay disabled until e-mail verification")
	assert.Equal(t, "Acme Inc", p.Name)
	plan, limitsJSON, billingRef, err := store.GetProviderPlan(context.Background(), p.ID)
	require.NoError(t, err)
	assert.Equal(t, "pro", plan)
	assert.Contains(t, limitsJSON, `"storage_bytes":1024`)
	assert.Empty(t, billingRef)

	// Duplicate slug → 409.
	resp, _ = cloudDo(t, client, "POST", srv.URL+"/api/cloud/signup",
		map[string]string{"email": "other@acme.test", "slug": "acme", "plan": "pro"})
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	// Verify: bad token → 404; real token → tenant enabled; replay → 404.
	resp, _ = cloudDo(t, client, "POST", srv.URL+"/api/cloud/verify", map[string]string{"token": "wrong"})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, vout := cloudDo(t, client, "POST", srv.URL+"/api/cloud/verify", map[string]string{"token": token})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, true, vout["enabled"])
	p, err = store.GetProviderBySlug(context.Background(), "acme")
	require.NoError(t, err)
	assert.True(t, p.Enabled)
	resp, _ = cloudDo(t, client, "POST", srv.URL+"/api/cloud/verify", map[string]string{"token": token})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "verify tokens are single-use")
}

// TestCloud_Stripe_NotConfigured503 — without STRIPE_SECRET both billing
// endpoints answer 503 "not configured".
func TestCloud_Stripe_NotConfigured503(t *testing.T) {
	srv, client, _ := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.Cloud.Enabled = true
	})
	resp, out := cloudDo(t, client, "POST", srv.URL+"/api/cloud/billing/checkout",
		map[string]string{"plan": "free"})
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Contains(t, out["error"], "not configured")
	resp, _ = cloudDo(t, client, "POST", srv.URL+"/api/cloud/billing/webhook", map[string]string{})
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// TestCloud_BadPlansEnv_SurfacedInStatus — a broken FILEX_CLOUD_PLANS must
// not take the server down: defaults stay active and /status reports it.
func TestCloud_BadPlansEnv_SurfacedInStatus(t *testing.T) {
	srv, client, _ := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.Cloud.Enabled = true
		c.Cloud.PlansJSON = `{definitely not a plan list`
	})
	resp, status := cloudDo(t, client, "GET", srv.URL+"/api/cloud/status", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, status, "plans_error")
	assert.EqualValues(t, 1, status["plans"], "default catalog stays active")
}
