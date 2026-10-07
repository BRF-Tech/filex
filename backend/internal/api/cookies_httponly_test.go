package api_test

// Every cookie filex sets is HttpOnly (task #92's cookie audit).
//
// With the ONLYOFFICE editor's frame on the document server's own origin
// (FILEX_ONLYOFFICE_FRAME_ORIGIN), code filex did not write runs on a host that
// is usually the same site as filex - and on a multi-tenant install the
// session cookie's Domain is the parent domain (`files.example.com` →
// `.example.com`), so the browser sends it there too. What keeps a script on
// that host from READING it is HttpOnly; what keeps it from USING it is the
// origin guard (office_frame_same_site_test.go).
//
// The audit, 2026-10-06 - the cookies filex sets, all HttpOnly already:
//
//	filex_session (session)          handlers/auth.go         HttpOnly, Lax, Secure behind TLS, Domain on multi-tenant
//	share unlock (a PIN's answer)    handlers/share.go, public_api.go, drop.go   HttpOnly, Lax, host-only
//	OIDC state                       auth/drivers/oidc        HttpOnly, Lax, host-only, 10 min
//	sign-in flow                     authsetup/oidcflow.go    HttpOnly, Lax, host-only
//
// No cookie a script must read: the web client's `filex_csrf` reader
// (web/src/api/client.ts) finds nothing, the server never sets one, and the
// bearer the desktop shell keeps is in sessionStorage, per origin.
//
// So these are green on the code before #92 too: they hold the line, so the
// next cookie cannot be added without it.

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Every `http.Cookie{…}` literal in filex's own code says `HttpOnly: true`.
func TestCookies_EveryCookieFilexSetsIsHttpOnly(t *testing.T) {
	root := filepath.Join("..") // backend/internal
	fset := token.NewFileSet()
	found := 0
	var bad []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The test harness builds a client jar, not an answer.
			if d.Name() == "testutil" || d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Cookie" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "http" {
				return true
			}
			found++
			httpOnly := false
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				k, _ := kv.Key.(*ast.Ident)
				v, _ := kv.Value.(*ast.Ident)
				if k != nil && v != nil && k.Name == "HttpOnly" && v.Name == "true" {
					httpOnly = true
				}
			}
			if !httpOnly {
				bad = append(bad, fset.Position(lit.Pos()).String())
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, found, 7, "found %d http.Cookie literals (8 on the day this was written); the walk broke", found)
	assert.Empty(t, bad, "a cookie that is not HttpOnly can be read by any script on a host it is sent to - the ONLYOFFICE frame origin among them")
}

// The session cookie as a browser receives it at sign-in.
func TestCookies_TheSessionCookieAtSignIn(t *testing.T) {
	srv, _, store := testutil.NewTestServerWith(t, nil, nil)
	testutil.SeedAdmin(t, store)
	body, _ := json.Marshal(map[string]string{"email": "admin@test.local", "password": "TestAdminPass!1"})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)
	var session *http.Cookie
	for _, c := range res.Cookies() {
		assert.True(t, c.HttpOnly, "%s is not HttpOnly", c.Name)
		if c.Name == authlocal.SessionCookieName {
			session = c
		}
	}
	require.NotNil(t, session, "no session cookie at sign-in")
	assert.True(t, session.HttpOnly)
	assert.True(t, session.Secure, "behind TLS the session cookie is Secure")
	assert.Equal(t, http.SameSiteLaxMode, session.SameSite)
}
