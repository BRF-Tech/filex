package handlers_test

// A storage created without saying whether it is on is ON.
//
// ⚠ The admin panel's "New storage" form never sends `enabled`, and Create
// decoded the body straight into model.Storage, whose zero value is false:
// every storage made from the panel was saved switched off. No scan started,
// the list said "Disabled", and "Sync now" answered 404 "storage not found"
// for a storage that plainly existed. The scripts and the e2e seeds always
// send `enabled: true`, which is why nothing caught it.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestStorageCreate_IsOnUnlessTheRequestSaysOtherwise(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	create := func(body map[string]any) map[string]any {
		raw, _ := json.Marshal(body)
		resp, err := client.Post(srv.URL+"/api/admin/storages", "application/json", bytes.NewReader(raw))
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		out := map[string]any{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}

	// Exactly what the admin panel's form sends.
	panel := create(map[string]any{
		"name": "from-the-panel", "driver": "local", "config": map[string]any{"root": t.TempDir()},
		"read_only": false, "sync_interval_s": 900, "sync_mode": "ondemand",
	})
	assert.Equal(t, true, panel["enabled"], "a storage made from the panel was saved switched off")

	off := create(map[string]any{
		"name": "asked-off", "driver": "local", "config": map[string]any{"root": t.TempDir()},
		"enabled": false, "sync_mode": "ondemand",
	})
	assert.Equal(t, false, off["enabled"], "an explicit `enabled: false` is kept")
}
