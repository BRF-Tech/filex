package handlers_test

// The antivirus CONFIGURATION half of /api/admin/protection: the on/off
// switch, the transport mode and the clamd address.
//
// ⚠⚠ These three are deferred — stored immediately, in force at the next
// restart, in both directions. The API has to be honest about that in a way a
// UI can render, which is what `restart_pending` is for; a toast that says
// "restart required" and then keeps saying it forever, or stops saying it
// while it is still true, is worse than no message.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/antivirus"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type avConfigBody struct {
	Antivirus struct {
		Enabled        bool     `json:"enabled"`
		Binary         string   `json:"binary"`
		Mode           string   `json:"mode"`
		Address        string   `json:"address"`
		Reachable      bool     `json:"reachable"`
		Health         string   `json:"health"`
		RestartPending bool     `json:"restart_pending"`
		ScanEnabled    bool     `json:"scan_enabled"`
		ScanMode       string   `json:"scan_mode"`
		ClamdAddr      string   `json:"clamd_addr"`
		Modes          []string `json:"modes"`
	} `json:"antivirus"`
}

func getAVConfig(t *testing.T, client *http.Client, url string) avConfigBody {
	t.Helper()
	resp, err := client.Get(url + "/api/admin/protection")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got avConfigBody
	testutil.ReadJSON(t, resp, &got)
	return got
}

func TestProtectionAVConfig_DefaultsAndShape(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	got := getAVConfig(t, client, srv.URL).Antivirus
	assert.False(t, got.ScanEnabled, "FILEX_CLAMAV=0 seeded the switch off")
	assert.Equal(t, antivirus.ModeBinary, got.ScanMode, "binary is what every install did before")
	assert.Equal(t, "", got.ClamdAddr)
	assert.Equal(t, []string{"binary", "daemon"}, got.Modes,
		"the form gets the closed set from the API rather than hard-coding it")

	// Switched off is not the same as broken: no transport is reported at all.
	assert.False(t, got.Enabled)
	assert.Equal(t, "", got.Mode)
	assert.False(t, got.Reachable)
}

// ⚠ The toggle is stored and echoed at once, so the UI can state the deferral
// truthfully at the moment of the flip rather than promising it is live.
func TestProtectionAVConfig_ToggleIsStoredImmediately(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp := patchProtection(t, client, srv.URL, map[string]any{"av_enabled": true})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	assert.True(t, getAVConfig(t, client, srv.URL).Antivirus.ScanEnabled)
	assert.True(t, antivirus.EnabledSetting.Resolve(context.Background(), store),
		"the row, not the environment, is what is in force from now on")

	// And back off again, because a switch that only works one way is not one.
	resp = patchProtection(t, client, srv.URL, map[string]any{"av_enabled": false})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	assert.False(t, getAVConfig(t, client, srv.URL).Antivirus.ScanEnabled)
	assert.False(t, antivirus.EnabledSetting.Resolve(context.Background(), store))
}

func TestProtectionAVConfig_DaemonAddressRoundTrip(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp := patchProtection(t, client, srv.URL, map[string]any{
		"av_enabled":    true,
		"av_mode":       "daemon",
		"av_clamd_addr": "clamav:3310",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	got := getAVConfig(t, client, srv.URL).Antivirus
	assert.Equal(t, "daemon", got.ScanMode)
	assert.Equal(t, "clamav:3310", got.ClamdAddr)
	assert.Equal(t, "clamav:3310", antivirus.AddrSetting.Resolve(context.Background(), store))

	// ⚠⚠ Nothing is listening on that name in a test, and the response says so
	// rather than showing a scanner that looks fine. `enabled` (configured) and
	// `reachable` (answers) are separate questions on purpose.
	assert.True(t, got.Enabled, "configured")
	assert.False(t, got.Reachable, "but nothing answered")
	assert.NotEmpty(t, got.Health, "and the page can say why")
	assert.Equal(t, "clamd", got.Binary, "the daemon is what would answer")
	assert.Equal(t, "daemon", got.Mode)
}

// A whitespace-mangled address is refused AT SAVE TIME. Without this the
// mistake is discovered by the first file that failed to get scanned.
func TestProtectionAVConfig_BadAddressIsRefused(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	for _, bad := range []string{"clamav 3310", "clamav:0", "clamav:http", ":3310"} {
		resp := patchProtection(t, client, srv.URL, map[string]any{"av_clamd_addr": bad})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "should refuse %q", bad)
		var body map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		// ⚠ The message is rendered under the form field, so it must read as a
		// sentence to the person who just typed the value — not as a Go error
		// wearing its package name.
		assert.NotContains(t, body["error"], "antivirus:", "for %q", bad)
		assert.Contains(t, body["error"], bad, "the message has to quote what was typed")
	}
	assert.Equal(t, "", antivirus.AddrSetting.Resolve(context.Background(), store),
		"a refused write stores nothing")

	// An unknown mode is refused and the error names the two that are allowed.
	resp := patchProtection(t, client, srv.URL, map[string]any{"av_mode": "carrier-pigeon"})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// ⚠ Each half is legal alone and the pair is not: daemon mode with no address
// is a scanner that is switched on and can reach nothing. It is refused while
// the operator is still looking at the form.
func TestProtectionAVConfig_DaemonModeNeedsAnAddress(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp := patchProtection(t, client, srv.URL, map[string]any{"av_mode": "daemon"})
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
	assert.Equal(t, antivirus.ModeBinary,
		antivirus.ModeSetting.Resolve(context.Background(), store), "nothing was stored")

	// Together they are fine.
	resp = patchProtection(t, client, srv.URL, map[string]any{
		"av_mode":       "daemon",
		"av_clamd_addr": "127.0.0.1:33999",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// And switching mode alone is fine once an address is on file.
	resp = patchProtection(t, client, srv.URL, map[string]any{"av_mode": "binary"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	resp = patchProtection(t, client, srv.URL, map[string]any{"av_mode": "daemon"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// Values are normalised before they are stored, so the row can never hold text
// the read path would reject and quietly replace with a default.
func TestProtectionAVConfig_ValuesAreCanonicalised(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp := patchProtection(t, client, srv.URL, map[string]any{
		"av_mode":       "  DAEMON  ",
		"av_clamd_addr": "  clamav:3310  ",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	raw, err := store.GetSetting(context.Background(), antivirus.ModeSetting.Key)
	require.NoError(t, err)
	assert.Equal(t, "daemon", raw, "stored canonical, not as typed")
	raw, err = store.GetSetting(context.Background(), antivirus.AddrSetting.Key)
	require.NoError(t, err)
	assert.Equal(t, "clamav:3310", raw)
}
