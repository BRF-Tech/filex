package confine

// A backslash, read both ways (GHSA-8gvc-6w52-6c7j, 2026-10-04).
//
// The checks here clean a path the POSIX way, where `\` belongs to a name; a
// storage on a Windows host reads it as a separator. A path is inside a root
// only when it is inside under both readings, on every host - so these tests
// hold whatever GOOS runs them. No live Windows instance was measured for this
// change: the Windows reading is the local driver's (toSlash, then
// path.Clean), reproduced here as `\` -> `/`.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoot_ABackslashClimbingOutIsOutside(t *testing.T) {
	root := Root{Adapter: "main", Rel: "projeler/acme"}

	for _, p := range []string{
		`main://projeler/acme/x\..\..\other`,
		`main://projeler/acme/x\..\..\other\gizli.txt`,
		`main://projeler/acme\..\other`,
		`projeler/acme/..\..\..\etc`,
		// A sibling of the root on a host where `\` is part of a name.
		`main://projeler/acme\gizli.txt`,
	} {
		_, err := root.enforce(p)
		assert.ErrorIs(t, err, ErrOutOfRoot, "enforce %q", p)
		assert.False(t, root.holds(p), "holds %q", p)
		a, rel, found := strings.Cut(p, "://")
		if !found {
			a, rel = "main", p
		}
		assert.False(t, root.Within(a, rel), "Within %q", p)
	}

	// Inside under both readings: still inside, spelled as it was sent.
	for _, p := range []string{
		`main://projeler/acme/a\b.txt`,
		`main://projeler/acme/x\..\y.txt`,
	} {
		got, err := root.enforce(p)
		require.NoError(t, err, p)
		assert.Equal(t, p, got)
		assert.True(t, root.holds(p), "holds %q", p)
		_, rel, _ := strings.Cut(p, "://")
		assert.True(t, root.Within("main", rel), "Within %q", p)
	}
}

// The middleware refuses such a path in a body and in `?path=`, and a header
// root that climbs out of the token's root.
func TestMiddleware_ABackslashClimbingOutIsRefused(t *testing.T) {
	ran := false
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran = true
		w.WriteHeader(http.StatusOK)
	}))

	body := httptest.NewRequest("POST", "/api/files/manager?action=newfolder",
		strings.NewReader(`{"path":"main://projeler/acme/x\\..\\..\\other","name":"n"}`))
	body.Header.Set("Content-Type", "application/json")
	body = body.WithContext(reqWithToken("read,write,root:main://projeler/acme", "").Context())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, body)
	assert.Equal(t, http.StatusForbidden, rec.Code, "a body path")

	q := reqWithToken("read,root:main://projeler/acme", "")
	q.URL.RawQuery = `action=index&path=main://projeler/acme/x%5C..%5C..%5Cother`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, q)
	assert.Equal(t, http.StatusForbidden, rec.Code, "a ?path=")

	_, _, err := FromRequest(reqWithToken("read,root:main://projeler/acme", `main://projeler/acme/x\..\..\other`))
	assert.ErrorIs(t, err, ErrOutOfRoot, "a header root")
	assert.False(t, ran, "the handler must not run")

	// Inside both ways, the handler runs.
	ok := httptest.NewRequest("POST", "/api/files/manager?action=newfolder",
		strings.NewReader(`{"path":"main://projeler/acme/a\\b","name":"n"}`))
	ok.Header.Set("Content-Type", "application/json")
	ok = ok.WithContext(reqWithToken("read,write,root:main://projeler/acme", "").Context())
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, ok)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, ran)
}
