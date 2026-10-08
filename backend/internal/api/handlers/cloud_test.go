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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

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

	// Validation: bad slug / bad email. (A plan in the body is not
	// validated: it is ignored - TestCloud_SignupStartsOnTheSignupPlan.)
	resp, _ = cloudDo(t, client, "POST", srv.URL+"/api/cloud/signup",
		map[string]string{"email": "a@b.test", "slug": "Bad Slug!"})
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

// TestCloud_SignupStartsOnTheSignupPlan — a signup gets the catalogue's first
// plan nobody pays for, whatever its body asks for. A paid plan is reached
// only through a payment Stripe confirms.
func TestCloud_SignupStartsOnTheSignupPlan(t *testing.T) {
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.Cloud.Enabled = true
		c.Cloud.PlansJSON = `[{"id":"free","name":"Free","limits":{"storage_bytes":1024}},` +
			`{"id":"pro","name":"Pro","stripe_price_id":"price_pro","limits":{"storage_bytes":1048576}}]`
	})
	resp, out := cloudDo(t, client, "POST", srv.URL+"/api/cloud/signup",
		map[string]string{"email": "owner@paid.test", "slug": "paid-ask", "plan": "pro"})
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "%v", out)
	assert.Equal(t, "free", out["plan"])
	p, err := store.GetProviderBySlug(context.Background(), "paid-ask")
	require.NoError(t, err)
	require.NotNil(t, p)
	plan, _, _, err := store.GetProviderPlan(context.Background(), p.ID)
	require.NoError(t, err)
	assert.Equal(t, "free", plan, "the signup was given the paid plan it asked for")
}

// TestCloud_TheWebhookMovesAPlanOnlyOnAVerifiedEvent — the webhook is the one
// door that changes a plan, and only for an event signed with the endpoint's
// secret.
func TestCloud_TheWebhookMovesAPlanOnlyOnAVerifiedEvent(t *testing.T) {
	const whsec = "whsec_cloud_test"
	srv, client, store := testutil.NewTestServerCfg(t, func(c *config.Config) {
		c.Cloud.Enabled = true
		c.Cloud.StripeSecret = "sk_test_cloud"
		c.Cloud.StripeWebhookSecret = whsec
		c.Cloud.PlansJSON = `[{"id":"free","name":"Free"},{"id":"pro","name":"Pro","stripe_price_id":"price_pro","limits":{"max_users":25}}]`
	})
	resp, out := cloudDo(t, client, "POST", srv.URL+"/api/cloud/signup",
		map[string]string{"email": "owner@hook.test", "slug": "hook"})
	require.Equal(t, http.StatusAccepted, resp.StatusCode, "%v", out)
	id := int64(out["tenant_id"].(float64))

	event := []byte(fmt.Sprintf(`{"id":"evt_1","type":"checkout.session.completed","data":{"object":`+
		`{"client_reference_id":"%d","subscription":"sub_9","payment_status":"paid","metadata":{"plan":"pro"}}}}`, id))
	post := func(sig string) int {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/cloud/billing/webhook", bytes.NewReader(event))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if sig != "" {
			req.Header.Set("Stripe-Signature", sig)
		}
		r, err := client.Do(req)
		require.NoError(t, err)
		_ = r.Body.Close()
		return r.StatusCode
	}
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(whsec))
	mac.Write([]byte(stamp + "."))
	mac.Write(event)
	good := "t=" + stamp + ",v1=" + hex.EncodeToString(mac.Sum(nil))

	assert.Equal(t, http.StatusBadRequest, post(""), "an unsigned event")
	assert.Equal(t, http.StatusBadRequest, post("t="+stamp+",v1="+strings.Repeat("0", 64)), "a forged signature")
	plan, _, _, err := store.GetProviderPlan(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "free", plan, "an unverified event moved the plan")

	assert.Equal(t, http.StatusOK, post(good))
	plan, _, ref, err := store.GetProviderPlan(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, "pro", plan)
	assert.Equal(t, "sub_9", ref)
}
