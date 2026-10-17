package handlers_test

// GET/PATCH /api/admin/search/settings (task #261): the search behaviours an
// admin edits, as settings-table rows - defaults and bounds, refusal of a
// number out of range (never a clamp), a memory percent applied the moment it
// is saved, and the admin gate. The seeding (the row winning over the
// variable) is internal/search settings_test.go.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type searchSettingsBody struct {
	Content           bool   `json:"content"`
	ContentMaxMB      int    `json:"content_max_mb"`
	ContentMaxMBMin   int    `json:"content_max_mb_min"`
	ContentMaxMBMax   int    `json:"content_max_mb_max"`
	MemoryPercent     int    `json:"memory_percent"`
	MemoryPercentMin  int    `json:"memory_percent_min"`
	MemoryPercentMax  int    `json:"memory_percent_max"`
	MemoryBudgetBytes uint64 `json:"memory_budget_bytes"`
	MemoryGivenBytes  uint64 `json:"memory_given_bytes"`
	AutoRebuild       bool   `json:"auto_rebuild"`
	IndexEnabled      bool   `json:"index_enabled"`
	Field             string `json:"field"`
	Message           string `json:"message"`
}

func searchSettingsServer(t *testing.T) (*search.Index, func(method string, body any) (int, searchSettingsBody)) {
	t.Helper()
	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) { d.Index = idx })
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	call := func(method string, body any) (int, searchSettingsBody) {
		t.Helper()
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, err := http.NewRequest(method, srv.URL+"/api/admin/search/settings", rd)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var out searchSettingsBody
		testutil.ReadJSON(t, resp, &out)
		return resp.StatusCode, out
	}
	return idx, call
}

func TestSearchSettings_DefaultsAndBounds(t *testing.T) {
	_, call := searchSettingsServer(t)
	code, got := call(http.MethodGet, nil)
	require.Equal(t, http.StatusOK, code)
	assert.True(t, got.Content)
	assert.Equal(t, search.DefaultContentMaxMB, got.ContentMaxMB)
	assert.Equal(t, search.MinContentMaxMB, got.ContentMaxMBMin)
	assert.Equal(t, search.MaxContentMaxMB, got.ContentMaxMBMax)
	assert.Equal(t, 25, got.MemoryPercent)
	assert.Equal(t, 1, got.MemoryPercentMin)
	assert.Equal(t, 50, got.MemoryPercentMax)
	assert.True(t, got.AutoRebuild)
	assert.True(t, got.IndexEnabled)
}

func TestSearchSettings_PatchStoresAndAnswersTheNewState(t *testing.T) {
	idx, call := searchSettingsServer(t)
	code, got := call(http.MethodPatch, map[string]any{
		"content": false, "content_max_mb": 64, "memory_percent": 10, "auto_rebuild": false,
	})
	require.Equal(t, http.StatusOK, code)
	assert.False(t, got.Content)
	assert.Equal(t, 64, got.ContentMaxMB)
	assert.Equal(t, 10, got.MemoryPercent)
	assert.False(t, got.AutoRebuild)

	// Read again: it is the ROW that answers, not the PATCH's echo.
	_, again := call(http.MethodGet, nil)
	assert.Equal(t, got.ContentMaxMB, again.ContentMaxMB)
	assert.False(t, again.Content)

	// The memory percent is applied at once, to the running index.
	assert.Equal(t, 10, idx.AppliedMemoryBudget().Percent, "the new percent was stored but not applied")
	if again.MemoryGivenBytes > 0 {
		assert.Equal(t, again.MemoryGivenBytes/100*10, again.MemoryBudgetBytes)
	}

	// A PATCH with one field leaves the others alone.
	_, one := call(http.MethodPatch, map[string]any{"content": true})
	assert.True(t, one.Content)
	assert.Equal(t, 64, one.ContentMaxMB)
}

func TestSearchSettings_OutOfRangeIsRefusedNotClamped(t *testing.T) {
	_, call := searchSettingsServer(t)
	for _, c := range []struct {
		field string
		v     int
	}{
		{"memory_percent", 0}, {"memory_percent", 51}, {"content_max_mb", 0}, {"content_max_mb", 4096},
	} {
		code, got := call(http.MethodPatch, map[string]any{c.field: c.v})
		assert.Equal(t, http.StatusBadRequest, code, "%s=%d", c.field, c.v)
		assert.Equal(t, c.field, got.Field)
		assert.NotEmpty(t, got.Message, "the refusal must be a sentence the page can show")
	}
	_, now := call(http.MethodGet, nil)
	assert.Equal(t, 25, now.MemoryPercent, "a refused value was stored")
	assert.Equal(t, search.DefaultContentMaxMB, now.ContentMaxMB)

	code, _ := call(http.MethodPatch, map[string]any{})
	assert.Equal(t, http.StatusBadRequest, code, "an empty PATCH")
}

func TestSearchSettings_RegularUserIsRefused(t *testing.T) {
	idx, err := search.Open(filepath.Join(t.TempDir(), "idx.bleve"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = idx.Close() })
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) { d.Index = idx })
	testutil.SeedRegularUser(t, store, "alice@example.com", "s3cret-pass-alice")
	testutil.LoginAs(t, srv, client, "alice@example.com", "s3cret-pass-alice")
	resp, err := client.Get(srv.URL + "/api/admin/search/settings")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}
