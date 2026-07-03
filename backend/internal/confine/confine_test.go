package confine

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

func TestRoot_Enforce(t *testing.T) {
	root := Root{Adapter: "main", Rel: "projeler/acme"}

	cases := []struct {
		in   string
		want string
		err  bool
	}{
		{"main://projeler/acme/foo.txt", "main://projeler/acme/foo.txt", false},
		{"main://projeler/acme/sub/x", "main://projeler/acme/sub/x", false},
		{"main://projeler/acme", "main://projeler/acme", false},
		{"main://", "main://projeler/acme", false},               // root → confined folder
		{"projeler/acme/foo", "main://projeler/acme/foo", false}, // adapter omitted → assumed
		{"main://projeler/other", "", true},                      // sibling tenant → blocked
		{"main://projeler/acme/../other", "", true},              // traversal → blocked
		{"other://projeler/acme", "", true},                      // wrong storage → blocked
		{"main://baba", "", true},                                // outside prefix → blocked
	}
	for _, c := range cases {
		got, err := root.enforce(c.in)
		if c.err {
			require.ErrorIs(t, err, ErrOutOfRoot, "input %q should be rejected", c.in)
			continue
		}
		require.NoError(t, err, "input %q", c.in)
		require.Equal(t, c.want, got, "input %q", c.in)
	}
}

func reqWithToken(scopes, header string) *http.Request {
	r := httptest.NewRequest("GET", "/api/files/manager?action=index", nil)
	ctx := auth.WithToken(r.Context(), &model.APIToken{Scopes: scopes})
	if header != "" {
		r.Header.Set("X-Filex-Root", header)
	}
	return r.WithContext(ctx)
}

func TestFromRequest(t *testing.T) {
	// token root only
	root, ok, err := FromRequest(reqWithToken("read,write,root:main://projeler/acme", ""))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, Root{"main", "projeler/acme"}, root)

	// header narrows within token root
	root, ok, err = FromRequest(reqWithToken("root:main://projeler/acme", "main://projeler/acme/sub"))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, Root{"main", "projeler/acme/sub"}, root)

	// header tries to escape the token ceiling → rejected
	_, _, err = FromRequest(reqWithToken("root:main://projeler/acme", "main://projeler/other"))
	require.ErrorIs(t, err, ErrOutOfRoot)

	// no token, no header → unconfined
	r := httptest.NewRequest("GET", "/x", nil)
	_, ok, err = FromRequest(r)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestMiddleware_BodyConfinement(t *testing.T) {
	var seen string
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		seen = string(b)
		w.WriteHeader(200)
	}))

	mkReq := func(body string) *http.Request {
		r := httptest.NewRequest("POST", "/api/files/move", bytes.NewReader([]byte(body)))
		r.Header.Set("Content-Type", "application/json")
		ctx := auth.WithToken(r.Context(), &model.APIToken{Scopes: "root:main://projeler/acme"})
		return r.WithContext(ctx)
	}

	// in-root move passes through (paths preserved)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, mkReq(`{"source":["main://projeler/acme/a.txt"],"target":"main://projeler/acme/sub"}`))
	require.Equal(t, 200, rec.Code)
	require.Contains(t, seen, "projeler/acme/a.txt")

	// out-of-root source → 403, handler never runs
	seen = ""
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, mkReq(`{"source":["main://projeler/EVIL/a.txt"],"target":"main://projeler/acme"}`))
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Empty(t, seen, "handler must not run for an out-of-root request")
}

func TestMiddleware_Unconfined_Passthrough(t *testing.T) {
	called := false
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(200) }))
	r := httptest.NewRequest("GET", "/api/files/manager?path=main://anything", nil) // no token, no header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r.WithContext(context.Background()))
	require.True(t, called)
	require.Equal(t, 200, rec.Code)
}
