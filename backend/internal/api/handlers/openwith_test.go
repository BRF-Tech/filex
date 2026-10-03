package handlers_test

// "Always open this kind with this app" (0.50, handlers/openwith.go): ONE
// record per account, the same for the browser, the desktop app and every
// embed (the maintainer, 2026-10-01).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// openWithAs answers one /api/me/open-with call as u.
func openWithAs(t *testing.T, h *handlers.OpenWith, u *model.User, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/api/me/open-with", func(r chi.Router) {
		r.Get("/", h.Get)
		r.Delete("/", h.Clear)
		r.Put("/{ext}", h.Put)
		r.Delete("/{ext}", h.Forget)
	})
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(auth.WithUser(req.Context(), u)))
	return rec
}

func choicesOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Choices map[string]string `json:"choices"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Choices
}

// surfaceChoices is what a surface's client reads: `openWith` of its prefs
// document, a JSON string holding the choices.
func surfaceChoices(t *testing.T, h *handlers.UserPrefs, u *model.User, surface string) map[string]string {
	t.Helper()
	rec := prefsGet(t, h, u, surface)
	require.Equal(t, http.StatusOK, rec.Code)
	var body struct {
		Prefs map[string]any `json:"prefs"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	raw, _ := body.Prefs["openWith"].(string)
	out := map[string]string{}
	if raw != "" {
		require.NoError(t, json.Unmarshal([]byte(raw), &out))
	}
	return out
}

// TestOpenWith_OneRecordForEverySurface: a choice made through one surface is
// the one every surface reads.
func TestOpenWith_OneRecordForEverySurface(t *testing.T) {
	prefs, store, ada, _ := newPrefsFixture(t)
	h := handlers.NewOpenWith(store)

	got := choicesOf(t, openWithAs(t, h, ada, "PUT", "/api/me/open-with/board", `{"handler":"builtin"}`))
	assert.Equal(t, map[string]string{"board": "builtin"}, got)
	got = choicesOf(t, openWithAs(t, h, ada, "PUT", "/api/me/open-with/drawio", `{"handler":"app:drawio/editor"}`))
	assert.Equal(t, map[string]string{"board": "builtin", "drawio": "app:drawio/editor"}, got)

	for _, surface := range []string{"web", "desktop"} {
		assert.Equal(t, map[string]string{"board": "builtin", "drawio": "app:drawio/editor"}, surfaceChoices(t, prefs, ada, surface),
			"the %s document carries the account's choices", surface)
	}

	assert.Equal(t, map[string]string{"drawio": "app:drawio/editor"}, choicesOf(t, openWithAs(t, h, ada, "DELETE", "/api/me/open-with/board", "")))
	assert.Equal(t, map[string]string{"drawio": "app:drawio/editor"}, surfaceChoices(t, prefs, ada, "desktop"))
}

