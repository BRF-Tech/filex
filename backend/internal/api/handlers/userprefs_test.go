package handlers_test

// Per-user, per-surface interface preferences (00047, handlers/userprefs.go).
//
// The point of the table is that a choice follows the PERSON, so the tests
// that matter are the ones about whose document a caller reaches and which
// surface it belongs to.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

func newPrefsFixture(t *testing.T) (*handlers.UserPrefs, db.Store, *model.User, *model.User) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	ada, err := store.CreateUser(context.Background(), "ada@example.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	bob, err := store.CreateUser(context.Background(), "bob@example.test", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	return handlers.NewUserPrefs(store), store, ada, bob
}

func prefsGet(t *testing.T, h *handlers.UserPrefs, u *model.User, surface string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/me/prefs?surface="+surface, nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req.WithContext(auth.WithUser(req.Context(), u)))
	return rec
}

func prefsPut(t *testing.T, h *handlers.UserPrefs, u *model.User, surface, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/me/prefs?surface="+surface, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Put(rec, req.WithContext(auth.WithUser(req.Context(), u)))
	return rec
}

// TestUserPrefs_OnePersonCannotReachAnother is the whole reason the document
// hangs off the session's identity and nothing else: there is no user id in
// the path or the body for a client to substitute.
func TestUserPrefs_OnePersonCannotReachAnother(t *testing.T) {
	h, _, ada, bob := newPrefsFixture(t)

	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "web", `{"prefs":{"theme":"dark"}}`).Code)
	require.Equal(t, http.StatusOK, prefsPut(t, h, bob, "web", `{"prefs":{"theme":"light"}}`).Code)

	assert.Contains(t, prefsGet(t, h, ada, "web").Body.String(), `"dark"`)
	got := prefsGet(t, h, bob, "web").Body.String()
	assert.Contains(t, got, `"light"`)
	assert.NotContains(t, got, `"dark"`, "one account read another's appearance")

	// An unauthenticated caller reaches nobody's.
	req := httptest.NewRequest("GET", "/api/me/prefs?surface=web", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestUserPrefs_WebAndDesktopAreSeparateRows: the desktop app is a window on
// somebody's own machine and keeps its own appearance. Merging the two would
// be the complaint this table answers, one level up.
func TestUserPrefs_WebAndDesktopAreSeparateRows(t *testing.T) {
	h, _, ada, bob := newPrefsFixture(t)

	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "web", `{"prefs":{"theme":"dark"}}`).Code)
	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "desktop", `{"prefs":{"theme":"light"}}`).Code)

	assert.Contains(t, prefsGet(t, h, ada, "web").Body.String(), `"dark"`)
	assert.Contains(t, prefsGet(t, h, ada, "desktop").Body.String(), `"light"`)

	// Nothing stored yet is an empty object, not a 404: it is every account's
	// first day on every surface it has not opened.
	rec := prefsGet(t, h, bob, "desktop")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"prefs":{}`)
}

// A person has ONE language. The switcher writes this document; everything
// the server renders for somebody — an app plugin's screens, the label on a
// queued job, mail — reads `users.locale`. They drifted the moment anybody
// used the switcher: a Turkish window with an English signing wizard inside
// it, on the same page.
func TestUserPrefs_ChoosingALanguageSetsItOnTheAccount(t *testing.T) {
	h, store, ada, _ := newPrefsFixture(t)
	ctx := context.Background()

	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "web", `{"prefs":{"theme":"dark","locale":"tr"}}`).Code)
	got, err := store.GetUser(ctx, ada.ID)
	require.NoError(t, err)
	assert.Equal(t, "tr", got.Locale, "the account keeps the language the person chose")
	assert.Equal(t, "UTC", got.Timezone, "the time zone is not touched")

	// The desktop app is the same person: the last language chosen wins,
	// whichever window it was chosen in.
	ada.Locale = "tr"
	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "desktop", `{"prefs":{"locale":"en"}}`).Code)
	got, err = store.GetUser(ctx, ada.ID)
	require.NoError(t, err)
	assert.Equal(t, "en", got.Locale)

	// A document with no language leaves the account alone.
	ada.Locale = "en"
	require.Equal(t, http.StatusOK, prefsPut(t, h, ada, "web", `{"prefs":{"palette":"lilac"}}`).Code)
	got, err = store.GetUser(ctx, ada.ID)
	require.NoError(t, err)
	assert.Equal(t, "en", got.Locale)
}

// TestUserPrefs_BadSurfaceIsRefused: an unknown surface must NOT be folded
// into "web" — a typo that silently wrote a desktop theme into the browser row
// would look exactly like the bug this table exists to fix.
func TestUserPrefs_BadSurfaceIsRefused(t *testing.T) {
	h, _, ada, _ := newPrefsFixture(t)
	assert.Equal(t, http.StatusBadRequest, prefsGet(t, h, ada, "tablet").Code)
	assert.Equal(t, http.StatusBadRequest, prefsPut(t, h, ada, "tablet", `{"prefs":{}}`).Code)

	// An absent surface means the browser, which is what every existing
	// caller is.
	req := httptest.NewRequest("GET", "/api/me/prefs", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req.WithContext(auth.WithUser(req.Context(), ada)))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"surface":"web"`)
}

// TestUserPrefs_ShapeAndSizeAreEnforcedServerSide: a limit the client applies
// is a limit the client can skip.
func TestUserPrefs_ShapeAndSizeAreEnforcedServerSide(t *testing.T) {
	h, _, ada, _ := newPrefsFixture(t)

	assert.Equal(t, http.StatusBadRequest, prefsPut(t, h, ada, "web", `{"prefs":[1,2,3]}`).Code)
	assert.Equal(t, http.StatusBadRequest, prefsPut(t, h, ada, "web", `{"prefs":"hello"}`).Code)
	assert.Equal(t, http.StatusBadRequest, prefsPut(t, h, ada, "web", `not json`).Code)

	big := `{"prefs":{"x":"` + strings.Repeat("a", 70<<10) + `"}}`
	assert.Equal(t, http.StatusRequestEntityTooLarge, prefsPut(t, h, ada, "web", big).Code)

	// …and a document the server refused was never stored.
	assert.Contains(t, prefsGet(t, h, ada, "web").Body.String(), `"prefs":{}`)
}
