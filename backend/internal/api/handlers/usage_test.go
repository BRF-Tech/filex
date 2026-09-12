package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const usageReportCSV = `date,bucket_id,bucket_name,uploaded_gb,deleted_gb,downloaded_gb,downloaded_bytes,downloaded_favored_bytes,stored_gb,storage_byte_hours,api_txn_class_a,api_txn_class_b,api_txn_class_c,api_txn_class_d
2026-09-10,,,0.00,0.00,0.00,0,0,0.00,0,0,0,87,0
2026-09-10,b4d5e6,vps-ops,0.02,0.00,0.00,40260,0,0.47,11093733371,27,2,3,0
`

// usageFixture stands a server up with a local storage that holds a B2-shaped
// report, and the settings that point filex at it.
type usageHarness struct {
	server *httptest.Server
	client *http.Client
	store  db.Store
}

func usageFixture(t *testing.T) usageHarness {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "2026-09-10"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(root, "2026-09-10", "usage.account-abc123.csv"), []byte(usageReportCSV), 0o644))

	drv, err := storage.Get("local")
	require.NoError(t, err)
	require.NoError(t, drv.Init(context.Background(), map[string]any{"path": root}))

	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
		// Every storage resolves to the report tree; this test has only one.
		d.StorageResolver = func(int64) (storage.Driver, error) { return drv, nil }
	})
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	_, err = store.CreateStorage(context.Background(), &model.Storage{
		Name: "b2-reports", Driver: "local", MountPath: "/reports",
		ConfigJSON: json.RawMessage(`{"path":"` + filepath.ToSlash(root) + `"}`),
		SyncMode:   model.SyncModePoll, SyncIntervalS: 900, Enabled: true, ReadOnly: true,
	})
	require.NoError(t, err)

	for k, v := range map[string]string{
		"usage.provider":       "b2",
		"usage.report_storage": "b2-reports",
		"usage.account_id":     "abc123",
	} {
		require.NoError(t, store.UpsertSetting(context.Background(), k, v))
	}
	return usageHarness{server: srv, client: client, store: store}
}

func TestUsage_ReportsWhatTheProviderPublished(t *testing.T) {
	h := usageFixture(t)

	resp, err := h.client.Get(h.server.URL + "/api/admin/usage?from=2026-09-10&to=2026-09-10")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Configured bool `json:"configured"`
		Days       []struct {
			Bucket string `json:"bucket"`
			Scope  string `json:"scope"`
			Source string `json:"source"`
		} `json:"days"`
		Totals struct {
			OpsC       int64 `json:"ops_c"`
			AccountOps struct {
				C int64 `json:"C"`
			} `json:"account_ops"`
		} `json:"totals"`
		Cost struct {
			Currency string  `json:"currency"`
			Total    float64 `json:"total"`
		} `json:"cost"`
		Notes []string `json:"notes"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))

	require.True(t, out.Configured)
	require.Len(t, out.Days, 2)
	require.Equal(t, int64(3), out.Totals.OpsC, "the bucket row")
	require.Equal(t, int64(87), out.Totals.AccountOps.C, "the account row, beside it")
	require.Equal(t, "USD", out.Cost.Currency)
	require.Zero(t, out.Cost.Total, "half a gigabyte is inside every free allowance")
	require.NotEmpty(t, out.Notes, "the page is told the prices are ours, not the operator's")
}

// An instance with nothing configured must say so rather than render an empty
// chart, which reads as "you spent nothing".
func TestUsage_UnconfiguredSaysSo(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/usage")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var out struct {
		Configured bool `json:"configured"`
		Days       []any
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.False(t, out.Configured)
	require.Empty(t, out.Days)
}

// The window is the caller's, and a bad one is refused rather than guessed at.
func TestUsage_RefusesAnUnparseableRange(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/usage?from=yesterday")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
