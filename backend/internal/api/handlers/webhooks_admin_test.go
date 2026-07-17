package handlers_test

// Webhook v2 admin API tests — target CRUD (secret always masked), the
// admin-only gate, and the synchronous test-fire endpoint, exercised
// through the real router so the auth middleware chain is covered.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func whDoJSON(t *testing.T, client *http.Client, method, url string, payload any) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		require.NoError(t, err)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, body)
	require.NoError(t, err)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// TestAdminWebhooks_RequiresAdmin — the whole namespace sits behind the
// admin group: anonymous callers never reach the handler.
func TestAdminWebhooks_RequiresAdmin(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Get(srv.URL + "/api/admin/webhooks")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// TestAdminWebhooks_CRUD_SecretMasked — full lifecycle: create → list →
// patch → delete, with the secret never appearing in any response.
func TestAdminWebhooks_CRUD_SecretMasked(t *testing.T) {
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.Notify = notify.New(d.Store, notify.Config{HTTPTimeout: time.Second, RetryBackoffs: []time.Duration{}})
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	// Create.
	resp := whDoJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks", map[string]any{
		"name":   "ops-hook",
		"url":    "https://example.com/hook",
		"secret": "topsecret",
		"events": []string{"file.uploaded", " share.created ", ""},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.NotContains(t, string(raw), "topsecret", "secret must never round-trip")
	var created struct {
		ID        int64    `json:"id"`
		Name      string   `json:"name"`
		SecretSet bool     `json:"secret_set"`
		Events    []string `json:"events"`
		Enabled   bool     `json:"enabled"`
	}
	require.NoError(t, json.Unmarshal(raw, &created))
	assert.Equal(t, "ops-hook", created.Name)
	assert.True(t, created.SecretSet)
	assert.True(t, created.Enabled, "enabled defaults to true")
	assert.Equal(t, []string{"file.uploaded", "share.created"}, created.Events, "events sanitized")

	// Create rejects bad URLs.
	resp = whDoJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks", map[string]any{
		"name": "bad", "url": "ftp://nope",
	})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()

	// List.
	resp = whDoJSON(t, client, http.MethodGet, srv.URL+"/api/admin/webhooks", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.NotContains(t, string(raw), "topsecret")
	var list struct {
		Items []struct {
			ID        int64 `json:"id"`
			SecretSet bool  `json:"secret_set"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(raw, &list))
	require.Len(t, list.Items, 1)
	assert.True(t, list.Items[0].SecretSet)

	// Patch: rename + disable + clear the secret (explicit empty string).
	resp = whDoJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/webhooks/1", map[string]any{
		"name":    "renamed",
		"enabled": false,
		"secret":  "",
		"events":  []string{},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var patched struct {
		Name      string   `json:"name"`
		SecretSet bool     `json:"secret_set"`
		Enabled   bool     `json:"enabled"`
		Events    []string `json:"events"`
	}
	testutil.ReadJSON(t, resp, &patched)
	resp.Body.Close()
	assert.Equal(t, "renamed", patched.Name)
	assert.False(t, patched.SecretSet, "empty secret clears")
	assert.False(t, patched.Enabled)
	assert.Empty(t, patched.Events)

	// Patch keeps the secret when the field is absent.
	resp = whDoJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/webhooks/1", map[string]any{
		"secret": "again",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = whDoJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/webhooks/1", map[string]any{
		"name": "renamed-2",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var patched2 struct {
		Name      string `json:"name"`
		SecretSet bool   `json:"secret_set"`
	}
	testutil.ReadJSON(t, resp, &patched2)
	resp.Body.Close()
	assert.Equal(t, "renamed-2", patched2.Name)
	assert.True(t, patched2.SecretSet, "absent secret field keeps the stored secret")

	// Delete.
	resp = whDoJSON(t, client, http.MethodDelete, srv.URL+"/api/admin/webhooks/1", nil)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()
	resp = whDoJSON(t, client, http.MethodDelete, srv.URL+"/api/admin/webhooks/1", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	resp = whDoJSON(t, client, http.MethodGet, srv.URL+"/api/admin/webhooks", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var empty struct {
		Items []any `json:"items"`
	}
	testutil.ReadJSON(t, resp, &empty)
	resp.Body.Close()
	assert.Empty(t, empty.Items)
}

// TestAdminWebhooks_TestFire — POST /{id}/test delivers a sample
// payload with the contract headers and reports the outcome inline.
func TestAdminWebhooks_TestFire(t *testing.T) {
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.Notify = notify.New(d.Store, notify.Config{HTTPTimeout: 2 * time.Second, RetryBackoffs: []time.Duration{}})
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	var (
		hits  atomic.Int32
		event string
		sig   string
		body  []byte
	)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		event = r.Header.Get("X-Filex-Event")
		sig = r.Header.Get("X-Filex-Signature")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	resp := whDoJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks", map[string]any{
		"name": "fire", "url": hook.URL, "secret": "sig-me",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created struct {
		ID int64 `json:"id"`
	}
	testutil.ReadJSON(t, resp, &created)
	resp.Body.Close()

	resp = whDoJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks/1/test", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var fire struct {
		OK     bool `json:"ok"`
		Result struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"result"`
	}
	testutil.ReadJSON(t, resp, &fire)
	resp.Body.Close()

	assert.True(t, fire.OK)
	assert.Equal(t, "sent", fire.Result.Status)
	assert.Equal(t, int32(1), hits.Load())
	assert.Equal(t, "webhook_test", event)
	assert.Equal(t, notify.Signature("sig-me", body), sig)
	assert.True(t, strings.Contains(string(body), `"event":"webhook_test"`))

	// Fire against an id that doesn't exist → 404.
	resp = whDoJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks/999/test", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// The list now carries the last_status of the fired target.
	resp = whDoJSON(t, client, http.MethodGet, srv.URL+"/api/admin/webhooks", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var list struct {
		Items []struct {
			LastStatus *struct {
				Status string `json:"status"`
			} `json:"last_status"`
		} `json:"items"`
	}
	testutil.ReadJSON(t, resp, &list)
	resp.Body.Close()
	require.Len(t, list.Items, 1)
	require.NotNil(t, list.Items[0].LastStatus)
	assert.Equal(t, "sent", list.Items[0].LastStatus.Status)
}

// TestAdminWebhooks_PersistedLastDelivery — the list must carry the
// persisted last-delivery columns (migration 00019): HTTP status code,
// error message and timestamp, surviving across service instances (the
// in-memory map only lives for the process).
func TestAdminWebhooks_PersistedLastDelivery(t *testing.T) {
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		d.Notify = notify.New(d.Store, notify.Config{HTTPTimeout: 2 * time.Second, RetryBackoffs: []time.Duration{}})
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp := whDoJSON(t, client, http.MethodPost, srv.URL+"/api/admin/webhooks", map[string]any{
		"name": "persisted", "url": "https://example.com/hook",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created struct {
		ID int64 `json:"id"`
	}
	testutil.ReadJSON(t, resp, &created)
	resp.Body.Close()

	// Fresh target → no last-delivery fields at all.
	resp = whDoJSON(t, client, http.MethodGet, srv.URL+"/api/admin/webhooks", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	type row struct {
		LastHTTPStatus *int    `json:"last_http_status"`
		LastError      *string `json:"last_error"`
		LastDeliveryAt *string `json:"last_delivery_at"`
	}
	var list struct {
		Items []row `json:"items"`
	}
	testutil.ReadJSON(t, resp, &list)
	resp.Body.Close()
	require.Len(t, list.Items, 1)
	assert.Nil(t, list.Items[0].LastHTTPStatus)
	assert.Nil(t, list.Items[0].LastError)
	assert.Nil(t, list.Items[0].LastDeliveryAt)

	// Simulate a recorded failed delivery straight through the store —
	// exactly what notify.recordTargetStatus persists.
	require.NoError(t, store.UpdateWebhookTargetDelivery(
		context.Background(), created.ID, 503, "HTTP 503", time.Now().UTC()))

	resp = whDoJSON(t, client, http.MethodGet, srv.URL+"/api/admin/webhooks", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// Fresh struct per read: unmarshal into a reused one would keep stale
	// pointers for omitted (omitempty) fields.
	var failed struct {
		Items []row `json:"items"`
	}
	testutil.ReadJSON(t, resp, &failed)
	resp.Body.Close()
	require.Len(t, failed.Items, 1)
	require.NotNil(t, failed.Items[0].LastHTTPStatus)
	assert.Equal(t, 503, *failed.Items[0].LastHTTPStatus)
	require.NotNil(t, failed.Items[0].LastError)
	assert.Equal(t, "HTTP 503", *failed.Items[0].LastError)
	require.NotNil(t, failed.Items[0].LastDeliveryAt)

	// A success overwrites the failure and clears the error.
	require.NoError(t, store.UpdateWebhookTargetDelivery(
		context.Background(), created.ID, 200, "", time.Now().UTC()))

	resp = whDoJSON(t, client, http.MethodGet, srv.URL+"/api/admin/webhooks", nil)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var succeeded struct {
		Items []row `json:"items"`
	}
	testutil.ReadJSON(t, resp, &succeeded)
	resp.Body.Close()
	require.Len(t, succeeded.Items, 1)
	require.NotNil(t, succeeded.Items[0].LastHTTPStatus)
	assert.Equal(t, 200, *succeeded.Items[0].LastHTTPStatus)
	assert.Nil(t, succeeded.Items[0].LastError, "success clears last_error")
	require.NotNil(t, succeeded.Items[0].LastDeliveryAt)
}
