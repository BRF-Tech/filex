package handlers_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A move's answer describes the item where it now is - its size included.
//
// RED PROOF (task #119, measured on v0.48.1): `file_move` and POST
// /api/ai/move answered `"size": 0` for every file (a 5-byte file came back
// as 0 bytes). The entry was built from the paths alone; nothing asked the
// storage what had arrived. An agent that checks the size to confirm a move
// reads "the file is empty" after a move that worked.

func TestAI_Move_AnswerCarriesTheSize(t *testing.T) {
	srv, client, tok, _, _ := aiTwoStorageFixture(t, false)

	t.Run("within a storage, to another folder and name", func(t *testing.T) {
		aiPut(t, client, srv.URL, tok, "hot://a.txt", "12345")
		mk := aiReq(t, client, "POST", srv.URL+"/api/ai/mkdir", tok, map[string]any{"path": "hot://arsiv"})
		require.Equal(t, http.StatusOK, mk.StatusCode)
		mk.Body.Close()
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
			"src": "hot://a.txt", "dst": "hot://arsiv/b.txt",
		})
		body := readBody(t, resp)
		require.Equal(t, http.StatusOK, resp.StatusCode, body)
		entry, _ := body["entry"].(map[string]any)
		require.NotNil(t, entry, body)
		assert.Equal(t, "hot://arsiv/b.txt", entry["path"])
		assert.EqualValues(t, 5, entry["size"], "a 5-byte file is 5 bytes where it landed")
		assert.NotZero(t, entry["last_modified"], "and says when it was last written")
	})

	t.Run("across two storages", func(t *testing.T) {
		aiPut(t, client, srv.URL, tok, "hot://c.txt", "abc")
		resp := aiReq(t, client, "POST", srv.URL+"/api/ai/move", tok, map[string]any{
			"src": "hot://c.txt", "dst": "cold://c.txt",
		})
		body := readBody(t, resp)
		require.Equal(t, http.StatusOK, resp.StatusCode, body)
		entry, _ := body["entry"].(map[string]any)
		require.NotNil(t, entry, body)
		assert.EqualValues(t, 3, entry["size"])
	})

	t.Run("the MCP tool, which calls the same code", func(t *testing.T) {
		aiPut(t, client, srv.URL, tok, "hot://d.txt", "1234567")
		isErr, out, text := mcpToolCall(t, srv.URL, tok, "file_move", map[string]any{"src": "hot://d.txt", "dst": "hot://e.txt"})
		require.False(t, isErr, text)
		entry, _ := out["entry"].(map[string]any)
		require.NotNil(t, entry, text)
		assert.Equal(t, "hot://e.txt", entry["path"])
		assert.EqualValues(t, 7, entry["size"])
	})
}
