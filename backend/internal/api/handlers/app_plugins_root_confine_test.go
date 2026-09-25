package handlers_test

// A token confined to one folder (`root:<adapter>://<rel>`) must not run an
// app on a file outside that folder.
//
// ⚠⚠ confine.Middleware rewrites the body keys it knows (`path`, `item`,
// `target`, `sourceDir`, `source`, `items[].path`) and the app doors take a
// `paths` ARRAY, which it passed through untouched; the app handlers checked
// the storage, the ACL and encrypted folders, never the token's root. So the
// one app token a host hands an embed — confined to the tenant's folder —
// could run an app on any file of the storage and have its result written
// next to that file (found 2026-09-25 while adding the drag-out link, #71).
//
// Every probe binds the token to an ADMIN account: an admin's ACL clears
// every path, so nothing but the token's `root:` can refuse the request.

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

func rootConfinedApp(t *testing.T) (*appFixture, string) {
	t.Helper()
	f := newAppFixture(t, nil)
	f.installEcho(t)
	f.writeFile(t, "docs/a.txt", "alpha")
	f.writeFile(t, "docs/b.txt", "bravo")
	f.writeFile(t, "secret/s.txt", "SECRET")
	u, err := f.store.CreateUser(context.Background(), "kutu-app@test.local", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	return f, issueToken(t, f.store, u.ID, "read,write,delete,root:main://docs", nil)
}

// appAs posts with the token only: a client with no cookie jar.
func appAs(t *testing.T, f *appFixture, tok, url string, body any) (int, string) {
	t.Helper()
	resp := aiReq(t, &http.Client{}, http.MethodPost, f.srv.URL+url, tok, body)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func TestAppPlugins_RootToken_RunStaysInRoot(t *testing.T) {
	f, tok := rootConfinedApp(t)

	code, raw := appAs(t, f, tok, "/api/files/plugins/actions/echo/upper/run", map[string]any{"paths": []string{"main://docs/a.txt"}})
	require.Less(t, code, 300, "inside the root the app runs: %s", raw)

	code, raw = appAs(t, f, tok, "/api/files/plugins/actions/echo/upper/run", map[string]any{"paths": []string{"main://secret/s.txt"}})
	assert.Equal(t, http.StatusForbidden, code, "outside the root: %s", raw)

	// One path outside is enough to refuse the whole selection.
	code, raw = appAs(t, f, tok, "/api/files/plugins/actions/echo/gather/run",
		map[string]any{"paths": []string{"main://docs/a.txt", "main://secret/s.txt"}})
	assert.Equal(t, http.StatusForbidden, code, "a selection reaching outside: %s", raw)
}

func TestAppPlugins_RootToken_ViewEventStaysInRoot(t *testing.T) {
	f, tok := rootConfinedApp(t)

	code, raw := appAs(t, f, tok, "/api/files/plugins/actions/echo/gather/run",
		map[string]any{"paths": []string{"main://docs/a.txt", "main://docs/b.txt"}})
	require.Less(t, code, 300, "inside the root the view opens: %s", raw)

	code, raw = appAs(t, f, tok, "/api/files/plugins/views/echo/picks/event", map[string]any{
		"event": "change", "path": "main://docs/a.txt",
		"paths": []string{"main://docs/a.txt", "main://secret/s.txt"},
	})
	assert.Equal(t, http.StatusForbidden, code, "an event naming a file outside: %s", raw)
}

// Nothing the refused calls asked for was written outside the root.
func TestAppPlugins_RootToken_WritesNothingOutside(t *testing.T) {
	f, tok := rootConfinedApp(t)
	appAs(t, f, tok, "/api/files/plugins/actions/echo/upper/run", map[string]any{"paths": []string{"main://secret/s.txt"}})
	assert.NoFileExists(t, f.root+"/secret/s-upper.txt")
}
