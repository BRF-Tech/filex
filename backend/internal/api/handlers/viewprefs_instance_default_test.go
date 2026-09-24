package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The operator's default folder view (`ui.default_folder_view`): what a folder
// opens as for a person who has neither changed that folder nor set a default
// of their own. The owner, 2026-09-21: "person AND instance default, the same
// model as the theme" — folder → person → instance → filex.
//
// What this pins:
//   - the value is validated on the way in (a view mode the explorer cannot
//     draw would reach every person who never chose one);
//   - it travels BESIDE the person's document on read, never inside it — the
//     client saves the document it read, so a default folded into `prefs`
//     would be written back as the person's own choice and the operator could
//     never change it for them again (the palette's defect this release).
func TestViewPrefs_InstanceDefault(t *testing.T) {
	srv, client, store := testutil.NewTestServer(t)
	email, pw := testutil.SeedAdmin(t, store)
	testutil.LoginAs(t, srv, client, email, pw)

	put := func(t *testing.T, url, body string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPut, url, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(raw)
	}
	read := func(t *testing.T) map[string]json.RawMessage {
		t.Helper()
		res, err := client.Get(srv.URL + "/api/files/manager/view-prefs")
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		out := map[string]json.RawMessage{}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		return out
	}
	setting := func(v string) string {
		b, _ := json.Marshal(map[string]string{"value": v})
		return string(b)
	}

	t.Run("nothing set: null, and the person's document is untouched", func(t *testing.T) {
		out := read(t)
		require.JSONEq(t, `null`, string(out["instance_default"]))
		require.JSONEq(t, `{}`, string(out["prefs"]))
	})

	t.Run("a valid default is stored normalised and served beside the document", func(t *testing.T) {
		code, body := put(t, srv.URL+"/api/admin/settings/ui.default_folder_view",
			setting(`{"v":"grid","k":"size","d":"desc","hidden":["owner","owner"],"junk":1}`))
		require.Equal(t, http.StatusOK, code, body)

		code, body = put(t, srv.URL+"/api/files/manager/view-prefs", `{"prefs":{"f":{},"u":1}}`)
		require.Equal(t, http.StatusOK, code, body)

		out := read(t)
		require.JSONEq(t, `{"v":"grid","k":"size","d":"desc","hidden":["owner"]}`, string(out["instance_default"]))
		// ⚠ The person's document is exactly what they saved — no `v`, no `k`.
		require.JSONEq(t, `{"f":{},"u":1}`, string(out["prefs"]))
	})

	t.Run("a view the explorer cannot draw is refused", func(t *testing.T) {
		for _, bad := range []string{
			`{"v":"tiles"}`,
			`{"k":"colour"}`,
			`{"k":"name","d":"sideways"}`,
			`{"hidden":["name"]}`,
			`not json`,
		} {
			code, body := put(t, srv.URL+"/api/admin/settings/ui.default_folder_view", setting(bad))
			require.Equal(t, http.StatusBadRequest, code, "%s → %s", bad, body)
		}
		// …and the batch door refuses it too, before writing anything.
		req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/admin/settings/",
			bytes.NewBufferString(`{"ui.default_folder_view":"{\"v\":\"tiles\"}"}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		require.NoError(t, err)
		res.Body.Close()
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
	})

	t.Run("clearing it answers null again", func(t *testing.T) {
		code, body := put(t, srv.URL+"/api/admin/settings/ui.default_folder_view", setting(``))
		require.Equal(t, http.StatusOK, code, body)
		require.JSONEq(t, `null`, string(read(t)["instance_default"]))
	})
}
