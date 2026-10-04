package handlers_test

// A `root:` token stays in its folder however the request body is sent
// (2026-10-04).
//
// confine.Middleware confines a request by reading its body the way it expects
// a client to send it: one JSON object, labelled as JSON, with a path in every
// field the handler reads. The handlers read it more loosely: json.Decoder
// takes the FIRST value and ignores what follows it and the Content-Type; the
// explorer's upload takes `path` from a multipart field; and a request with no
// path at all means the storage root. Each test sends an out-of-root request
// in one of those other shapes and is red while it is accepted and something
// appears outside the root.

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// confRaw sends bytes as they are, with the Content-Type the caller names (none
// when empty).
func confRaw(t *testing.T, base, tok, method, p, contentType string, body []byte) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, base+p, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("X-Filex-Token", tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// confMultipart builds a multipart/form-data body: the fields, then one file.
func confMultipart(t *testing.T, fields map[string]string, fileName, content string) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	if fileName != "" {
		fw, err := mw.CreateFormFile("file[]", fileName)
		require.NoError(t, err)
		_, err = fw.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	return mw.FormDataContentType(), buf.Bytes()
}

// confAnywhere reports whether rel exists under any of the fixture's storages.
func confAnywhere(f *doorFix, rel string) bool {
	for _, root := range []string{f.RootMain, f.RootYan, f.RootRbac} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			return true
		}
	}
	return false
}

func confExists(root, rel string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// The same new-folder request outside the root, sent in every shape the
// handler reads and the middleware did not.
func TestConfineBody_AFolderIsNotMadeOutsideTheRoot(t *testing.T) {
	f, tok := confinedFix(t)

	jsonOf := func(name string) string {
		return fmt.Sprintf(`{"path":"main://disari","name":%q}`, name)
	}
	shapes := []struct {
		label, name, contentType, body string
	}{
		{"JSON, then more bytes", "a1", "application/json", jsonOf("a1") + ` trailing`},
		{"JSON, then a second object", "a2", "application/json", jsonOf("a2") + `{"path":"main://kutu"}`},
		{"JSON, then a large tail", "a3", "application/json", jsonOf("a3") + strings.Repeat("x", 9<<20)},
		{"JSON labelled text", "b1", "text/plain", jsonOf("b1")},
		{"JSON with no Content-Type", "b2", "", jsonOf("b2")},
		{"JSON labelled as a binary stream", "b3", "application/octet-stream", jsonOf("b3")},
		{"JSON labelled as a multipart form", "b4", "multipart/form-data; boundary=zz", jsonOf("b4")},
		{"JSON labelled as a form", "b5", "application/x-www-form-urlencoded", jsonOf("b5")},
	}
	for _, s := range shapes {
		code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=newfolder", s.contentType, []byte(s.body))
		assert.GreaterOrEqual(t, code, 400, "%s: %s", s.label, raw)
		assert.False(t, confExists(f.RootMain, "disari/"+s.name), "%s: %q must not exist outside the root", s.label, s.name)
	}
}

// A request that names no path means the storage root to the handler. For a
// confined token the root is its folder, never the storage.
func TestConfineBody_NoPathIsTheRootNotTheStorage(t *testing.T) {
	f, tok := confinedFix(t)

	for _, s := range []struct{ label, name, body string }{
		{"no path key", "n1", `{"name":"n1"}`},
		{"empty path", "n2", `{"path":"","name":"n2"}`},
		{"a path of another key only", "n3", `{"item":"main://kutu","name":"n3"}`},
	} {
		code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=newfolder", "application/json", []byte(s.body))
		// Refused, or made inside the folder: both keep the root; what must
		// not happen is a folder at the top of a storage.
		_ = code
		assert.False(t, confAnywhere(f, s.name), "%s: %q must not be made at the top of a storage (%d %s)", s.label, s.name, code, raw)
	}
}

// The explorer's upload reads its destination from a multipart form field,
// which the middleware did not look at.
func TestConfineBody_AMultipartFieldIsConfined(t *testing.T) {
	f, tok := confinedFix(t)

	up := func(fields map[string]string, query, file string) (int, string) {
		ct, body := confMultipart(t, fields, file, "icerik")
		return confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=upload"+query, ct, body)
	}

	// Out of the root, by the field alone.
	code, raw := up(map[string]string{"path": "main://disari"}, "", "u1.txt")
	assert.GreaterOrEqual(t, code, 400, raw)
	assert.False(t, confExists(f.RootMain, "disari/u1.txt"), "the field must not place the file outside the root")

	// Out of the root, by the field, behind a query that is inside it.
	code, raw = up(map[string]string{"path": "main://disari"}, "&path=main://kutu", "u2.txt")
	assert.False(t, confExists(f.RootMain, "disari/u2.txt"), "a query inside the root must not shield the field (%d %s)", code, raw)

	// No destination at all: the storage root to the handler, the folder to a
	// confined token.
	code, raw = up(nil, "", "u3.txt")
	assert.False(t, confAnywhere(f, "u3.txt"), "an upload with no path must not land at the top of a storage (%d %s)", code, raw)

	// Inside the root it still works.
	code, raw = up(map[string]string{"path": "main://kutu"}, "", "u4.txt")
	assert.Equal(t, http.StatusOK, code, raw)
	assert.True(t, confExists(f.RootMain, "kutu/u4.txt"), "an upload into the folder must still work")
}

// What a confined token may do, it still may: the fixes above are about the
// shape of the body, not about what is allowed.
func TestConfineBody_InsideTheRootStillWorks(t *testing.T) {
	f, tok := confinedFix(t)

	for _, s := range []struct{ label, contentType, body, name string }{
		{"plain JSON", "application/json", `{"path":"main://kutu","name":"ok1"}`, "ok1"},
		{"a charset on the type", "application/json; charset=utf-8", `{"path":"main://kutu","name":"ok2"}`, "ok2"},
		{"a trailing newline", "application/json", "{\"path\":\"main://kutu\",\"name\":\"ok3\"}\n", "ok3"},
		{"the path without its storage", "application/json", `{"path":"kutu","name":"ok4"}`, "ok4"},
	} {
		code, raw := confRaw(t, f.URL, tok, http.MethodPost, "/api/files/manager?action=newfolder", s.contentType, []byte(s.body))
		assert.Equal(t, http.StatusOK, code, "%s: %s", s.label, raw)
		assert.True(t, confExists(f.RootMain, "kutu/"+s.name), "%s: the folder must be made inside the root", s.label)
	}
}
