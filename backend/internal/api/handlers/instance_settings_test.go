package handlers_test

// Admin → Settings: the settings that lived in the environment alone until
// 0.57 (task #258, internal/instancesettings), over HTTP as the cards read and
// write them.

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

func instanceItems(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	raw, ok := body["items"].([]any)
	require.True(t, ok, "items: %v", body)
	out := map[string]map[string]any{}
	for _, it := range raw {
		m := it.(map[string]any)
		out[m["key"].(string)] = m
	}
	return out
}

func TestInstanceSettings_AGroupIsReadWithItsBoundsAndItsVariable(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/instance-settings?group=general", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	items := instanceItems(t, body)
	for _, key := range []string{"instance.default_locale", "sync.default_interval_minutes", "upload.staging_ttl_hours", "cache.max_gb", "thumbs.sweep", "thumbs.sweep_interval_minutes"} {
		require.Contains(t, items, key)
		assert.Equal(t, "general", items[key]["group"], key)
	}
	assert.NotContains(t, items, "notify.enabled", "another page's")
	sync := items["sync.default_interval_minutes"]
	assert.Equal(t, float64(15), sync["value"])
	assert.Equal(t, float64(1), sync["min"])
	assert.Equal(t, float64(1440), sync["max"])
	assert.Equal(t, "FILEX_SYNC_INTERVAL", sync["env"])
	assert.Equal(t, true, sync["restart"])
}

func TestInstanceSettings_AChangeIsStoredAndSaidToWaitForARestart(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	status, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/instance-settings?group=apps",
		map[string]any{"apps.max_input_mb": 1024, "coedit.unsaved_days": 7})
	require.Equal(t, http.StatusOK, status, "%v", body)
	items := instanceItems(t, body)
	assert.Equal(t, float64(1024), items["apps.max_input_mb"]["value"])
	assert.Equal(t, float64(7), items["coedit.unsaved_days"]["value"])

	v, err := store.GetSetting(t.Context(), "apps.max_input_mb")
	require.NoError(t, err)
	assert.Equal(t, "1024", v)
}

func TestInstanceSettings_AnOutOfRangeValueIsRefusedUnderItsFieldAndNothingIsWritten(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	status, body := doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/instance-settings",
		map[string]any{"cache.max_gb": 50, "sync.default_interval_minutes": 0})
	require.Equal(t, http.StatusBadRequest, status, "%v", body)
	assert.Equal(t, "sync.default_interval_minutes", body["field"])
	assert.Equal(t, "Enter a whole number from 1 to 1440.", body["message"])

	v, _ := store.GetSetting(t.Context(), "cache.max_gb")
	assert.Equal(t, "", v, "the good field of a refused save is not written either")

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/instance-settings",
		map[string]any{"instance.default_locale": "Türkçe"})
	require.Equal(t, http.StatusBadRequest, status, "%v", body)
	assert.Equal(t, "Choose one of the languages offered.", body["message"])
}
