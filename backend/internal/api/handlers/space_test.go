package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/diskfree"
	"github.com/brf-tech/filex/backend/internal/space"
)

// spaceOf asks the manager API what `filex mount` asks.
func spaceOf(t *testing.T, c *http.Client, base, remote string) (int, space.Figures) {
	t.Helper()
	u := base + "/api/files/manager?action=space"
	if remote != "" {
		u += "&path=" + url.QueryEscape(remote)
	}
	status, body := mtGet(t, c, u)
	var f space.Figures
	if status == http.StatusOK {
		require.NoError(t, json.Unmarshal([]byte(body), &f), body)
	}
	return status, f
}

// Issue #116, the `filex mount` side: the drive it maps is the storage's disk.
func TestManagerSpace_AStorageIsItsDisk(t *testing.T) {
	f := newMTFix(t, false)
	disk, err := diskfree.Measure(f.RootA)
	if err != nil {
		t.Skipf("no disk probe on this platform: %v", err)
	}
	status, got := spaceOf(t, f.A, f.URL, f.StA.Name+"://")
	require.Equal(t, http.StatusOK, status)
	require.True(t, got.Known, "a local storage could not be measured")
	require.Equal(t, disk.Total, got.Total)
	require.NotZero(t, got.Free)
}

// Without --remote, `filex mount` maps the storage list: the caller's storages
// added up, one disk counted once (alpha's two storages share the test's disk).
func TestManagerSpace_TheStorageListCountsADiskOnce(t *testing.T) {
	f := newMTFix(t, true)
	disk, err := diskfree.Measure(f.RootA)
	if err != nil {
		t.Skipf("no disk probe on this platform: %v", err)
	}
	status, got := spaceOf(t, f.A, f.URL, "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, disk.Total, got.Total)
}

// A storage of another tenant has no size to read: the same not-found its
// listing answers.
func TestManagerSpace_AnotherTenantsStorageIsNotFound(t *testing.T) {
	f := newMTFix(t, true)
	status, _ := spaceOf(t, f.A, f.URL, f.StB.Name+"://")
	require.Equal(t, http.StatusNotFound, status)
}
