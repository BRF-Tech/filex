package handlers_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAIUpload_RefusesFolderTarget — passing a FOLDER as `path` used to write a
// single file at that exact key. On a filesystem the OS refuses, which is why
// this never showed locally; on an object store the write SUCCEEDS and leaves
// `X` and `X/…` living side by side.
//
// What that cost (2026-08-06, brkip DR mirror): the mirror could never settle
// the prefix, so `mc mirror` re-copied it every run — 2760 syncs in 24h, 1016
// versions of one PNG, a 43 MiB folder occupying 45 GB, disk at 96%. Quieter
// and worse: the colliding object made everything beneath it unlistable, so 314
// objects had no DR backup and nothing said so.
func TestAIUpload_RefusesFolderTarget(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/mkdir", tok, map[string]any{"path": "main://reports"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// The exact mistake: `path` names the folder, not a file inside it.
	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://reports", "content": "boom",
	})
	defer resp.Body.Close()
	require.Equal(t, http.StatusConflict, resp.StatusCode,
		"writing a file onto an existing folder must be refused with 409")

	// The folder must still be a folder, and still listable.
	ls := aiReq(t, client, "GET", srv.URL+"/api/ai/files?path=main://reports", tok, nil)
	defer ls.Body.Close()
	require.Equal(t, http.StatusOK, ls.StatusCode, "the folder must survive the refused write")
}

// TestAIMkdir_RefusesFileTarget — the same collision approached from the other
// side: a folder created on top of an existing file name.
func TestAIMkdir_RefusesFileTarget(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://archive", "content": "i am a file",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/mkdir", tok, map[string]any{"path": "main://archive"})
	defer resp.Body.Close()
	require.Equal(t, http.StatusConflict, resp.StatusCode,
		"a folder created onto an existing file must be refused with 409")

	// The file must be untouched — still readable, still its original bytes.
	rd := aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://archive", tok, nil)
	defer rd.Body.Close()
	require.Equal(t, http.StatusOK, rd.StatusCode)
	body, err := io.ReadAll(rd.Body)
	require.NoError(t, err)
	require.Equal(t, "i am a file", string(body),
		"the refused mkdir must not have disturbed the file")
}

// TestAIUpload_NormalWritesStillWork — the guard must not cost the ordinary
// cases: a fresh path, and overwriting an existing file.
func TestAIUpload_NormalWritesStillWork(t *testing.T) {
	srv, client, _, tok := aiFixture(t)

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://docs/a.txt", "content": "first",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/upload", tok, map[string]any{
		"path": "main://docs/a.txt", "content": "second",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, "overwriting a file is not a conflict")
	resp.Body.Close()

	rd := aiReq(t, client, "GET", srv.URL+"/api/ai/download?path=main://docs/a.txt", tok, nil)
	defer rd.Body.Close()
	body, err := io.ReadAll(rd.Body)
	require.NoError(t, err)
	require.Equal(t, "second", string(body))
}
