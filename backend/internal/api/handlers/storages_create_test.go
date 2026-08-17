package handlers_test

// The bug these tests lock down: POST /api/admin/storages answered 400
// ROOT_PATH_FORBIDDEN for s3, sftp and webdav no matter what the operator
// typed, because the admin form and the validator kept separate lists of
// config keys. Both sides now read the driver's descriptor, so the
// contract is testable: a config built from what a driver *declares* it
// needs must be accepted.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil"

	// The API package does not link the storage drivers itself; the server
	// binary does. Register them here so descriptors are present.
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/ftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/s3"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/sftp"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/smb"
	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/webdav"
)

// sampleConfig fills every field a driver declares as required — exactly
// what a descriptor-driven form collects before it enables Save.
func sampleConfig(d storage.Descriptor) map[string]any {
	cfg := map[string]any{}
	for _, f := range d.Fields {
		if !f.Required && !f.Root {
			continue
		}
		switch f.Type {
		case storage.FieldBool:
			cfg[f.Key] = f.Default == true
		case storage.FieldInt:
			if n, ok := f.Default.(int); ok {
				cfg[f.Key] = n
			} else {
				cfg[f.Key] = 1
			}
		case storage.FieldSelect:
			if len(f.Options) > 0 {
				cfg[f.Key] = f.Options[0].Value
			}
		case storage.FieldPassword:
			cfg[f.Key] = "s3cr3t"
		default:
			if f.Root {
				cfg[f.Key] = "fileman"
			} else if f.Key == "url" {
				cfg[f.Key] = "https://dav.example.com/files/"
			} else {
				cfg[f.Key] = "sample"
			}
		}
	}
	return cfg
}

func createStorage(t *testing.T, srv, driver string, client *http.Client, cfg map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":   fmt.Sprintf("t-%s-%d", driver, len(cfg)),
		"driver": driver,
		"config": cfg,
	})
	resp, err := client.Post(srv+"/api/admin/storages", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestStorages_Create_FromDescriptor — every driver the admin UI can
// offer must be creatable from what its descriptor declares. Before the
// descriptors existed this was true for `local` only.
func TestStorages_Create_FromDescriptor(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	descs := storage.Descriptors()
	require.GreaterOrEqual(t, len(descs), 5, "built-in drivers should be registered")
	for _, d := range descs {
		t.Run(d.Driver, func(t *testing.T) {
			code, out := createStorage(t, srv.URL, d.Driver, client, sampleConfig(d))
			require.Equal(t, http.StatusOK, code, "driver %s rejected: %v", d.Driver, out["error"])
			assert.Equal(t, d.Driver, out["driver"])
		})
	}
}

// TestStorages_Create_LegacyKeys — the spellings older surfaces wrote are
// declared as aliases, so rows created by them keep validating (and the
// drivers keep reading them).
func TestStorages_Create_LegacyKeys(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	cases := []struct {
		name   string
		driver string
		config map[string]any
	}{
		{
			// What the shipped admin form sent for sftp: base_path, and a
			// key file path instead of the PEM.
			name: "sftp base_path + key_path", driver: "sftp",
			config: map[string]any{
				"host": "files.example.com", "port": 22, "user": "filex",
				"password": "pw", "key_path": "/etc/filex/keys/id_ed25519",
				"base_path": "/srv/files",
			},
		},
		{
			// What the replication-target dialog sent: username, not user.
			name: "sftp username", driver: "sftp",
			config: map[string]any{"host": "h", "username": "filex", "password": "pw", "root": "/srv/files"},
		},
		{
			name: "webdav username + remote_path", driver: "webdav",
			config: map[string]any{"url": "https://dav.example.com/files/", "username": "filex", "password": "pw", "remote_path": "fileman"},
		},
		{
			name: "ftp remote_path", driver: "ftp",
			config: map[string]any{"host": "ftp.example.com", "user": "filex", "password": "pw", "remote_path": "/files"},
		},
		{
			// local's pre-0.19 key.
			name: "local root", driver: "local",
			config: map[string]any{"root": "/var/lib/filex/data"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := createStorage(t, srv.URL, tc.driver, client, tc.config)
			require.Equal(t, http.StatusOK, code, "legacy config rejected: %v", out["error"])
		})
	}
}

// TestStorages_Create_StillRejectsRootMount — the fix must not open the
// door the validator was built to hold shut.
func TestStorages_Create_StillRejectsRootMount(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	for _, tc := range []struct {
		driver string
		config map[string]any
	}{
		{"s3", map[string]any{"bucket": "b", "region": "auto", "access_key": "a", "secret_key": "s"}},
		{"s3", map[string]any{"bucket": "b", "prefix": "/"}},
		{"local", map[string]any{"path": ""}},
		{"sftp", map[string]any{"host": "h", "user": "u", "password": "p", "base_path": "/"}},
		{"webdav", map[string]any{"url": "https://dav.example.com/", "user": "u", "password": "p"}},
		{"ftp", map[string]any{"host": "h", "user": "u", "password": "p", "root": "  "}},
	} {
		t.Run(tc.driver+fmt.Sprint(len(tc.config)), func(t *testing.T) {
			code, out := createStorage(t, srv.URL, tc.driver, client, tc.config)
			require.Equal(t, http.StatusBadRequest, code, "root mount was accepted")
			assert.Contains(t, fmt.Sprint(out["error"]), "ROOT_PATH_FORBIDDEN")
		})
	}
}

// ---------- /api/admin/storage-drivers ----------

func TestAdmin_StorageDrivers_List(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/storage-drivers")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got []map[string]any
	testutil.ReadJSON(t, resp, &got)
	byName := map[string]map[string]any{}
	for _, d := range got {
		byName[fmt.Sprint(d["driver"])] = d
	}
	// ftp is registered in the backend but the old admin form's hardcoded
	// list never mentioned it, which made the driver invisible.
	for _, want := range []string{"local", "s3", "sftp", "ftp", "webdav"} {
		require.Contains(t, byName, want)
	}

	s3 := byName["s3"]
	require.NotEmpty(t, s3["capabilities"])
	fields, _ := s3["fields"].([]any)
	require.NotEmpty(t, fields)
	var sawRoot, sawSecret bool
	for _, raw := range fields {
		f, _ := raw.(map[string]any)
		require.NotEmpty(t, f["i18n_key"], "field %v has no i18n key — the surface would render English", f["key"])
		require.NotEmpty(t, f["label"], "field %v has no English fallback label", f["key"])
		if f["root"] == true {
			sawRoot = true
			assert.Equal(t, "prefix", f["key"], "s3 scopes on prefix")
		}
		if f["secret"] == true {
			sawSecret = true
			assert.Equal(t, "password", f["type"], "secrets must render masked")
		}
	}
	assert.True(t, sawRoot, "s3 descriptor must flag its root field")
	assert.True(t, sawSecret, "s3 descriptor must flag its credential fields")
}

func TestAdmin_StorageDrivers_RequiresAdmin(t *testing.T) {
	srv, client, _ := testutil.NewTestServer(t)
	resp, err := client.Get(srv.URL + "/api/admin/storage-drivers")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
