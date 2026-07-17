package handlers_test

// GET/PATCH /api/admin/protection ("Koru" v0.4): defaults, updates,
// validation bounds, admin gating and the antivirus status block.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

type protectionBody struct {
	TrashRetentionDays int `json:"trash_retention_days"`
	VersionsKeepN      int `json:"versions_keep_n"`
	Antivirus          struct {
		Enabled bool   `json:"enabled"`
		Binary  string `json:"binary"`
	} `json:"antivirus"`
}

func patchProtection(t *testing.T, client *http.Client, url string, body map[string]any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPatch, url+"/api/admin/protection", bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

func TestProtection_GetDefaults(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0") // deterministic AV state regardless of host
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/protection")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got protectionBody
	testutil.ReadJSON(t, resp, &got)
	assert.Equal(t, trash.DefaultRetentionDays, got.TrashRetentionDays)
	assert.Equal(t, 0, got.VersionsKeepN)
	assert.False(t, got.Antivirus.Enabled)
	assert.Equal(t, "", got.Antivirus.Binary)
}

func TestProtection_PatchUpdates(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp := patchProtection(t, client, srv.URL, map[string]any{
		"trash_retention_days": 14,
		"versions_keep_n":      5,
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got protectionBody
	testutil.ReadJSON(t, resp, &got)
	assert.Equal(t, 14, got.TrashRetentionDays)
	assert.Equal(t, 5, got.VersionsKeepN)

	// The settings actually landed on the canonical keys.
	v, err := store.GetSetting(context.Background(), trash.SettingKey)
	require.NoError(t, err)
	assert.Equal(t, "14", v)
	v, err = store.GetSetting(context.Background(), versioning.SettingKeyKeepN)
	require.NoError(t, err)
	assert.Equal(t, "5", v)

	// Partial PATCH: keep_n back to 0 (= unlimited) leaves retention alone.
	resp2 := patchProtection(t, client, srv.URL, map[string]any{"versions_keep_n": 0})
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var got2 protectionBody
	testutil.ReadJSON(t, resp2, &got2)
	assert.Equal(t, 14, got2.TrashRetentionDays)
	assert.Equal(t, 0, got2.VersionsKeepN)
}

func TestProtection_PatchValidation(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	for name, body := range map[string]map[string]any{
		"retention too low":  {"trash_retention_days": 0},
		"retention too high": {"trash_retention_days": 3651},
		"keep_n negative":    {"versions_keep_n": -1},
		"keep_n too high":    {"versions_keep_n": 1001},
		"empty body":         {},
	} {
		resp := patchProtection(t, client, srv.URL, body)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, name)
		resp.Body.Close()
	}

	// Invalid writes must not have touched the settings.
	_, err := store.GetSetting(context.Background(), versioning.SettingKeyKeepN)
	assert.Error(t, err, "no keep_n row should exist after rejected PATCHes")
}

func TestProtection_RequiresAdmin(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)

	// Anonymous → 401.
	resp, err := client.Get(srv.URL + "/api/admin/protection")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// Regular user → 403.
	testutil.SeedRegularUser(t, store, "user@test.local", "UserPass!123")
	testutil.LoginAs(t, srv, client, "user@test.local", "UserPass!123")
	resp, err = client.Get(srv.URL + "/api/admin/protection")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestProtection_AntivirusStatusEnabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake clamav script needs a POSIX shell")
	}
	fake := filepath.Join(t.TempDir(), "clamscan")
	require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("FILEX_CLAMAV", "")
	t.Setenv("FILEX_CLAMAV_BIN", fake)

	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/protection")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got protectionBody
	testutil.ReadJSON(t, resp, &got)
	assert.True(t, got.Antivirus.Enabled)
	assert.Equal(t, "clamscan", got.Antivirus.Binary)
}
