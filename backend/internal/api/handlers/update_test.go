package handlers_test

// The update endpoints must stay safe in their most boring configuration: no
// updater wired at all. A nil service is the normal state for an install that
// turned checking off, and the admin page still calls these — so they answer a
// describable status instead of panicking or 404ing.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/update"
)

func TestUpdateStatus_NoServiceIsDescribedNotBroken(t *testing.T) {
	h := handlers.NewUpdate(nil)

	rec := httptest.NewRecorder()
	h.Status(rec, httptest.NewRequest(http.MethodGet, "/api/admin/update", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, false, body["enabled"])
	assert.Equal(t, string(update.ActionNone), body["action"])
	assert.NotEmpty(t, body["current"], "the running version is always reported")
	assert.Contains(t, body["reason"], "disabled")
}

func TestUpdateApply_WithoutServiceIs409NotServerError(t *testing.T) {
	// 409 says "this cannot succeed in this configuration", which is true and
	// permanent. A 5xx would invite a retry loop.
	h := handlers.NewUpdate(nil)
	rec := httptest.NewRecorder()
	h.Apply(rec, httptest.NewRequest(http.MethodPost, "/api/admin/update/apply", nil))
	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestUpdateStatus_DisabledServiceStillReportsMode(t *testing.T) {
	// Checking off (policy=off) must not hide WHICH kind of install this is —
	// the page uses it to decide between "upgrade now" and instructions.
	svc := update.New(update.Config{Enabled: false, Policy: update.PolicyOff, CurrentVersion: "v0.7.6"})
	h := handlers.NewUpdate(svc)

	rec := httptest.NewRecorder()
	h.Status(rec, httptest.NewRequest(http.MethodGet, "/api/admin/update", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, false, body["enabled"])
	assert.Contains(t, []any{"binary", "docker"}, body["mode"])
}

func TestUpdateCheck_DisabledDoesNotReachTheNetwork(t *testing.T) {
	// With checking off, /check must be a no-op that still returns a status:
	// a disabled install should never emit an outbound request because someone
	// clicked a button.
	svc := update.New(update.Config{
		Enabled:        false,
		Policy:         update.PolicyOff,
		CurrentVersion: "v0.7.6",
		ManifestURL:    "http://127.0.0.1:1/never-reachable",
	})
	h := handlers.NewUpdate(svc)

	rec := httptest.NewRecorder()
	h.Check(rec, httptest.NewRequest(http.MethodPost, "/api/admin/update/check", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Empty(t, body["check_error"], "no request was attempted, so there is no error to report")
}
