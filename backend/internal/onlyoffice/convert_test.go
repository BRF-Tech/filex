package onlyoffice

// The conversion client every caller shares (convert.go, purpose.go): a
// thumbnail asked of a fake document server (httptest) the way filex asks
// the real one, and what each of its answers means.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeSecret = "convert-test-secret"

// fakeDS is a document server's ConvertService: it records every request's
// signed payload and answers what answer() says for the n-th request (from 1).
type fakeDS struct {
	srv      *httptest.Server
	mu       sync.Mutex
	payloads []map[string]any
	answer   func(n int, payload map[string]any) (int, any)
	png      []byte
}

func newFakeDS(t *testing.T, answer func(n int, payload map[string]any) (int, any)) *fakeDS {
	t.Helper()
	f := &fakeDS{answer: answer, png: tinyPNG()}
	mux := http.NewServeMux()
	mux.HandleFunc("/ConvertService.ashx", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		tok, _ := body["token"].(string)
		payload, err := verifyHS256(tok, fakeSecret)
		if err != nil {
			// A document server that enforces JWT: -8 before anything else.
			_ = json.NewEncoder(w).Encode(map[string]any{"error": -8})
			return
		}
		f.mu.Lock()
		f.payloads = append(f.payloads, payload)
		n := len(f.payloads)
		f.mu.Unlock()
		status, ans := f.answer(n, payload)
		w.WriteHeader(status)
		if s, ok := ans.(string); ok {
			_, _ = w.Write([]byte(s))
			return
		}
		_ = json.NewEncoder(w).Encode(ans)
	})
	mux.HandleFunc("/cache/files/result.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(f.png)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDS) resultURL() string { return f.srv.URL + "/cache/files/result.png" }

func (f *fakeDS) seen() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.payloads...)
}

// tinyPNG is a 1x1 PNG.
func tinyPNG() []byte {
	b, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	return b
}

func serviceFor(ds string) *Service {
	return &Service{DocumentServerURL: ds, JWTSecret: fakeSecret, PublicURL: "http://filex.test"}
}