// TestOpenWith_ASurfaceDocumentCannotOverwriteIt: a surface PUTs its whole
// document; a stale copy of the choices in it must not undo a choice made
// since on another surface.
func TestOpenWith_ASurfaceDocumentCannotOverwriteIt(t *testing.T) {
	prefs, store, ada, _ := newPrefsFixture(t)
	h := handlers.NewOpenWith(store)
	choicesOf(t, openWithAs(t, h, ada, "PUT", "/api/me/open-with/board", `{"handler":"app:board/board"}`))

	// The browser read {board: builtin} at boot and now saves a palette.
	rec := prefsPut(t, prefs, ada, "web", `{"prefs":{"palette":"ocean","openWith":"{\"board\":\"builtin\"}"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Equal(t, map[string]string{"board": "app:board/board"}, choicesOf(t, openWithAs(t, h, ada, "GET", "/api/me/open-with", "")))
	assert.Equal(t, map[string]string{"board": "app:board/board"}, surfaceChoices(t, prefs, ada, "web"))
	stored, err := store.GetUserPrefs(context.Background(), ada.ID, "web")
	require.NoError(t, err)
	assert.NotContains(t, stored, "openWith", "the surface document is stored without the account's choices")
	assert.Contains(t, stored, "ocean", "and with everything else it was sent")
}

// TestOpenWith_AdoptsTheLastWrittenSurfaceChoicesOnce: before 0.50's account
// row, a client kept the choices in its own surface document. The account
// adopts the ones written LAST, once; clearing them is not undone by the old
// copy.
func TestOpenWith_AdoptsTheLastWrittenSurfaceChoicesOnce(t *testing.T) {
	prefs, store, ada, _ := newPrefsFixture(t)
	h := handlers.NewOpenWith(store)
	ctx := context.Background()
	require.NoError(t, store.SetUserPrefs(ctx, ada.ID, "web", `{"theme":"dark","openWith":"{\"board\":\"builtin\"}"}`))
	// user_prefs.updated_at has second precision.
	time.Sleep(1100 * time.Millisecond)
	require.NoError(t, store.SetUserPrefs(ctx, ada.ID, "desktop", `{"openWith":"{\"board\":\"app:board/board\",\"md\":\"builtin\"}"}`))

	want := map[string]string{"board": "app:board/board", "md": "builtin"}
	assert.Equal(t, want, choicesOf(t, openWithAs(t, h, ada, "GET", "/api/me/open-with", "")), "the desktop document was written last")
	assert.Equal(t, want, surfaceChoices(t, prefs, ada, "web"), "and the browser reads the same")

	assert.Empty(t, choicesOf(t, openWithAs(t, h, ada, "DELETE", "/api/me/open-with", "")))
	assert.Empty(t, choicesOf(t, openWithAs(t, h, ada, "GET", "/api/me/open-with", "")), "cleared stays cleared")
	assert.Empty(t, surfaceChoices(t, prefs, ada, "desktop"), "the old copy in a surface document is not what a client reads")
}

// TestOpenWith_RefusesWhatIsNotAKindOrAnOpener: a kind is an extension; a
// choice is filex's own viewer or an app's interface.
func TestOpenWith_RefusesWhatIsNotAKindOrAnOpener(t *testing.T) {
	_, store, ada, _ := newPrefsFixture(t)
	h := handlers.NewOpenWith(store)
	for name, c := range map[string]struct{ path, body string }{
		"not a kind":          {"/api/me/open-with/a%20b", `{"handler":"builtin"}`},
		"a thumbnail handler": {"/api/me/open-with/jar", `{"handler":"app:pkglist"}`},
		"nothing":             {"/api/me/open-with/jar", `{"handler":""}`},
		"not json":            {"/api/me/open-with/jar", `builtin`},
	} {
		rec := openWithAs(t, h, ada, "PUT", c.path, c.body)
		assert.Equal(t, http.StatusBadRequest, rec.Code, name)
	}
	assert.Empty(t, choicesOf(t, openWithAs(t, h, ada, "GET", "/api/me/open-with", "")))
}

// TestOpenWith_OnePersonCannotReachAnother: the record hangs off the
// session's identity, nothing a client sends.
func TestOpenWith_OnePersonCannotReachAnother(t *testing.T) {
	prefs, store, ada, bob := newPrefsFixture(t)
	h := handlers.NewOpenWith(store)
	choicesOf(t, openWithAs(t, h, ada, "PUT", "/api/me/open-with/board", `{"handler":"builtin"}`))
	assert.Empty(t, choicesOf(t, openWithAs(t, h, bob, "GET", "/api/me/open-with", "")))
	assert.Empty(t, surfaceChoices(t, prefs, bob, "web"))
}

// TestUserPrefs_AccountIsNotASurface: the account row is written one kind at
// a time, never as a whole document a client sends.
func TestUserPrefs_AccountIsNotASurface(t *testing.T) {
	prefs, _, ada, _ := newPrefsFixture(t)
	assert.Equal(t, http.StatusBadRequest, prefsPut(t, prefs, ada, handlers.SurfaceAccount, `{"prefs":{"openWith":{"board":"app:x/y"}}}`).Code)
	assert.Equal(t, http.StatusBadRequest, prefsGet(t, prefs, ada, handlers.SurfaceAccount).Code)
}
