package handlers_test

// Issue #44: a storage's scan exclusions (`config.scan_exclude`). A pattern
// that cannot mean what the operator wants — above all one that would exclude
// the whole storage — is refused where it is typed, with a machine code in
// `error` and a sentence in the reader's language in `message` (the server
// catalogue, `server.storage.*`); one that compiles reaches the running syncer
// on Save, like every other storage edit.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	filexsync "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// createStorageLang is createStorage by an admin whose account speaks lang —
// the language the refusal has to come back in.
func createStorageLang(t *testing.T, srv string, client *http.Client, lang string, cfg map[string]any) (int, map[string]any) {
	t.Helper()
	prof, _ := json.Marshal(map[string]any{"locale": lang})
	preq, err := http.NewRequest(http.MethodPatch, srv+"/api/auth/profile", strings.NewReader(string(prof)))
	require.NoError(t, err)
	preq.Header.Set("Content-Type", "application/json")
	presp, err := client.Do(preq)
	require.NoError(t, err)
	_ = presp.Body.Close()
	require.Equal(t, http.StatusOK, presp.StatusCode, "set the account's language")
	body, _ := json.Marshal(map[string]any{"name": "t-lang-" + lang, "driver": "local", "config": cfg})
	req, err := http.NewRequest(http.MethodPost, srv+"/api/admin/storages", strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestStorages_ScanExclusions(t *testing.T) {
	ctx := context.Background()
	var worker *filexsync.Worker
	srv, client, store := testutil.NewTestServerWith(t, nil, func(d *api.Deps) { worker = d.Worker })
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	root := t.TempDir()
	write := func(rel string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte("x"), 0o644))
	}
	write("proj/readme.md")

	t.Run("create refuses a pattern that cannot work, and says why", func(t *testing.T) {
		for bad, says := range map[string]string{
			"*":                      "would exclude everything on this storage",
			"**/*":                   "would exclude everything on this storage",
			"!keep":                  "cannot be used to exclude paths from scanning",
			"a/../b":                 "cannot be used to exclude paths from scanning",
			"[x":                     "cannot be used to exclude paths from scanning",
			strings.Repeat("a", 513): "at most 200 patterns, each at most 512 characters",
		} {
			code, out := createStorage(t, srv.URL, "local", client, map[string]any{"path": root, "scan_exclude": bad})
			assert.Equal(t, http.StatusBadRequest, code, "%q was accepted", bad)
			assert.Equal(t, "SCAN_EXCLUDE_INVALID", out["error"], "%q", bad)
			assert.Contains(t, fmt.Sprint(out["message"]), says, "%q", bad)
		}
	})

	t.Run("the refusal is in the reader's language", func(t *testing.T) {
		code, out := createStorageLang(t, srv.URL, client, "tr", map[string]any{"path": root, "scan_exclude": "**"})
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, "“**” bu depodaki her şeyi hariç tutar. Bunun yerine atlanacak olanı yazın; örneğin .git, *.tmp ya da downloads/incomplete/**.", out["message"])
		// …and so is the root-path guard beside it in the same handler.
		code, out = createStorageLang(t, srv.URL, client, "tr", map[string]any{"path": "/"})
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Equal(t, "ROOT_PATH_FORBIDDEN", out["error"])
		assert.Contains(t, fmt.Sprint(out["message"]), "Bir depo, arka ucun tamamı olamaz")
		code, out = createStorageLang(t, srv.URL, client, "en", map[string]any{"path": "/"})
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Contains(t, fmt.Sprint(out["message"]), "cannot be the whole of its backend")
	})

	code, out := createStorage(t, srv.URL, "local", client, map[string]any{"path": root, "scan_exclude": "*.tmp\n# notes\n"})
	require.Equal(t, http.StatusOK, code, "%v", out["error"])
	id := int64(out["id"].(float64))

	patch := func(t *testing.T, exclude string) (int, string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{
			"name": out["name"], "driver": "local", "mount_path": "/",
			"config":  map[string]any{"path": root, "scan_exclude": exclude},
			"enabled": true, "sync_mode": string(model.SyncModeOnDemand), "sync_interval_s": 900,
		})
		req, err := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/api/admin/storages/%d", srv.URL, id), strings.NewReader(string(raw)))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, fmt.Sprint(body["message"])
	}

	t.Run("an edit that cannot work is refused and changes nothing", func(t *testing.T) {
		code, msg := patch(t, ".git\n**")
		require.Equal(t, http.StatusBadRequest, code)
		assert.Contains(t, msg, "exclude everything")
		st, err := store.GetStorage(ctx, id)
		require.NoError(t, err)
		assert.Contains(t, string(st.ConfigJSON), "*.tmp", "the refused edit was saved anyway")
	})

	t.Run("an edit that works reaches the running syncer", func(t *testing.T) {
		code, msg := patch(t, ".git")
		require.Equal(t, http.StatusOK, code, msg)
		write("proj/.git/HEAD")
		require.NoError(t, worker.Trigger(ctx, id))
		n, _ := store.GetNodeByPath(ctx, id, pathkey.Hash(id, "/proj/readme.md"))
		assert.NotNil(t, n)
		n, _ = store.GetNodeByPath(ctx, id, pathkey.Hash(id, "/proj/.git/HEAD"))
		assert.Nil(t, n, "the syncer kept walking with the pattern it started with")
	})
}

// The storage form draws the scan settings from the descriptor endpoint, next
// to — not inside — the driver's own fields, which the replication dialog
// also draws.
func TestAdmin_StorageDrivers_CarryTheScanFields(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	resp, err := client.Get(srv.URL + "/api/admin/storage-drivers")
	require.NoError(t, err)
	defer resp.Body.Close()
	var got []storage.Descriptor
	testutil.ReadJSON(t, resp, &got)
	require.NotEmpty(t, got)
	for _, d := range got {
		require.Len(t, d.ScanFields, 1, d.Driver)
		f := d.ScanFields[0]
		assert.Equal(t, storage.ScanExcludeKey, f.Key)
		assert.True(t, f.Multiline)
		assert.Equal(t, "storages.fields.scanExclude", f.I18nKey)
		assert.Equal(t, "storages.fieldHelp.scanExclude", f.HelpI18nKey)
		assert.Contains(t, f.Help, "not access control", "the help must say what the setting is not")
		for _, df := range d.Fields {
			assert.NotEqual(t, storage.ScanExcludeKey, df.Key, "%s: the scan setting leaked into the driver's own fields", d.Driver)
		}
	}
}