// The document server is asked asynchronously and polled with the SAME
// request until it says it is done; everything it is asked for (the
// thumbnail's size, a sheet's layout) is inside the signed token, the only
// place a document server with tokenRequiredParams reads it from; and the
// picture comes back from its own address.
func TestOOThumb_DrawsThroughDocumentServer(t *testing.T) {
	var ds *fakeDS
	ds = newFakeDS(t, func(n int, _ map[string]any) (int, any) {
		if n < 3 {
			return http.StatusOK, map[string]any{"endConvert": false, "percent": n * 30}
		}
		return http.StatusOK, map[string]any{"endConvert": true, "fileType": "png", "fileUrl": ds.resultURL()}
	})
	svc := serviceFor(ds.srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	png, err := svc.DrawThumbnail(ctx, ThumbRequest{NodeID: 42, StorageID: 3, Name: "Bütçe 2026.xlsx", ContentSig: "e:abc", Attempt: 1})
	require.NoError(t, err)
	assert.Equal(t, tinyPNG(), png, "the picture is the document server's result")

	seen := ds.seen()
	require.Len(t, seen, 3, "asked once, then polled until endConvert")
	for _, p := range seen {
		assert.Equal(t, true, p["async"], "never synchronous: a synchronous request is held inside the document server")
		assert.Equal(t, seen[0]["key"], p["key"], "a poll is the same request")
	}
	p := seen[0]
	assert.Equal(t, "xlsx", p["filetype"])
	assert.Equal(t, "png", p["outputtype"])
	th, _ := p["thumbnail"].(map[string]any)
	require.NotNil(t, th, "the thumbnail is inside the signed payload")
	assert.EqualValues(t, 1, th["aspect"])
	assert.EqualValues(t, ThumbSize, th["width"])
	assert.EqualValues(t, ThumbSize, th["height"])
	layout, _ := p["spreadsheetLayout"].(map[string]any)
	require.NotNil(t, layout, "a sheet is laid out, inside the signed payload")
	assert.EqualValues(t, 1, layout["fitToWidth"])
	assert.EqualValues(t, 0, layout["fitToHeight"])
	assert.Equal(t, true, layout["gridLines"])

	// The address the document server downloads from names the purpose and
	// is signed with it, on the address it reaches filex at.
	u, err := url.Parse(p["url"].(string))
	require.NoError(t, err)
	assert.Equal(t, "http://filex.test"+FetchPath, u.Scheme+"://"+u.Host+u.Path)
	q := u.Query()
	assert.Equal(t, PurposeThumb, q.Get("p"))
	assert.Equal(t, "42", q.Get("n"))
	require.NoError(t, svc.VerifyPurposeFetchCtx(ctx, 42, mustInt(t, q.Get("exp")), PurposeThumb, q.Get("sig")))
	require.Error(t, svc.VerifyFetchSignatureCtx(ctx, 42, mustInt(t, q.Get("exp")), q.Get("sig")),
		"a thumbnail's address is not an editor's")
	require.Error(t, svc.VerifyPurposeFetchCtx(ctx, 42, mustInt(t, q.Get("exp")), PurposeConvert, q.Get("sig")),
		"nor another purpose's")
	assert.LessOrEqual(t, mustInt(t, q.Get("exp")), time.Now().Add(PurposeFetchTTL+time.Minute).Unix(), "short-lived")

	// A document (not a sheet) is not laid out.
	ds2 := newFakeDS(t, func(int, map[string]any) (int, any) { return http.StatusOK, map[string]any{"error": -3} })
	_, _ = serviceFor(ds2.srv.URL).DrawThumbnail(ctx, ThumbRequest{NodeID: 7, Name: "mektup.docx", Attempt: 1})
	require.Len(t, ds2.seen(), 1)
	_, laid := ds2.seen()[0]["spreadsheetLayout"]
	assert.False(t, laid, "a word document has no sheet to lay out")
}

func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	for _, c := range s {
		require.True(t, c >= '0' && c <= '9', "not a number: %q", s)
		n = n*10 + int64(c-'0')
	}
	return n
}

// The key names the content, the parameters and the attempt: the document
// server answers a key it has seen with what it made then (a cached failure
// included), so a new version of the file, a new picture size or a retry
// must each be a new key - and the same request the same key, or nothing
// the document server cached is ever reused.
func TestOOThumb_KeyCarriesContentParamsAndRetry(t *testing.T) {
	svc := serviceFor("http://ds.test")
	ctx := context.Background()
	base := ThumbRequest{NodeID: 9, StorageID: 2, Name: "a.docx", ContentSig: "e:v1", Attempt: 1}
	k := svc.ThumbKey(ctx, base)
	assert.Equal(t, k, svc.ThumbKey(ctx, base), "the same request, the same key")
	assert.Regexp(t, `^[0-9A-Za-z.=_-]{1,128}$`, k, "what the document server takes")

	for name, r := range map[string]ThumbRequest{
		"another version": {NodeID: 9, StorageID: 2, Name: "a.docx", ContentSig: "e:v2", Attempt: 1},
		"a retry":         {NodeID: 9, StorageID: 2, Name: "a.docx", ContentSig: "e:v1", Attempt: 2},
		"another file":    {NodeID: 10, StorageID: 2, Name: "a.docx", ContentSig: "e:v1", Attempt: 1},
		"another storage": {NodeID: 9, StorageID: 3, Name: "a.docx", ContentSig: "e:v1", Attempt: 1},
	} {
		assert.NotEqual(t, k, svc.ThumbKey(ctx, r), name)
	}
	other := serviceFor("http://ds.test")
	other.PublicURL = "http://another-filex.test"
	assert.NotEqual(t, k, other.ThumbKey(ctx, base), "another filex on the same document server")

	// The parameters are in it: the key is ConvertKey over them, so a new
	// thumbParams is a new key.
	assert.Equal(t, k, ConvertKey(PurposeThumb, "http://filex.test", "2", "9", "e:v1", thumbParams, "1"))
	assert.NotEqual(t, k, ConvertKey(PurposeThumb, "http://filex.test", "2", "9", "e:v1", thumbParams+"x", "1"))
	// Parts are not simply glued together: "ab"+"c" is not "a"+"bc".
	assert.NotEqual(t, ConvertKey("ab", "c"), ConvertKey("a", "bc"))

	// And the key the document server is sent is this one.
	var ds *fakeDS
	ds = newFakeDS(t, func(int, map[string]any) (int, any) {
		return http.StatusOK, map[string]any{"endConvert": true, "fileUrl": ds.resultURL()}
	})
	s := serviceFor(ds.srv.URL)
	_, err := s.DrawThumbnail(ctx, ThumbRequest{NodeID: 9, StorageID: 2, Name: "a.docx", ContentSig: "e:v1", Attempt: 3})
	require.NoError(t, err)
	assert.Equal(t, s.ThumbKey(ctx, ThumbRequest{NodeID: 9, StorageID: 2, ContentSig: "e:v1", Attempt: 3}), ds.seen()[0]["key"])
}

