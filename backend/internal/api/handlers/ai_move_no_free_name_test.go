package handlers_test

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An agent's move lands beside a taken name (`b-copy.txt`, `b-copy-2.txt`, up
// to `b-copy-100.txt`, ops.MoveDest). When every one of those is taken as
// well, the move is refused and nothing moves. docs/MCP.md has always said
// that refusal is `409 NO_FREE_NAME`, as the explorer's move answers it; the
// /api/ai surface answered 500 with no code (mapDriverErr found no "exists"
// in the text), which tells a client to retry, and the MCP tool said only the
// sentence. Task #116.

// fillEveryFreeName takes b.txt and every name MoveDest would offer beside it.
func fillEveryFreeName(t *testing.T, dir string) {
	t.Helper()
	write := func(name string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("taken"), 0o644))
	}
	write("b.txt")
	write("b-copy.txt")
	for i := 2; i <= 100; i++ {
		write(fmt.Sprintf("b-copy-%d.txt", i))
	}
}

func TestAI_Move_NoFreeNameLeft_Is409WithCode(t *testing.T) {
	srv, client, tok, hotDir, coldDir := aiTwoStorageFixture(t, false)
	aiPut(t, client, srv.URL, tok, "hot://a.txt", "incoming")

	t.Run("same storage", func(t *testing.T) {
		fillEveryFreeName(t, hotDir)
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
			"src": "hot://a.txt", "dst": "hot://b.txt",
		})
		body := readBody(t, resp)
		assert.Equal(t, http.StatusConflict, resp.StatusCode, body)
		assert.Equal(t, "NO_FREE_NAME", body["code"], body)
		assert.Equal(t, "incoming", readFile(t, filepath.Join(hotDir, "a.txt")), "nothing moved")
		assert.Equal(t, "taken", readFile(t, filepath.Join(hotDir, "b.txt")))
	})

	t.Run("across storages", func(t *testing.T) {
		fillEveryFreeName(t, coldDir)
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
			"src": "hot://a.txt", "dst": "cold://b.txt",
		})
		body := readBody(t, resp)
		assert.Equal(t, http.StatusConflict, resp.StatusCode, body)
		assert.Equal(t, "NO_FREE_NAME", body["code"], body)
		assert.Equal(t, "incoming", readFile(t, filepath.Join(hotDir, "a.txt")), "the source stays")
	})

	t.Run("the MCP tool leads with the code", func(t *testing.T) {
		payload := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"file_move","arguments":{"src":"hot://a.txt","dst":"hot://b.txt"}}}`
		req, _ := http.NewRequest("POST", srv.URL+"/api/ai/mcp", strings.NewReader(payload))
		req.Header.Set("X-Filex-Token", tok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := client.Do(req)
		require.NoError(t, err)
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, string(b))
		assert.Contains(t, string(b), `"isError":true`, string(b))
		assert.Contains(t, string(b), "NO_FREE_NAME: ", string(b))
		assert.Equal(t, "incoming", readFile(t, filepath.Join(hotDir, "a.txt")), "nothing moved")
	})
}
