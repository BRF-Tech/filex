package handlers_test

// The download endpoints must not put a raw non-ASCII byte in a header.
//
// `internal/httpx` has the unit tests for the value itself; this one guards the
// WIRING — that every place which serves bytes actually calls it. The two can
// drift, and when they do the symptom is not a failing request: it is a client
// that throws while parsing the response, from inside an event handler, with
// the transfer left hanging.
//
//	TypeError: Cannot convert argument to a ByteString because the character
//	at index 32 has a value of 305 which is greater than 255
//
// That is what Electron's `net.fetch` did to the filex desktop app on
// 2026-08-29: dragging out a folder that contained `Türkçe adlı dosya.txt`
// stopped filling in halfway and the app showed a raw JavaScript error box.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDownload_HeadersAreASCII_ForANonASCIIFilename(t *testing.T) {
	fx := newTwoStorageFixture(t)
	const name = "Türkçe adlı dosya.txt"
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, name), []byte("icerik"), 0o644))

	for _, action := range []string{"download", "preview"} {
		q := url.Values{"action": []string{action}, "path": []string{"alpha://" + name}}
		req := httptest.NewRequest(http.MethodGet, "/api/files/manager?"+q.Encode(), nil)
		rec := httptest.NewRecorder()
		fx.mh.List(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, action)

		for key, values := range rec.Header() {
			for _, v := range values {
				for i := 0; i < len(v); i++ {
					if v[i] > 127 {
						t.Fatalf("%s: header %s carries byte 0x%X at index %d (%q) — no HTTP header may",
							action, key, v[i], i, v)
					}
				}
			}
		}
	}
}

func TestDownload_ContentDispositionKeepsTheRealNameInTheExtendedForm(t *testing.T) {
	fx := newTwoStorageFixture(t)
	const name = "Türkçe adlı dosya.txt"
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, name), []byte("icerik"), 0o644))

	q := url.Values{"action": []string{"download"}, "path": []string{"alpha://" + name}}
	req := httptest.NewRequest(http.MethodGet, "/api/files/manager?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	fx.mh.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	cd := rec.Header().Get("Content-Disposition")
	require.Contains(t, cd, `filename="`, "an ASCII fallback must always be there")
	require.Contains(t, cd, "filename*=UTF-8''T%C3%BCrk%C3%A7e%20adl%C4%B1%20dosya.txt",
		"the real name has to survive, percent-encoded: %s", cd)
}

func TestDownload_PlainNameGetsNoSecondCopyOfItself(t *testing.T) {
	fx := newTwoStorageFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(fx.rootA, "rapor.txt"), []byte("x"), 0o644))

	q := url.Values{"action": []string{"download"}, "path": []string{"alpha://rapor.txt"}}
	req := httptest.NewRequest(http.MethodGet, "/api/files/manager?"+q.Encode(), nil)
	rec := httptest.NewRecorder()
	fx.mh.List(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	cd := rec.Header().Get("Content-Disposition")
	require.Equal(t, `attachment; filename="rapor.txt"`, cd)
	require.False(t, strings.Contains(cd, "filename*"))
}
