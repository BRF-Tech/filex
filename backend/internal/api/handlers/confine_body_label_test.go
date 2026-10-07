package handlers_test

// filex #156: confine.Middleware reads a confined call's body as JSON whatever
// its Content-Type, and passes the bytes of a file on as they were sent.
//
// Up to 0.52 it read a body only when the Content-Type said JSON. Every door
// had learnt to ask the root itself (GHSA-8gvc-6w52-6c7j), so a text/plain
// object outside the root was refused - but with the door's own answer, not
// the middleware's. And a body labelled JSON was cut at 8 MiB and its first
// object re-encoded, the bytes of a file PUT to an upload part or an app's
// save included: a .json saved through a confined call landed re-ordered,
// re-escaped or refused. Each test here is red on that code.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileBytes is a file whose bytes a re-encoding would change: keys out of
// order, an id no float64 holds, and text JSON escapes for HTML.
const fileBytes = `{"z":1,"path":"main://elsewhere","a":"<b>&</b>","id":9007199254740993}`

// rootReq is a request on the fixture's admin session, confined to the whole
// of main by X-Filex-Root, with the Content-Type it names.
func rootReq(t *testing.T, c *http.Client, method, u, contentType string, body []byte, hdr map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, u, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Filex-Root", "main://")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// A staged upload's part is the file's bytes, whatever the client labels it.
func TestConfineBodyLabel_AStagedPartIsStoredByteForByte(t *testing.T) {
	f := newStagedFixture(t)
	src := []byte(fileBytes)
	total := len(src)

	begin, _ := json.Marshal(map[string]any{
		"path": "main://", "name": "veri.json", "size": total, "chunk_size": total,
		"hash": "sha256:" + sha256Hex(src),
	})
	code, raw := rootReq(t, f.client, http.MethodPost, f.srv.URL+"/api/files/upload/begin", "application/json", begin, nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	var begun map[string]any
	require.NoError(t, json.Unmarshal(raw, &begun))
	id, _ := begun["id"].(string)
	require.NotEmpty(t, id)

	code, raw = rootReq(t, f.client, http.MethodPut, f.srv.URL+"/api/files/upload/"+id, "application/json", src,
		map[string]string{"Content-Range": fmt.Sprintf("bytes 0-%d/%d", total-1, total)})
	require.Equal(t, http.StatusOK, code, "a part labelled JSON is still the file's bytes: %s", raw)

	code, raw = rootReq(t, f.client, http.MethodPost, f.srv.URL+"/api/files/upload/"+id+"/commit", "application/json", nil, nil)
	require.Equal(t, http.StatusAccepted, code, string(raw))
	var committed map[string]any
	require.NoError(t, json.Unmarshal(raw, &committed))
	require.Equal(t, "ok", f.waitForOp(t, num(committed["op_id"])))

	landed, err := os.ReadFile(filepath.Join(f.rootDir, "veri.json"))
	require.NoError(t, err)
	assert.Equal(t, fileBytes, string(landed), "the file must land byte for byte")
}

// An app's interface saving a file: the body is the file.
func TestConfineBodyLabel_AnAppSaveIsStoredByteForByte(t *testing.T) {
	f := newAppFixture(t, nil)
	f.installUIApp(t)
	f.writeFile(t, "doc.sketch", "v1")

	u := f.srv.URL + "/api/files/plugins/ui/sketch/editor/save?path=" + url.QueryEscape("main://doc.sketch")
	code, raw := rootReq(t, f.admin, http.MethodPut, u, "application/json", []byte(fileBytes), nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	assert.Equal(t, fileBytes, f.readFile(t, "doc.sketch"), "the save must land byte for byte")

	// Larger than the 8 MiB the middleware reads of an object: not cut.
	big := `{"z":"` + string(bytes.Repeat([]byte("x"), 9<<20)) + `","a":1}`
	code, raw = rootReq(t, f.admin, http.MethodPut, u, "application/json", []byte(big), nil)
	require.Equal(t, http.StatusOK, code, string(raw))
	got := f.readFile(t, "doc.sketch")
	assert.Equal(t, len(big), len(got), "a 9 MiB save must land whole")
	assert.True(t, got == big, "a 9 MiB save must land byte for byte")
}

// An object outside the root gets the middleware's one answer on every door,
// whatever its Content-Type.
func TestConfineBodyLabel_AnObjectOutsideTheRootIsOneAnswer(t *testing.T) {
	f, tok := confinedFix(t)
	for _, door := range []struct{ route, body string }{
		{"/api/files/save-text", `{"path":"main://disari/gizli.txt","content":"x"}`},
		{"/api/files/manager?action=newfolder", `{"path":"main://disari","name":"yeni"}`},
		{"/api/files/archive/create", `{"sources":["main://disari/gizli.txt"],"dest":"main://kutu/p.zip"}`},
		{"/api/files/copy", `{"source":["main://disari/gizli.txt"],"target":"main://kutu"}`},
	} {
		wantCode, want := confRaw(t, f.URL, tok, http.MethodPost, door.route, "application/json", []byte(door.body))
		require.Equal(t, http.StatusForbidden, wantCode, "%s as JSON: %s", door.route, want)
		for _, ct := range []string{"text/plain", "", "application/octet-stream"} {
			code, raw := confRaw(t, f.URL, tok, http.MethodPost, door.route, ct, []byte(door.body))
			assert.Equal(t, wantCode, code, "%s as %q", door.route, ct)
			assert.Equal(t, want, raw, "%s as %q: the same answer, byte for byte", door.route, ct)
		}
	}
	settle()
	assert.Equal(t, "gizli", cdRead(t, f.RootMain, "disari/gizli.txt"))
	assert.False(t, confExists(f.RootMain, "disari/yeni"))
	assert.False(t, confExists(f.RootMain, "kutu/p.zip"))
	assert.False(t, confExists(f.RootMain, "kutu/gizli.txt"))
}

// An object larger than the middleware reads is refused, not passed on with
// keys it never saw - in any label.
func TestConfineBodyLabel_AnObjectTooLargeToHoldIsRefused(t *testing.T) {
	f, tok := confinedFix(t)
	body := []byte(`{"path":"main://kutu/ic.txt","content":"` + string(bytes.Repeat([]byte("x"), 9<<20)) + `"}`)
	for _, ct := range []string{"text/plain", "", "application/json"} {
		code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/save-text", ct, body)
		assert.Equal(t, http.StatusRequestEntityTooLarge, code, "as %q: %s", ct, raw)
	}
	assert.Equal(t, "icerde", cdRead(t, f.RootMain, "kutu/ic.txt"), "nothing was written")
}
