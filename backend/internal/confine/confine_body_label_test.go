package confine

// A confined call's body is held to the root whatever its Content-Type
// (filex #156, the class of GHSA-8gvc-6w52-6c7j).
//
// Middleware read a body only when its Content-Type contained "json", while
// the handlers decode their body with json.Decoder whatever the label: the
// same object sent as text/plain (or with no Content-Type) reached them as the
// client wrote it. Each handler now asks the root itself, and this layer is
// the one that catches the handler that forgets to. It also cut every body
// labelled JSON at 8 MiB and wrote back its first object re-encoded - the
// bytes of a file PUT to an upload part or an app's save included.
//
// These tests use only what the package exported before the change, so they
// compile against the old code and are red there.

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// handed is what the handler behind Middleware was given.
type handed struct {
	ran  bool
	body []byte
	cl   int64
}

func behindMiddleware() (http.Handler, *handed) {
	got := &handed{}
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.ran = true
		got.cl = r.ContentLength
		if r.Body != nil {
			got.body, _ = io.ReadAll(r.Body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	return h, got
}

const acme = "read,write,delete,root:main://projeler/acme"

func bodyReq(scopes, method, target, contentType string, body []byte) *http.Request {
	r := httptest.NewRequest(method, target, bytes.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if scopes == "" {
		return r
	}
	return r.WithContext(auth.WithToken(r.Context(), &model.APIToken{Scopes: scopes}))
}

// serve runs one request through a fresh Middleware.
func serve(r *http.Request) (*httptest.ResponseRecorder, *handed) {
	h, got := behindMiddleware()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec, got
}

// labels are the Content-Types a client may put on a body; the handlers read
// it as JSON under every one of them.
var labels = []string{
	"application/json",
	"text/plain",
	"",
	"application/octet-stream",
	"application/x-www-form-urlencoded",
	"multipart/form-data; boundary=x",
}

// methods are the calls that carry a body, each on a route of its kind.
var methods = []struct{ method, target string }{
	{http.MethodPost, "/api/files/manager?action=newfolder"},
	{http.MethodPatch, "/api/files/permissions/7"},
	{http.MethodDelete, "/api/files/share/7"},
	{http.MethodPut, "/api/files/manager/view-prefs"},
	{http.MethodGet, "/api/files/manager?action=index"},
}

const outsideAnswer = `{"error":"path outside confined root"}`

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestMiddleware_AnObjectIsHeldWhateverItsLabel(t *testing.T) {
	for _, m := range methods {
		for _, ct := range labels {
			rec, got := serve(bodyReq(acme, m.method, m.target, ct, []byte(`{"path":"main://projeler/EVIL","name":"n"}`)))
			assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s as %q", m.method, m.target, ct)
			assert.Equal(t, outsideAnswer, rec.Body.String(), "%s as %q: the one answer", m.method, ct)
			assert.False(t, got.ran, "%s as %q: the handler must not run", m.method, ct)

			// A key in another case and a tail after the object, as in JSON.
			rec, got = serve(bodyReq(acme, m.method, m.target, ct, []byte(`{"PATH":"main://projeler/EVIL"} tail`)))
			assert.Equal(t, http.StatusForbidden, rec.Code, "%s as %q, PATH with a tail", m.method, ct)
			assert.False(t, got.ran)

			// Inside the root the body is rewritten as a JSON one is: a bare
			// path is qualified on the confined storage.
			rec, got = serve(bodyReq(acme, m.method, m.target, ct, []byte(" \r\n\t"+`{"path":"projeler/acme/sub","name":"n"}`)))
			require.Equal(t, http.StatusOK, rec.Code, "%s as %q inside the root: %s", m.method, ct, rec.Body.String())
			require.True(t, got.ran)
			assert.JSONEq(t, `{"path":"main://projeler/acme/sub","name":"n"}`, string(got.body), "%s as %q", m.method, ct)
			assert.EqualValues(t, len(got.body), got.cl, "Content-Length follows the rewritten body")
		}
	}
}

// The X-Filex-Root header confines as a token's root does.
func TestMiddleware_AHeaderRootHoldsAPlainBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/files/save-text", strings.NewReader(`{"path":"main://projeler/EVIL/a.txt","content":"x"}`))
	r.Header.Set("Content-Type", "text/plain")
	r.Header.Set("X-Filex-Root", "main://projeler/acme")
	rec, got := serve(r)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, got.ran)
}

// The two routes whose body is the bytes of a file pass it on as it was sent:
// not cut at 8 MiB, not re-encoded, whatever the label says.
func TestMiddleware_AFileBodyPassesByteForByte(t *testing.T) {
	small := []byte(`{"z":1,"path":"main://projeler/EVIL","a":"<b>&</b>","n":9007199254740993}`)
	big := cat([]byte(`{"z":1,"path":"main://projeler/EVIL","a":"`), bytes.Repeat([]byte("x"), 9<<20), []byte(`"}`))
	save := "/api/files/plugins/ui/sketch/editor/save?path=" + url.QueryEscape("main://projeler/acme/a.sketch")
	for _, target := range []string{"/api/files/upload/0a1b2c3d", save, "/api/files/plugins/ui/sketch/editor/save?session=abc&offset=0"} {
		for name, body := range map[string][]byte{"small": small, "9 MiB": big} {
			for _, ct := range []string{"application/json", "text/plain", "application/octet-stream", ""} {
				rec, got := serve(bodyReq(acme, http.MethodPut, target, ct, body))
				require.Equal(t, http.StatusOK, rec.Code, "%s %s as %q: %s", target, name, ct, rec.Body.String())
				assert.True(t, bytes.Equal(body, got.body), "%s: the %s body as %q must reach the handler byte for byte (got %d bytes)", target, name, ct, len(got.body))
				assert.EqualValues(t, len(body), got.cl, "%s %s as %q", target, name, ct)
			}
		}
	}
	// The query is still held on those routes.
	rec, got := serve(bodyReq(acme, http.MethodPut, "/api/files/plugins/ui/sketch/editor/save?path="+url.QueryEscape("main://projeler/EVIL/a.sketch"), "application/octet-stream", small))
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, got.ran)
}

