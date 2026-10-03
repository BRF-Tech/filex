package handlers_test

// Issue #104: a storage plugin's log is served like an app plugin's -
// `GET /api/admin/plugins/{id}/logs?after=<seq>` → `{lines, next}` - so the
// admin panel's one log panel reads both. (What lands in it is pinned in
// internal/plugin manager_logs_test.go.)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestStoragePluginLogs_AnswerTheAppPluginShape(t *testing.T) {
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	m, err := plugin.New(plugin.Options{Store: store, Dir: t.TempDir(), SecretKey: "test-secret-key"})
	require.NoError(t, err)
	t.Cleanup(m.Shutdown)
	row, err := store.CreatePlugin(ctx, &model.Plugin{Name: "acme", Kind: model.PluginKindRemote, Address: "http://127.0.0.1:9"})
	require.NoError(t, err)

	r := chi.NewRouter()
	h := handlers.NewPlugins(m, false)
	r.Get("/api/admin/plugins/{id}/logs", h.Logs)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/api/admin/plugins/%d/logs?after=0", row.ID), nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Contains(t, body, "lines")
	assert.Contains(t, body, "next")
	_, isList := body["lines"].([]any)
	assert.True(t, isList, "lines is a list, never null: %s", rec.Body.String())

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("GET", fmt.Sprintf("/api/admin/plugins/%d/logs", row.ID+1000), nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