// What each answer means for whoever asked: the same bytes will fail again
// (corrupt, password, too large), or asking again later may work.
func TestOOThumb_ErrorClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		answer any
		class  ErrorClass
		code   int
		what   string
	}{
		{"-3 the converter could not read it", 200, map[string]any{"error": -3}, ClassCorrupt, -3, "ds-3"},
		{"-7 input error", 200, map[string]any{"error": -7}, ClassCorrupt, -7, "ds-7"},
		{"-9 no output format", 200, map[string]any{"error": -9}, ClassCorrupt, -9, "ds-9"},
		{"-5 a password", 200, map[string]any{"error": -5}, ClassPassword, -5, "ds-5"},
		{"-10 a size limit", 200, map[string]any{"error": -10}, ClassTooLarge, -10, "ds-10"},
		{"-1 unknown", 200, map[string]any{"error": -1}, ClassTransient, -1, "ds-1"},
		{"-2 timeout", 200, map[string]any{"error": -2}, ClassTransient, -2, "ds-2"},
		{"-4 download", 200, map[string]any{"error": -4}, ClassTransient, -4, "ds-4"},
		{"-6 result database", 200, map[string]any{"error": -6}, ClassTransient, -6, "ds-6"},
		{"-8 token", 200, map[string]any{"error": -8}, ClassTransient, -8, "ds-8"},
		{"a 502 from a proxy", 502, "<html>bad gateway</html>", ClassTransient, 0, "http-502"},
		{"a page that is not an answer", 200, "<html>not the document server</html>", ClassTransient, 0, "answer"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ds := newFakeDS(t, func(int, map[string]any) (int, any) { return c.status, c.answer })
			_, err := serviceFor(ds.srv.URL).DrawThumbnail(context.Background(), ThumbRequest{NodeID: 1, Name: "x.docx", Attempt: 1})
			ce, ok := AsConvertError(err)
			require.True(t, ok, "%v", err)
			assert.Equal(t, c.class, ce.Class)
			assert.Equal(t, c.code, ce.Code)
			assert.Equal(t, c.what, ce.What)
		})
	}

	t.Run("the network", func(t *testing.T) {
		ds := httptest.NewServer(http.NotFoundHandler())
		addr := ds.URL
		ds.Close()
		_, err := serviceFor(addr).DrawThumbnail(context.Background(), ThumbRequest{NodeID: 1, Name: "x.docx", Attempt: 1})
		ce, ok := AsConvertError(err)
		require.True(t, ok, "%v", err)
		assert.Equal(t, ClassTransient, ce.Class)
		assert.Equal(t, "net", ce.What)
	})
	t.Run("a document server that never finishes", func(t *testing.T) {
		ds := newFakeDS(t, func(int, map[string]any) (int, any) { return 200, map[string]any{"endConvert": false} })
		ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
		defer cancel()
		_, err := serviceFor(ds.srv.URL).DrawThumbnail(ctx, ThumbRequest{NodeID: 1, Name: "x.docx", Attempt: 1})
		ce, ok := AsConvertError(err)
		require.True(t, ok, "%v", err)
		assert.Equal(t, ClassTransient, ce.Class)
		assert.Equal(t, "timeout", ce.What)
	})
	t.Run("not configured", func(t *testing.T) {
		_, err := (&Service{}).DrawThumbnail(context.Background(), ThumbRequest{NodeID: 1, Name: "x.docx", Attempt: 1})
		require.ErrorIs(t, err, ErrNotConfigured)
	})
	t.Run("a -4 because filex refused the encrypted file", func(t *testing.T) {
		var svc *Service
		ds := newFakeDS(t, func(int, map[string]any) (int, any) {
			// The document server's download reached filex and was refused
			// (handlers.OnlyOffice.fetchForPurpose records it so).
			svc.NotePurposeFetch(1, PurposeThumb, http.StatusUnsupportedMediaType, FetchEncrypted)
			return 200, map[string]any{"error": -4}
		})
		svc = serviceFor(ds.srv.URL)
		_, err := svc.DrawThumbnail(context.Background(), ThumbRequest{NodeID: 1, Name: "x.docx", Attempt: 1})
		ce, ok := AsConvertError(err)
		require.True(t, ok, "%v", err)
		assert.Equal(t, ClassEncrypted, ce.Class)
	})
}