// Only those two routes, spelled exactly, with PUT: anything else that looks
// like them is held like any other body.
func TestMiddleware_OnlyTheListedFileRoutesAreExempt(t *testing.T) {
	out := []byte(`{"path":"main://projeler/EVIL"}`)
	for _, c := range []struct{ method, target string }{
		{http.MethodPost, "/api/files/upload/0a1b2c3d"},
		{http.MethodPatch, "/api/files/upload/0a1b2c3d"},
		{http.MethodPut, "/api/files/upload/0a1b2c3d/commit"},
		{http.MethodPut, "/api/files/upload/"},
		{http.MethodPut, "/api/files/upload"},
		{http.MethodPut, "/api/files/upload/a/../../save-text"},
		{http.MethodPut, "/api/files//upload/0a1b2c3d"},
		{http.MethodPut, "/api/files/Upload/0a1b2c3d"},
		{http.MethodPut, "/api/me/upload/0a1b2c3d"},
		{http.MethodPost, "/api/files/plugins/ui/sketch/editor/save"},
		{http.MethodPut, "/api/files/plugins/ui/sketch/editor/call"},
		{http.MethodPut, "/api/files/plugins/ui/sketch/save"},
		{http.MethodPut, "/api/files/plugins/ui/sketch/editor/save/x"},
		{http.MethodPut, "/api/files/plugins/ui//editor/save"},
	} {
		for _, ct := range []string{"application/octet-stream", "application/json"} {
			rec, got := serve(bodyReq(acme, c.method, c.target, ct, out))
			assert.Equal(t, http.StatusForbidden, rec.Code, "%s %s as %q", c.method, c.target, ct)
			assert.False(t, got.ran, "%s %s as %q", c.method, c.target, ct)
		}
	}
}

