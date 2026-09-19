package handlers_test

// POST /api/admin/storages/discover — the folders under a root, so the
// operator can mount several of them as separate storages in one go.
//
// Issue #31: a bucket with N top-level folders was N trips through the storage
// form, because the bucket root itself is never mounted (ROOT_PATH_FORBIDDEN).
// The probe may look at the root; only the rows created afterwards go through
// the guard.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"

	_ "github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

func discover(t *testing.T, srv string, client *http.Client, driver string, cfg map[string]any) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"driver": driver, "config": cfg})
	resp, err := client.Post(srv+"/api/admin/storages/discover", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestStoragesDiscover_ListsTheFoldersUnderARoot(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)
	root := t.TempDir()
	for _, d := range []string{"belgeler", "arsiv", ".filex-trash", ".git"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "loose.txt"), []byte("x"), 0o644))

	code, out := discover(t, srv.URL, client, "local", map[string]any{"path": root})
	require.Equal(t, http.StatusOK, code, out)
	require.Equal(t, true, out["ok"], out)
	assert.Equal(t, "path", out["root_key"], "the caller is told which config key the root lives in")

	folders, _ := out["folders"].([]any)
	require.Len(t, folders, 2, "two folders, sorted; the file and the dot-folders are not offered: %v", folders)
	first, _ := folders[0].(map[string]any)
	second, _ := folders[1].(map[string]any)
	assert.Equal(t, "arsiv", first["name"])
	assert.Equal(t, filepath.ToSlash(filepath.Join(root, "arsiv")), filepath.ToSlash(first["root"].(string)))
	assert.Equal(t, "belgeler", second["name"])

	// Each discovered root is a storage the ordinary create accepts.
	code, created := createStorage(t, srv.URL, "local", client, map[string]any{"path": first["root"]})
	require.Contains(t, []int{http.StatusOK, http.StatusCreated}, code, created)
}

func TestStoragesDiscover_RefusesWhatItCannotLookUnder(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	code, out := discover(t, srv.URL, client, "no-such-driver", map[string]any{})
	assert.Equal(t, http.StatusBadRequest, code, out)

	// A root that cannot be listed (a file, not a folder) is a failed probe,
	// said in the body the way Test says it.
	file := filepath.Join(t.TempDir(), "dosya.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	code, out = discover(t, srv.URL, client, "local", map[string]any{"path": file})
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, false, out["ok"])
	assert.NotEmpty(t, out["error"])
}