// The answer names where to download the result. Only the document server's
// own address is fetched: an answer naming any other is refused and that
// address is never asked.
func TestOOThumb_ResultFromForeignOriginRefused(t *testing.T) {
	var foreignHits atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		foreignHits.Add(1)
		_, _ = w.Write(tinyPNG())
	}))
	defer foreign.Close()
	ds := newFakeDS(t, func(int, map[string]any) (int, any) {
		return 200, map[string]any{"endConvert": true, "fileUrl": foreign.URL + "/cache/files/result.png"}
	})
	_, err := serviceFor(ds.srv.URL).DrawThumbnail(context.Background(), ThumbRequest{NodeID: 1, Name: "x.pptx", Attempt: 1})
	require.ErrorIs(t, err, ErrForeignOrigin)
	assert.Zero(t, foreignHits.Load(), "the foreign address was never fetched")

	// The same answer on the document server's own address is fetched.
	var own *fakeDS
	own = newFakeDS(t, func(int, map[string]any) (int, any) {
		return 200, map[string]any{"endConvert": true, "fileUrl": own.resultURL()}
	})
	got, err := serviceFor(own.srv.URL).DrawThumbnail(context.Background(), ThumbRequest{NodeID: 1, Name: "x.pptx", Attempt: 1})
	require.NoError(t, err)
	assert.Equal(t, tinyPNG(), got)

	// A result larger than the caller accepts is refused, not cut short.
	big := newFakeDS(t, func(int, map[string]any) (int, any) { return 200, nil })
	big.answer = func(int, map[string]any) (int, any) {
		return 200, map[string]any{"endConvert": true, "fileUrl": big.resultURL()}
	}
	big.png = []byte(strings.Repeat("x", 2048))
	_, err = serviceFor(big.srv.URL).DrawThumbnail(context.Background(), ThumbRequest{NodeID: 1, Name: "x.pptx", Attempt: 1, MaxBytes: 1024})
	ce, ok := AsConvertError(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, "result", ce.What)
}

func TestSameOrigin(t *testing.T) {
	assert.True(t, sameOrigin("http://ds:8080/cache/x", "http://ds:8080"))
	assert.True(t, sameOrigin("https://docs.example/cache/x", "https://DOCS.example/"))
	assert.True(t, sameOrigin("https://docs.example:443/cache/x", "https://docs.example"))
	assert.False(t, sameOrigin("http://docs.example/cache/x", "https://docs.example"), "another scheme")
	assert.False(t, sameOrigin("http://ds:8081/cache/x", "http://ds:8080"), "another port")
	assert.False(t, sameOrigin("http://169.254.169.254/latest", "http://ds:8080"), "another host")
	assert.False(t, sameOrigin("/cache/x", "http://ds:8080"), "no host")
}
