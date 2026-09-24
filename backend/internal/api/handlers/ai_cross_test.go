package handlers_test

// The agent surface moving a file BETWEEN storages.
//
// `POST /api/ai/move` used to answer "cross-storage move not supported" — an
// honest refusal, but a refusal: an agent asked to tidy files from one depo
// into another had to fall back to read-all-bytes-and-write-them-back, through
// its own context. It now runs the same engine the queue uses for a
// cross-storage paste (`ops.Transfer`), which is the point: one implementation
// of "carry a tree between two drivers", not one per surface.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/capability"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	syncpkg "github.com/brf-tech/filex/backend/internal/sync"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// aiTwoStorageFixture is aiFixture with a second storage ("cold"), so the two
// ends of a move can live apart.
func aiTwoStorageFixture(t *testing.T, coldReadOnly bool) (*httptest.Server, *http.Client, string, string, string) {
	t.Helper()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	hotDir, coldDir := t.TempDir(), t.TempDir()

	hotDrv, coldDrv := &local.Driver{}, &local.Driver{}
	require.NoError(t, hotDrv.Init(ctx, map[string]any{"root": hotDir}))
	require.NoError(t, coldDrv.Init(ctx, map[string]any{"root": coldDir}))

	mk := func(name, dir string, ro bool) *model.Storage {
		st, err := store.CreateStorage(ctx, &model.Storage{
			Name: name, Driver: "local", MountPath: "/" + name, Enabled: true, ReadOnly: ro,
			ConfigJSON: json.RawMessage(`{"root":"` + strings.ReplaceAll(dir, `\`, `\\`) + `"}`),
		})
		require.NoError(t, err)
		return st
	}
	hot := mk("hot", hotDir, false)
	cold := mk("cold", coldDir, coldReadOnly)

	resolver := func(id int64) (storage.Driver, error) {
		switch id {
		case hot.ID:
			return hotDrv, nil
		case cold.ID:
			return coldDrv, nil
		}
		return nil, fmt.Errorf("unknown id %d", id)
	}

	localDrv := authlocal.New(store)
	require.NoError(t, localDrv.Init(ctx, nil))
	auth.SetEnabled([]auth.Driver{localDrv})

	cfg := config.Default()
	cfg.PublicURL = "http://test.local"
	cfg.CORS.AllowedOrigins = []string{"*"}

	srv := httptest.NewServer(api.BuildRouter(&api.Deps{
		Cfg:             cfg,
		Store:           store,
		Worker:          syncpkg.New(store),
		Caps:            capability.New(store),
		Share:           share.NewService(store),
		StorageResolver: resolver,
		LocalAuth:       localDrv,
	}))
	t.Cleanup(srv.Close)

	uid, _ := testutil.SeedAdminUser(t, store)
	tok := issueToken(t, store, uid, fullScopes, nil)
	return srv, &http.Client{}, tok, hotDir, coldDir
}

func TestAI_Move_AcrossStorages_CarriesTheBytesAndRemovesTheSource(t *testing.T) {
	srv, client, tok, hotDir, coldDir := aiTwoStorageFixture(t, false)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "hot://rapor.txt", "content": "veri",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://rapor.txt", "dst": "cold://arsiv/rapor.txt",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	entry, _ := body["entry"].(map[string]any)
	assert.Equal(t, "cold://arsiv/rapor.txt", entry["path"], "the answer names where it now lives")
	assert.Equal(t, "file", entry["type"], "a file crossed, so the answer still says file")

	landed, err := os.ReadFile(filepath.Join(coldDir, "arsiv", "rapor.txt"))
	require.NoError(t, err, "the bytes must be in the OTHER storage")
	assert.Equal(t, "veri", string(landed))

	_, err = os.Stat(filepath.Join(hotDir, "rapor.txt"))
	assert.True(t, os.IsNotExist(err), "the source goes away once the copy is verified")

	// And the agent's own view agrees: the old path is gone, the new one is there.
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=hot://rapor.txt", tok, nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
	resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path=cold://arsiv/rapor.txt", tok, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func TestAI_Move_AcrossStorages_RefusesAReadOnlyTarget(t *testing.T) {
	srv, client, tok, hotDir, coldDir := aiTwoStorageFixture(t, true)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "hot://kalsin.txt", "content": "burada",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://kalsin.txt", "dst": "cold://kalsin.txt",
	})
	assert.GreaterOrEqual(t, resp.StatusCode, 400, readBody(t, resp))

	// The refusal must cost nothing: source intact, target untouched.
	body, err := os.ReadFile(filepath.Join(hotDir, "kalsin.txt"))
	require.NoError(t, err)
	assert.Equal(t, "burada", string(body))
	entries, err := os.ReadDir(coldDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestAI_Move_AcrossStorages_CarriesAWholeFolder(t *testing.T) {
	srv, client, tok, hotDir, coldDir := aiTwoStorageFixture(t, false)

	for _, f := range []struct{ path, body string }{
		{"hot://proje/README.md", "# proje"},
		{"hot://proje/src/main.go", "package main"},
	} {
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
			"path": f.path, "content": f.body,
		})
		require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
	}

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://proje", "dst": "cold://arsiv/proje",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	// ⚠ What crossed was a TREE, and the answer has to say so. This return
	// hard-coded `Type: "file"`, so an agent that had just moved a folder was
	// told it now holds a file — and would go on to call file_read on it.
	entry, _ := body["entry"].(map[string]any)
	assert.Equal(t, "dir", entry["type"], "a folder crossed, so the answer says folder")

	for rel, want := range map[string]string{
		filepath.Join("arsiv", "proje", "README.md"):      "# proje",
		filepath.Join("arsiv", "proje", "src", "main.go"): "package main",
	} {
		got, err := os.ReadFile(filepath.Join(coldDir, rel))
		require.NoError(t, err, rel)
		assert.Equal(t, want, string(got), rel)
	}
	_, err := os.Stat(filepath.Join(hotDir, "proje"))
	assert.True(t, os.IsNotExist(err), "the whole tree moved, so nothing stays behind")
}

