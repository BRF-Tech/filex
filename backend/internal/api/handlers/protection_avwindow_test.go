package handlers_test

// The antivirus save-scan window on /api/admin/protection: the default, an
// explicit value, and what happens either side of the bounds.
//
// A setting is a contract, so all three halves of it are pinned here — refused
// on write, clamped on read, and reported back with the bounds the UI renders.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/antivirus"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

type avWindowBody struct {
	Antivirus struct {
		SaveScanWindowMinutes int `json:"save_scan_window_minutes"`
		SaveScanWindowMin     int `json:"save_scan_window_min"`
		SaveScanWindowMax     int `json:"save_scan_window_max"`
		MaxScanMB             int `json:"max_scan_mb"`
		MaxScanMBMin          int `json:"max_scan_mb_min"`
		MaxScanMBMax          int `json:"max_scan_mb_max"`
	} `json:"antivirus"`
}

func getProtectionAV(t *testing.T, client *http.Client, url string) avWindowBody {
	t.Helper()
	resp, err := client.Get(url + "/api/admin/protection")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got avWindowBody
	testutil.ReadJSON(t, resp, &got)
	return got
}

func TestProtectionAVWindow_DefaultAndBounds(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	got := getProtectionAV(t, client, srv.URL)
	assert.Equal(t, 30, got.Antivirus.SaveScanWindowMinutes, "the documented default")
	assert.Equal(t, 2, got.Antivirus.SaveScanWindowMin)
	assert.Equal(t, 60, got.Antivirus.SaveScanWindowMax)
}

func TestProtectionAVWindow_PatchHonoursValue(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp := patchProtection(t, client, srv.URL, map[string]any{"av_save_scan_window_minutes": 45})
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, 45, getProtectionAV(t, client, srv.URL).Antivirus.SaveScanWindowMinutes)

	// It really is stored, so a later scan resolves it without a restart.
	assert.Equal(t, 45, antivirus.SaveWindowSetting.Resolve(context.Background(), store))

	// Both bounds are inclusive.
	for _, v := range []int{2, 60} {
		r := patchProtection(t, client, srv.URL, map[string]any{"av_save_scan_window_minutes": v})
		require.Equal(t, http.StatusOK, r.StatusCode)
		r.Body.Close()
		assert.Equal(t, v, getProtectionAV(t, client, srv.URL).Antivirus.SaveScanWindowMinutes)
	}
}

// ⚠ Out of range is REFUSED at save time, not silently clamped: the operator
// finds out while looking at the field. 0 in particular has no meaning here —
// it reads equally as "scan every save" and "never scan" — so it is rejected
// rather than given one.
func TestProtectionAVWindow_PatchRefusesOutOfRange(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	ok := patchProtection(t, client, srv.URL, map[string]any{"av_save_scan_window_minutes": 20})
	require.Equal(t, http.StatusOK, ok.StatusCode)
	ok.Body.Close()

	for _, bad := range []int{0, 1, -5, 61, 1440} {
		resp := patchProtection(t, client, srv.URL, map[string]any{"av_save_scan_window_minutes": bad})
		body := map[string]string{}
		testutil.ReadJSON(t, resp, &body)
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "value %d must be refused", bad)
		assert.Contains(t, body["error"], "between 2 and 60 minutes")
		assert.Equal(t, 20, getProtectionAV(t, client, srv.URL).Antivirus.SaveScanWindowMinutes,
			"a refused write must not disturb the stored value")
	}
}

// A row that is already out of range — written by hand, or by an older build
// with different bounds — clamps on read rather than taking the server down,
// and the page shows the value actually in force.
func TestProtectionAVWindow_ClampsAStoredOutOfRangeRow(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	ctx := context.Background()
	require.NoError(t, store.UpsertSetting(ctx, antivirus.SaveWindowSetting.Key, "10080"))
	assert.Equal(t, 60, getProtectionAV(t, client, srv.URL).Antivirus.SaveScanWindowMinutes)

	require.NoError(t, store.UpsertSetting(ctx, antivirus.SaveWindowSetting.Key, "0"))
	assert.Equal(t, 2, getProtectionAV(t, client, srv.URL).Antivirus.SaveScanWindowMinutes)

	require.NoError(t, store.UpsertSetting(ctx, antivirus.SaveWindowSetting.Key, "nonsense"))
	assert.Equal(t, 30, getProtectionAV(t, client, srv.URL).Antivirus.SaveScanWindowMinutes)
}

// The scan-size ceiling, the other antivirus setting that now lives in the
// database. Same three halves: default, honoured write, refused write.
func TestProtectionAVMaxScan_DefaultPatchAndBounds(t *testing.T) {
	t.Setenv("FILEX_CLAMAV", "0")
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	got := getProtectionAV(t, client, srv.URL)
	assert.Equal(t, 100, got.Antivirus.MaxScanMB, "the old FILEX_CLAMAV_MAX default, in MB")
	assert.Equal(t, 1, got.Antivirus.MaxScanMBMin)
	assert.Equal(t, 10240, got.Antivirus.MaxScanMBMax)

	ok := patchProtection(t, client, srv.URL, map[string]any{"av_max_scan_mb": 250})
	require.Equal(t, http.StatusOK, ok.StatusCode)
	ok.Body.Close()
	assert.Equal(t, 250, getProtectionAV(t, client, srv.URL).Antivirus.MaxScanMB)
	assert.EqualValues(t, 250<<20, antivirus.MaxScanBytesFrom(context.Background(), store),
		"the value the scanner reads must be the value the page shows")

	for _, bad := range []int{0, -1, 10241} {
		resp := patchProtection(t, client, srv.URL, map[string]any{"av_max_scan_mb": bad})
		body := map[string]string{}
		testutil.ReadJSON(t, resp, &body)
		resp.Body.Close()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "value %d must be refused", bad)
		assert.Contains(t, body["error"], "between 1 and 10240 MB")
		assert.Equal(t, 250, getProtectionAV(t, client, srv.URL).Antivirus.MaxScanMB)
	}
}
