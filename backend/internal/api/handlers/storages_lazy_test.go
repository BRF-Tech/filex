package handlers_test

// The admin surface of sync_mode `lazy` (docs/LAZY-CATALOGUE.md): the storage
// form draws the mode's settings from the descriptor, only for the drivers it
// exists on; the API refuses the mode elsewhere and refuses settings outside
// their bounds; and the storage list says how much of each storage its
// catalogue (and so its size figure) covers.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/storage"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func postStorage(t *testing.T, client *http.Client, url string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := client.Post(url+"/api/admin/storages", "application/json", bytes.NewReader(raw))
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// Only a driver a storage may be catalogued lazily on carries the settings,
// so the form offers the mode nowhere else.
//
// Break: fill LazyFields for every driver in StorageDrivers.List.
func TestAdmin_StorageDrivers_CarryTheLazyFieldsForLocalOnly(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/storage-drivers")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []storage.Descriptor
	testutil.ReadJSON(t, resp, &got)
	seen := 0
	for _, d := range got {
		if d.Driver == "local" {
			seen++
			keys := []string{}
			for _, f := range d.LazyFields {
				keys = append(keys, f.Key)
			}
			assert.Equal(t, []string{storage.LazyFillKey, storage.LazyMaxWatchesKey, storage.LazyWatchTTLKey}, keys)
			continue
		}
		assert.Empty(t, d.LazyFields, "%s cannot be catalogued lazily", d.Driver)
	}
	assert.Equal(t, 1, seen)
}

// The mode is refused off a local storage, and its settings outside their
// bounds, with the reason.
//
// Break: drop validateLazySettings from Create.
func TestStorages_LazyModeAndSettingsAreValidated(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	root := t.TempDir()

	code, out := postStorage(t, client, srv.URL, map[string]any{
		"name": "uzak", "driver": "s3", "sync_mode": "lazy",
		"config": map[string]any{"bucket": "b", "prefix": "p", "access_key": "a", "secret_key": "s"},
	})
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, out["error"], "only available on local storages")

	for _, bad := range []map[string]any{
		{"lazy_fill": "sometimes"},
		{"lazy_max_watches": 0},
		{"lazy_max_watches": "many"},
		{"lazy_watch_ttl": 7*24*60 + 1},
	} {
		cfg := map[string]any{"path": root}
		for k, v := range bad {
			cfg[k] = v
		}
		code, out := postStorage(t, client, srv.URL, map[string]any{"name": "tembel", "driver": "local", "sync_mode": "lazy", "config": cfg})
		assert.Equal(t, http.StatusBadRequest, code, "%v was accepted", bad)
		assert.NotEmpty(t, out["error"])
	}

	code, out = postStorage(t, client, srv.URL, map[string]any{"name": "tembel", "driver": "local", "sync_mode": "lazy",
		"config": map[string]any{"path": root, "lazy_fill": "on_open", "lazy_max_watches": "200", "lazy_watch_ttl": 30}})
	require.Equal(t, http.StatusOK, code, out)
	assert.Equal(t, "lazy", out["sync_mode"])
}

// The storage list carries `coverage` while its size figure counts only part
// of a storage (the same object drive usage carries, so Home draws both the
// same way) and a lazy storage's `catalogue` block for the storage page.
func TestStorages_ListSaysHowMuchTheCatalogueCovers(t *testing.T) {
	var worker *filexsync.Worker
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) { worker = d.Worker })
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	root := t.TempDir()

	code, out := postStorage(t, client, srv.URL, map[string]any{"name": "tembel", "driver": "local", "sync_mode": "lazy", "enabled": true,
		"config": map[string]any{"path": root, "lazy_fill": "on_open"}})
	require.Equal(t, http.StatusOK, code, out)
	lazyID := int64(out["id"].(float64))
	code, out = postStorage(t, client, srv.URL, map[string]any{"name": "istekle", "driver": "local", "sync_mode": "ondemand",
		"config": map[string]any{"path": t.TempDir()}})
	require.Equal(t, http.StatusOK, code, out)
	require.Eventually(t, func() bool {
		_, ok := worker.CatalogueStatus(context.Background(), lazyID)
		return ok
	}, 5*time.Second, 10*time.Millisecond, "the lazy engine is running")

	type row struct {
		Name      string                       `json:"name"`
		Coverage  *filexsync.CatalogueCoverage `json:"coverage"`
		Catalogue *filexsync.CatalogueCoverage `json:"catalogue"`
	}
	resp, err := client.Get(srv.URL + "/api/admin/storages")
	require.NoError(t, err)
	var rows []row
	testutil.ReadJSON(t, resp, &rows)
	_ = resp.Body.Close()
	by := map[string]row{}
	for _, r := range rows {
		by[r.Name] = r
	}
	require.NotNil(t, by["tembel"].Coverage)
	assert.Equal(t, filexsync.CoverageVisitedOnly, by["tembel"].Coverage.Reason)
	require.NotNil(t, by["tembel"].Catalogue, "the storage page's catalogue block")
	assert.Equal(t, "on_open", by["tembel"].Catalogue.Fill)
	assert.Equal(t, "off", by["tembel"].Catalogue.Filler)
	require.NotNil(t, by["istekle"].Coverage, "never scanned: its figure is partial")
	assert.Equal(t, filexsync.CoverageFirstScan, by["istekle"].Coverage.Reason)
	assert.Nil(t, by["istekle"].Catalogue, "not lazy: no catalogue block")
}