// TestAI_Move_AcrossStorages_DoesNotOverwriteWhatIsAlreadyThere — the same
// gesture, aimed at a folder that already holds that name.
//
// ⚠⚠ Until 2026-09-20 `moveAcross` handed the caller's `dst` straight to
// `ops.Transfer`, and Transfer writes exactly where it is told: the resident
// file was overwritten by the arriving bytes and the source was then deleted.
// Two files in, one file out — and no trash copy of the loser, because nothing
// trashed it; the only surface in the product where "move this there" destroyed
// data was the one an agent drives unattended. Every other move de-collides
// (the queued cross-depo transfer has called `ops.UniqueDest` since the day it
// was written), so this one does too: both files survive, and the answer names
// the free name the bytes actually landed on.
func TestAI_Move_AcrossStorages_DoesNotOverwriteWhatIsAlreadyThere(t *testing.T) {
	srv, client, tok, hotDir, coldDir := aiTwoStorageFixture(t, false)

	for _, f := range []struct{ path, body string }{
		{"hot://rapor.txt", "yeni"},
		{"cold://arsiv/rapor.txt", "eski"},
	} {
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
			"path": f.path, "content": f.body,
		})
		require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
	}

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://rapor.txt", "dst": "cold://arsiv/rapor.txt",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	entry, _ := body["entry"].(map[string]any)
	assert.Equal(t, "cold://arsiv/rapor-copy.txt", entry["path"],
		"the answer must name where the bytes REALLY are; told the path it asked for, the agent sends its user to somebody else's file")
	assert.Equal(t, "rapor-copy.txt", entry["name"])
	assert.Equal(t, "file", entry["type"])

	resident, err := os.ReadFile(filepath.Join(coldDir, "arsiv", "rapor.txt"))
	require.NoError(t, err, "the file that was already there must still be there")
	assert.Equal(t, "eski", string(resident), "…and with its own bytes: nothing wrote over it")

	arrived, err := os.ReadFile(filepath.Join(coldDir, "arsiv", "rapor-copy.txt"))
	require.NoError(t, err, "the moved file lands beside it, on a free name")
	assert.Equal(t, "yeni", string(arrived))

	_, err = os.Stat(filepath.Join(hotDir, "rapor.txt"))
	assert.True(t, os.IsNotExist(err), "it is still a MOVE: the source goes once the copy is verified")

	// The agent's own view of both depots agrees with the disk.
	for _, p := range []string{"cold://arsiv/rapor.txt", "cold://arsiv/rapor-copy.txt"} {
		resp = aiReq(t, client, "GET", srv.URL+"/api/ai/info?path="+p, tok, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode, p)
		resp.Body.Close()
	}
}

// TestAI_Move_AcrossStorages_KeepsTheSourceWhenALinkCannotBeCarried — the
// agent's arm of the rule in ops/cross.go (Skipped): a link the source driver
// cannot follow is left behind and named, and a move deletes only what it
// carried, so the source stays.
//
// ⚠ Before the fix this request answered an error for the whole folder — the
// transfer died reading the link — and the copy it had already started stayed
// at the destination. The answer is a SUCCESS now, because the copy happened:
// an agent told "error" retries, and the retry lands a second copy on a free
// name beside the first.
func TestAI_Move_AcrossStorages_KeepsTheSourceWhenALinkCannotBeCarried(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Symlink needs Developer Mode on Windows; the suite runs this on Linux")
	}
	srv, client, tok, hotDir, coldDir := aiTwoStorageFixture(t, false)
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "hot://proje/README.md", "content": "# proje",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, readBody(t, resp))
	require.NoError(t, os.Symlink("../yok", filepath.Join(hotDir, "proje", "kirik")))

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://proje", "dst": "cold://proje",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	entry, _ := body["entry"].(map[string]any)
	assert.Equal(t, "cold://proje", entry["path"])
	assert.Equal(t, true, entry["source_kept"], "the answer must say the source is still there")
	left, _ := entry["left_behind"].([]any)
	require.Len(t, left, 1, body)
	assert.Equal(t, map[string]any{"path": "hot://proje/kirik", "reason": "broken"}, left[0])

	got, err := os.ReadFile(filepath.Join(coldDir, "proje", "README.md"))
	require.NoError(t, err, "the copy is complete without the link")
	assert.Equal(t, "# proje", string(got))
	_, err = os.Stat(filepath.Join(hotDir, "proje", "README.md"))
	assert.NoError(t, err, "nothing was deleted: the source stays whole")
}
