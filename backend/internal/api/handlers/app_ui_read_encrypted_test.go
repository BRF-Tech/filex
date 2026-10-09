package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sec055 S7: an app's interface is never handed the bytes of a file the
// server holds only as ciphertext - one in an encrypted folder, or a single
// encrypted file (`.fxe`) - the same `403 encrypted` its action, screen call
// and save get. Red before: the read went through the preview route and
// answered 200 with the ciphertext.
func TestAppUIRead_NeverHandsAnAppCiphertext(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	f.writeFile(t, "doc.sketch", "v1")
	f.markEncrypted(t, f.st, f.root, "kasa")
	f.writeFile(t, "kasa/doc.sketch", "filexe2e-ciphertext-in-a-folder")
	f.writeFile(t, "tek.sketch.fxe", "filexfxe-ciphertext-on-its-own")

	get := func(p string) (int, string) {
		res, err := f.admin.Get(f.srv.URL + "/api/files/plugins/ui/sketch/editor/read?path=" + url.QueryEscape(p))
		require.NoError(t, err)
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	code, body := get("main://doc.sketch")
	require.Equal(t, http.StatusOK, code, body)
	assert.Equal(t, "v1", body, "a plain file is read as before")

	for p, rel := range map[string]string{"main://kasa/doc.sketch": "kasa/doc.sketch", "main://tek.sketch.fxe": "tek.sketch.fxe"} {
		code, body = get(p)
		require.Equal(t, http.StatusForbidden, code, "%s: %s", p, body)
		assert.NotContains(t, body, "ciphertext", "%s: no byte of it", p)
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(body), &m), body)
		assert.Equal(t, "encrypted", m["error"], "%s: the code every app door answers", p)
		msg, _ := m["message"].(string)
		assert.Contains(t, msg, rel, "%s: the server's sentence names the file", p)
	}
}