// A multipart form needs no entry in that list: it begins with its boundary,
// never with an object, so it passes untouched - and a JSON object labelled
// multipart is still held (TestMiddleware_AnObjectIsHeldWhateverItsLabel).
func TestMiddleware_AMultipartFormPassesByteForByte(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("path", "main://projeler/acme"))
	fw, err := mw.CreateFormFile("file[]", "a.json")
	require.NoError(t, err)
	_, _ = fw.Write([]byte(`{"z":1,"a":"<b>"}`))
	_, _ = fw.Write(bytes.Repeat([]byte("x"), 9<<20))
	require.NoError(t, mw.Close())
	body := buf.Bytes()

	rec, got := serve(bodyReq(acme, http.MethodPost, "/api/files/manager?action=upload&path="+url.QueryEscape("main://projeler/acme"), mw.FormDataContentType(), body))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, bytes.Equal(body, got.body), "the form must reach the handler byte for byte (got %d of %d bytes)", len(got.body), len(body))
	assert.EqualValues(t, len(body), got.cl)
}

// A body that begins as an object and is larger than this layer reads is
// refused, not passed on: past the cut its keys were never seen.
func TestMiddleware_AnObjectTooLargeToHoldIsRefused(t *testing.T) {
	inside := cat([]byte(`{"path":"main://projeler/acme/a.txt","content":"`), bytes.Repeat([]byte("x"), 9<<20), []byte(`"}`))
	// The path after the cut.
	late := cat([]byte(`{"content":"`), bytes.Repeat([]byte("x"), 9<<20), []byte(`","path":"main://projeler/EVIL/a.txt"}`))
	// White space before the object is read as the handler's decoder skips it.
	spaced := cat(bytes.Repeat([]byte(" "), 9<<20), []byte(`{"path":"main://projeler/EVIL/a.txt"}`))
	for name, body := range map[string][]byte{"inside": inside, "late": late, "spaced": spaced} {
		for _, ct := range []string{"application/json", "text/plain", ""} {
			rec, got := serve(bodyReq(acme, http.MethodPost, "/api/files/save-text", ct, body))
			assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, "%s as %q", name, ct)
			assert.Equal(t, `{"error":"request body too large"}`, rec.Body.String(), "%s as %q", name, ct)
			assert.False(t, got.ran, "%s as %q: the handler must not run", name, ct)
		}
	}

	// At the limit exactly it is still held.
	head := []byte(`{"path":"projeler/acme/a.txt","content":"`)
	tail := []byte(`"}`)
	exact := cat(head, bytes.Repeat([]byte("x"), 8<<20-len(head)-len(tail)), tail)
	require.Len(t, exact, 8<<20)
	rec, got := serve(bodyReq(acme, http.MethodPost, "/api/files/save-text", "text/plain", exact))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var seen map[string]any
	require.NoError(t, json.Unmarshal(got.body, &seen))
	assert.Equal(t, "main://projeler/acme/a.txt", seen["path"])
}

// A body that begins as an object and does not parse is refused: what this
// layer cannot read it cannot hold to the root.
func TestMiddleware_AnObjectThatDoesNotParseIsRefused(t *testing.T) {
	for _, body := range []string{
		`{"path":"main://projeler/EVIL/a.txt"`,
		`{"path":"main://projeler/acme/a.txt",}`,
		`{"path" "main://projeler/EVIL"}`,
		` {`,
	} {
		for _, ct := range []string{"application/json", "text/plain", ""} {
			rec, got := serve(bodyReq(acme, http.MethodPost, "/api/files/manager?action=newfolder", ct, []byte(body)))
			assert.Equal(t, http.StatusBadRequest, rec.Code, "%s as %q", body, ct)
			assert.Equal(t, `{"error":"bad json"}`, rec.Body.String(), "%s as %q", body, ct)
			assert.False(t, got.ran, "%s as %q: the handler must not run", body, ct)
		}
	}
}

