package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// H10: a sync client used to re-list every folder of its pair every 30
// seconds to find out that nothing had changed. `action=changes` answers that
// with one request: a cursor, and whether anything under the folder changed
// since the cursor the client sends back.
func TestManagerChanges_AnswersWhetherAFolderChanged(t *testing.T) {
	srv, client, _, _, _ := renameFixture(t)

	ask := func(folder, since string) (bool, string) {
		t.Helper()
		u := srv.URL + "/api/files/manager?action=changes&path=" + url.QueryEscape(folder) + "&since=" + url.QueryEscape(since)
		resp, err := client.Get(u)
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out struct {
			Cursor  string `json:"cursor"`
			Changed bool   `json:"changed"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		require.NotEmpty(t, out.Cursor)
		return out.Changed, out.Cursor
	}

	changed, cur := ask("files://Leon", "")
	require.True(t, changed, "no cursor yet: the client has never looked")
	changed, again := ask("files://Leon", cur)
	require.False(t, changed, "nothing happened since the cursor")
	require.Equal(t, cur, again)

	body, _ := json.Marshal(map[string]string{"path": "files://Leon", "item": "files://Leon/not.txt", "name": "note.txt"})
	resp, err := client.Post(srv.URL+"/api/files/manager?action=rename", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	changed, next := ask("files://Leon", cur)
	require.True(t, changed, "a rename inside the folder is a change")
	require.NotEqual(t, cur, next)
	changed, _ = ask("files://Leon/Belgeler", cur)
	require.False(t, changed, "the rename did not touch this subfolder")
}

func TestManagerChanges_RefusesDotDot(t *testing.T) {
	srv, client, _, _, _ := renameFixture(t)
	resp, err := client.Get(srv.URL + "/api/files/manager?action=changes&path=" + url.QueryEscape("files://Leon/../x"))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
