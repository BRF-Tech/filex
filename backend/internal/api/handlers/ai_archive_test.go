package handlers_test

// Tests for the AI server-side archive surface: POST /api/ai/zip + /unzip.
// Exercises a zip→unzip round-trip (files + recursive folders) and the
// confinement ceiling (a root-scoped token cannot pack/extract outside its
// root). Runs against the real router built by aiFixture.

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// aiDownload fetches raw bytes from /api/ai/download.
func aiDownload(t *testing.T, client *http.Client, base, tok, path string) (int, []byte) {
	t.Helper()
	resp := aiReq(t, client, "GET", base+"/api/ai/download?path="+path, tok, nil)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestAI_ZipUnzip_RoundTrip(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	// Seed a file and a nested folder.
	for path, content := range map[string]string{
		"main://src/a.txt":     "AAA",
		"main://src/sub/b.txt": "BBB",
	} {
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{"path": path, "content": content})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// Zip a file + a folder (recursive) into out/archive.zip.
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/zip", tok, map[string]any{
		"sources": []string{"main://src/a.txt", "main://src/sub"},
		"dest":    "main://out/archive.zip",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var zres map[string]any
	testutil.ReadJSON(t, resp, &zres)
	resp.Body.Close()
	entry, _ := zres["entry"].(map[string]any)
	require.NotNil(t, entry)
	assert.Equal(t, "archive.zip", entry["name"])

	// Download the archive and confirm it is a real zip with both members.
	code, raw := aiDownload(t, client, srv.URL, tok, "main://out/archive.zip")
	require.Equal(t, http.StatusOK, code)
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	require.NoError(t, err, "dest must be a valid zip")
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	assert.True(t, names["a.txt"], "zip should contain a.txt — got %v", names)
	assert.True(t, names["sub/b.txt"], "zip should contain sub/b.txt (recursive) — got %v", names)

	// Unzip into a fresh dir and verify contents round-trip.
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/unzip", tok, map[string]any{
		"src":  "main://out/archive.zip",
		"dest": "main://restored",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var ures map[string]any
	testutil.ReadJSON(t, resp, &ures)
	resp.Body.Close()
	assert.Equal(t, float64(2), ures["extracted"], "two files extracted")

	code, got := aiDownload(t, client, srv.URL, tok, "main://restored/a.txt")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "AAA", string(got))
	code, got = aiDownload(t, client, srv.URL, tok, "main://restored/sub/b.txt")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "BBB", string(got))
}

func TestAI_Zip_ConfinementRejected(t *testing.T) {
	srv, client, store, adminTok := aiFixture(t)

	// Seed one file inside the tenant root, one outside it (via the full token).
	for _, p := range []string{"main://tenant/data.txt", "main://outside/secret.txt"} {
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", adminTok, map[string]any{"path": p, "content": "x"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	// A token confined to main://tenant (bound to a distinct user — aiFixture
	// already seeded the default admin).
	u, err := store.CreateUser(context.Background(), "tenant@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	conf := issueToken(t, store, u.ID, "read,write,delete,root:main://tenant", nil)

	// In-root zip works (bare relative paths resolve under the root).
	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/zip", conf, map[string]any{
		"sources": []string{"data.txt"},
		"dest":    "bundle.zip",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// A source OUTSIDE the root must be rejected (not packed).
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/zip", conf, map[string]any{
		"sources": []string{"main://outside/secret.txt"},
		"dest":    "leak.zip",
	})
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, "zipping outside the confinement root must fail")
	resp.Body.Close()

	// An unzip dest OUTSIDE the root must be rejected too.
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/unzip", conf, map[string]any{
		"src":  "bundle.zip",
		"dest": "main://outside",
	})
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, "extracting outside the confinement root must fail")
	resp.Body.Close()
}