// A number too large for a float64 failed this layer's decoding, which then
// passed the whole body on untouched - while a handler's struct, which has no
// field for that number, decoded the path beside it.
func TestMiddleware_ANumberHidesNoPath(t *testing.T) {
	for _, ct := range []string{"application/json", "text/plain"} {
		rec, got := serve(bodyReq(acme, http.MethodPost, "/api/files/manager?action=newfolder", ct, []byte(`{"path":"main://projeler/EVIL","name":"n","pad":1e999}`)))
		assert.Equal(t, http.StatusForbidden, rec.Code, "as %q: %s", ct, rec.Body.String())
		assert.False(t, got.ran, "as %q", ct)
	}

	// Inside the root the numbers and the text come through as they were
	// written: no float rounding, no HTML escaping.
	rec, got := serve(bodyReq(acme, http.MethodPost, "/api/files/save-text", "application/json",
		[]byte(`{"path":"projeler/acme/a.html","content":"<b>&amp;</b>","storage_id":9007199254740993,"pad":1e999}`)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, string(got.body), `"storage_id":9007199254740993`)
	assert.Contains(t, string(got.body), `"pad":1e999`)
	assert.Contains(t, string(got.body), `"content":"<b>&amp;</b>"`)
	assert.Contains(t, string(got.body), `"path":"main://projeler/acme/a.html"`)
}

// A body that is not an object names no path a handler reads: it passes on as
// it was sent, at any size.
func TestMiddleware_ABodyThatIsNotAnObjectPassesUntouched(t *testing.T) {
	bodies := map[string][]byte{
		"array":   []byte(`["main://projeler/EVIL"]`),
		"string":  []byte(`"main://projeler/EVIL"`),
		"null":    []byte(`null`),
		"form":    []byte("path=main://projeler/EVIL&name=n"),
		"empty":   {},
		"spaces":  []byte("   "),
		"9 MiB":   bytes.Repeat([]byte("a"), 9<<20),
		"9 MiB [": cat([]byte("["), bytes.Repeat([]byte(`"x",`), 9<<18), []byte(`"x"]`)),
	}
	for name, body := range bodies {
		for _, ct := range []string{"application/json", "text/plain"} {
			rec, got := serve(bodyReq(acme, http.MethodPost, "/api/files/manager?action=newfolder", ct, body))
			require.Equal(t, http.StatusOK, rec.Code, "%s as %q", name, ct)
			assert.True(t, bytes.Equal(body, got.body), "%s as %q must pass byte for byte (got %d of %d bytes)", name, ct, len(got.body), len(body))
			assert.EqualValues(t, len(body), got.cl, "%s as %q", name, ct)
		}
	}
}

// An unconfined call's body is not read at all.
func TestMiddleware_AnUnconfinedBodyIsNotRead(t *testing.T) {
	for name, body := range map[string][]byte{
		"object":    []byte(`{"z":1,"path":"main://anywhere","a":"<b>"}`),
		"broken":    []byte(`{"path":`),
		"9 MiB":     cat([]byte(`{"path":"main://anywhere","content":"`), bytes.Repeat([]byte("x"), 9<<20), []byte(`"}`)),
		"multipart": []byte("--x\r\n"),
	} {
		for _, ct := range []string{"application/json", "text/plain"} {
			rec, got := serve(bodyReq("", http.MethodPost, "/api/files/save-text", ct, body))
			require.Equal(t, http.StatusOK, rec.Code, "%s as %q", name, ct)
			assert.True(t, bytes.Equal(body, got.body), "%s as %q", name, ct)
		}
	}
}

// HoldBody - what a handler that decodes its own body calls - holds it as the
// middleware does, and fails closed the same way.
func TestHoldBody_IsTheMiddlewaresReading(t *testing.T) {
	root := Root{Adapter: "main", Rel: "projeler/acme"}
	for _, body := range []string{`{"path":"main://projeler/acme/a.txt"`, `{"path":"main://projeler/EVIL","n":1e999}`} {
		_, err := HoldBody(root, []byte(body))
		assert.Error(t, err, body)
	}
	for _, body := range []string{`["main://projeler/EVIL"]`, `null`, ``} {
		out, err := HoldBody(root, []byte(body))
		require.NoError(t, err, body)
		assert.Equal(t, body, string(out))
	}
	out, err := HoldBody(root, []byte(`{"path":"projeler/acme/a.txt","n":9007199254740993}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"path":"main://projeler/acme/a.txt","n":9007199254740993}`, string(out))
	assert.Contains(t, string(out), "9007199254740993")
}
