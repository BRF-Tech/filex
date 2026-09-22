package handlers_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The agent surface (`POST /api/ai/move`, and the MCP `file_move` tool that
// calls the same code) took `dst` literally and handed it to the driver, so a
// move or rename onto a name that was already taken replaced that file — the
// same hole the explorer's rename had. Across two storages the transfer wrote
// straight over the destination file as well.

func aiPut(t *testing.T, client *http.Client, base, tok, p, content string) {
	t.Helper()
	resp := aiReq(t, client, "POST", base+"/api/ai/upload", tok, map[string]any{"path": p, "content": content})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err, "nothing at %s", p)
	return string(b)
}

func TestAI_Move_OntoATakenName_IsRefused(t *testing.T) {
	srv, client, tok, hotDir, _ := aiTwoStorageFixture(t, false)
	aiPut(t, client, srv.URL, tok, "hot://a.txt", "incoming")
	aiPut(t, client, srv.URL, tok, "hot://b.txt", "was-here")

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://a.txt", "dst": "hot://b.txt",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusConflict, resp.StatusCode, body)
	assert.Contains(t, body["error"], "b.txt")
	assert.NotContains(t, body["error"], hotDir, "an error must not carry a server path")

	assert.Equal(t, "was-here", readFile(t, filepath.Join(hotDir, "b.txt")))
	assert.Equal(t, "incoming", readFile(t, filepath.Join(hotDir, "a.txt")))
}

func TestAI_Move_AcrossStorages_OntoATakenName_IsRefused(t *testing.T) {
	srv, client, tok, hotDir, coldDir := aiTwoStorageFixture(t, false)
	aiPut(t, client, srv.URL, tok, "hot://rapor.txt", "new")
	aiPut(t, client, srv.URL, tok, "cold://rapor.txt", "old")

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://rapor.txt", "dst": "cold://rapor.txt",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusConflict, resp.StatusCode, body)

	assert.Equal(t, "old", readFile(t, filepath.Join(coldDir, "rapor.txt")), "the transfer wrote over the destination")
	assert.Equal(t, "new", readFile(t, filepath.Join(hotDir, "rapor.txt")), "and the source must still be there")
}

// Moving a thing onto itself is not a collision: nothing changes, and it says
// so rather than failing.
func TestAI_Move_OntoItself_IsANoOp(t *testing.T) {
	srv, client, tok, hotDir, _ := aiTwoStorageFixture(t, false)
	aiPut(t, client, srv.URL, tok, "hot://a.txt", "stay")

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://a.txt", "dst": "hot://a.txt",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Equal(t, "stay", readFile(t, filepath.Join(hotDir, "a.txt")))
}

// A rename to a free name, and a case-only rename, still work.
func TestAI_Move_ToAFreeName_AndCaseOnly_StillWork(t *testing.T) {
	srv, client, tok, hotDir, _ := aiTwoStorageFixture(t, false)
	aiPut(t, client, srv.URL, tok, "hot://a.txt", "content")

	resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://a.txt", "dst": "hot://c.txt",
	})
	body := readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	assert.Equal(t, "content", readFile(t, filepath.Join(hotDir, "c.txt")))

	resp = aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
		"src": "hot://c.txt", "dst": "hot://C.txt",
	})
	body = readBody(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode, body)
	entries, err := os.ReadDir(hotDir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if e.Name()[0] != '.' {
			names = append(names, e.Name())
		}
	}
	assert.Equal(t, []string{"C.txt"}, names)
}
