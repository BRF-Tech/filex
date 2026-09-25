package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/archivecli"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestArchiveAdminPolicyValidationAndPersistence(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, password := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, password)

	status, body := doJSON(t, client, http.MethodGet, srv.URL+"/api/admin/archives", nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "7z", body["default_format"])
	assert.NotEmpty(t, body["providers"])

	status, capabilities := doJSON(t, client, http.MethodGet, srv.URL+"/api/files/capabilities", nil)
	require.Equal(t, http.StatusOK, status)
	archivePolicy := capabilities["archive"].(map[string]any)
	// The explorer is offered what this server can MAKE: every format but a
	// plain ZIP needs 7-Zip (TestCapabilities_ArchiveOffersWhatThisServerCanMake
	// pins both halves), so this depends on whether the host running the test
	// has one.
	if archivePolicy["encryption"] == true {
		assert.Equal(t, "7z", archivePolicy["default_format"])
		assert.Equal(t, []any{"zip", "7z", "tar", "tar.gz", "tar.bz2", "tar.xz"}, archivePolicy["allowed_formats"])
	} else {
		assert.Equal(t, "zip", archivePolicy["default_format"])
		assert.Equal(t, []any{"zip"}, archivePolicy["allowed_formats"])
	}

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/archives", map[string]any{
		"allowed_formats": []string{"7z"},
		"default_format":  "zip",
	})
	require.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, body["error"], "included")

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/archives", map[string]any{
		"max_entries": 0,
	})
	require.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, body["error"], "max_entries")

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/archives", map[string]any{
		"enabled":            false,
		"allowed_formats":    []string{".7Z", "zip", "rar", "7z"},
		"default_format":     ".7z",
		"max_entries":        321,
		"max_expanded_bytes": 64 << 20,
		"timeout_seconds":    120,
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, false, body["enabled"])
	assert.Equal(t, "7z", body["default_format"])
	assert.Equal(t, []any{"7z", "zip"}, body["allowed_formats"])
	assert.Equal(t, float64(321), body["max_entries"])
	assert.Equal(t, float64(64<<20), body["max_expanded_bytes"])
	assert.Equal(t, float64(120), body["timeout_seconds"])

	assert.Equal(t, "false", settingValue(t, store, archivecli.SettingEnabled))
	assert.Equal(t, "7z", settingValue(t, store, archivecli.SettingDefaultFormat))
	assert.Equal(t, "7z,zip", settingValue(t, store, archivecli.SettingAllowedFormats))
	assert.Equal(t, "321", settingValue(t, store, archivecli.SettingMaxEntries))
	assert.Equal(t, "67108864", settingValue(t, store, archivecli.SettingMaxExpandedBytes))
	assert.Equal(t, "120", settingValue(t, store, archivecli.SettingTimeoutSeconds))

	status, body = doJSON(t, client, http.MethodPatch, srv.URL+"/api/admin/archives", map[string]any{
		"allowed_formats": []string{"tgz", ".tar.bz2", "txz"},
		"default_format":  "tgz",
	})
	require.Equal(t, http.StatusOK, status, "%v", body)
	assert.Equal(t, "tar.gz", body["default_format"])
	assert.Equal(t, []any{"tar.gz", "tar.bz2", "tar.xz"}, body["allowed_formats"])

	policy := archivecli.New(store, archivecli.Config{}).Policy(context.Background())
	assert.False(t, policy.Enabled)
	assert.Equal(t, "tar.gz", policy.DefaultFormat)
	assert.Equal(t, []string{"tar.gz", "tar.bz2", "tar.xz"}, policy.AllowedFormats)
}
