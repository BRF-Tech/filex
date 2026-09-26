package dav

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/basepath"
)

// WebDAV under a base path (FILEX_BASE_PATH), mounted exactly as the router
// mounts it: the base comes off in front of chi, /dav is a chi mount.
//
// A client mapped `https://example.com/filex/dav/depo/` and speaks that path
// in both directions: the hrefs of a PROPFIND answer have to be under it
// (clients match them against the collection they asked about, and Windows'
// mini-redirector and davfs2 drop entries whose href is elsewhere), and a
// MOVE or COPY names its Destination under it. Both used to assume /dav at
// the root of the host.
func TestWebDAVUnderABasePath(t *testing.T) {
	ha := newHarness(t)
	ha.addStorage(t, "depo", false, false)

	r := chi.NewRouter()
	r.Use(basepath.Middleware("/filex"))
	r.Mount(Prefix, ha.h)
	srv := httptest.NewServer(r)
	defer srv.Close()

	do := func(method, path string, hdr map[string]string, body string) (int, string) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rdr)
		require.NoError(t, err)
		req.SetBasicAuth(ha.adminEmail, ha.adminPass)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	code, _ := do(http.MethodPut, "/filex/dav/depo/a.txt", nil, "altında")
	require.Equal(t, http.StatusCreated, code)

	// PROPFIND: every href is under the base.
	code, body := do("PROPFIND", "/filex/dav/depo/", map[string]string{"Depth": "1"}, "")
	require.Equal(t, http.StatusMultiStatus, code, body)
	hrefs := regexp.MustCompile(`<D:href>([^<]*)</D:href>`).FindAllStringSubmatch(body, -1)
	require.NotEmpty(t, hrefs, body)
	sawFile := false
	for _, m := range hrefs {
		require.True(t, strings.HasPrefix(m[1], "/filex/dav/depo/"), "href %q is outside the base", m[1])
		sawFile = sawFile || m[1] == "/filex/dav/depo/a.txt"
	}
	require.True(t, sawFile, "the file is listed under its own href: %s", body)

	// The root collection too.
	code, body = do("PROPFIND", "/filex/dav/", map[string]string{"Depth": "1"}, "")
	require.Equal(t, http.StatusMultiStatus, code, body)
	require.Contains(t, body, "<D:href>/filex/dav/depo/</D:href>")

	// MOVE names its Destination under the base, absolute or path-only.
	code, body = do("MOVE", "/filex/dav/depo/a.txt", map[string]string{"Destination": srv.URL + "/filex/dav/depo/b.txt"}, "")
	require.Equal(t, http.StatusCreated, code, body)
	code, body = do(http.MethodGet, "/filex/dav/depo/b.txt", nil, "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "altında", body)
	code, body = do("COPY", "/filex/dav/depo/b.txt", map[string]string{"Destination": "/filex/dav/depo/c.txt"}, "")
	require.Equal(t, http.StatusCreated, code, body)

	// A Destination outside the base is not somewhere under /dav.
	code, _ = do("MOVE", "/filex/dav/depo/b.txt", map[string]string{"Destination": srv.URL + "/dav/depo/d.txt"}, "")
	require.Equal(t, http.StatusBadGateway, code)

	// And the bare /dav on the host's root is not filex's at all.
	code, _ = do("PROPFIND", "/dav/", map[string]string{"Depth": "1"}, "")
	require.Equal(t, http.StatusNotFound, code)
}
