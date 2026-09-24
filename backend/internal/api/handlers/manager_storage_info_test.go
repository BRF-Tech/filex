package handlers_test

// The manager's listings say, beside the bare `storages` names, whether each
// drive the caller can see is read-only (`storage_info`).
//
// ⚠ Why (QA, 2026-09-21): `read_only` used to be said only for the storage
// being LISTED. A person who cannot read `/api/admin/storages` — every
// non-admin — never learnt that a drive was read-only until they were inside
// it and the menu refused them; the SPA tried to find out anyway with a call
// to the admin list that answered 403 on every page load. The root is
// RBAC-filtered already, so the flag must not name a drive the caller cannot
// open either.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func mkInfoStorage(t *testing.T, url string, admin *http.Client, name string, readOnly, rbac bool) {
	t.Helper()
	cfg, _ := json.Marshal(map[string]string{"root": t.TempDir()})
	st, raw := doReq(t, admin, http.MethodPost, url+"/api/admin/storages", model.Storage{
		Name:          name,
		Driver:        "local",
		MountPath:     "/data",
		ConfigJSON:    cfg,
		SyncMode:      model.SyncModePoll,
		SyncIntervalS: 900,
		Enabled:       true,
		ReadOnly:      readOnly,
		RBACEnabled:   rbac,
	})
	require.Equal(t, http.StatusOK, st, "create storage %s: %s", name, raw)
}

type storageInfoBody struct {
	Storages    []string `json:"storages"`
	StorageInfo []struct {
		Name     string `json:"name"`
		ReadOnly bool   `json:"read_only"`
	} `json:"storage_info"`
}

func TestManagerRoot_SaysWhichDrivesAreReadOnly(t *testing.T) {
	srv, adminClient, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, adminClient, email, pw)

	mkInfoStorage(t, srv.URL, adminClient, "depo", false, false)
	mkInfoStorage(t, srv.URL, adminClient, "arsiv", true, false)
	mkInfoStorage(t, srv.URL, adminClient, "kapali", true, true)

	createUser(t, srv.URL, adminClient, "info-p@test.local", "UserPass1!", model.RoleUser)
	userClient := freshClient(t)
	testutil.LoginAs(t, srv, userClient, "info-p@test.local", "UserPass1!")

	read := func(path string) storageInfoBody {
		t.Helper()
		st, raw := doReq(t, userClient, http.MethodGet, srv.URL+"/api/files/manager?action=index&path="+path, nil)
		require.Equal(t, http.StatusOK, st, "index %q: %s", path, raw)
		var b storageInfoBody
		require.NoError(t, json.Unmarshal(raw, &b))
		return b
	}

	for _, path := range []string{"", "depo%3A%2F%2F"} {
		b := read(path)
		got := map[string]bool{}
		for _, i := range b.StorageInfo {
			got[i.Name] = i.ReadOnly
		}
		assert.Equal(t, map[string]bool{"depo": false, "arsiv": true}, got,
			"listing %q: storage_info must mark arsiv read-only for a non-admin, and name exactly the drives in `storages`", path)
		assert.ElementsMatch(t, b.Storages, []string{"depo", "arsiv"},
			"the RBAC drive the caller has no grant on is not listed — and not described either")
	}
}
