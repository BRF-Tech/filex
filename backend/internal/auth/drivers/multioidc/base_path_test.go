package multioidc_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/multioidc"
	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

// Under a base path (FILEX_BASE_PATH) every tenant host serves filex under it
// too, so a tenant's default OIDC callback is https://<tenant>/<base>/api/…,
// and the state cookie the callback reads is scoped to the base. Without the
// base the IdP would send the browser back to the host's root, where filex
// does not answer.
func TestTenantOIDCStartUnderABasePath(t *testing.T) {
	idp := fakeidp.New(t)
	_, store := testutil.NewTestDB(t)
	tenant(t, store, "tenant-a", idp)
	m := multioidc.New(store, nil)

	start := func(base string) *http.Response {
		r := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start", nil)
		r.Host = "files.tenant-a.test"
		r = r.WithContext(basepath.With(r.Context(), base))
		rec := httptest.NewRecorder()
		require.NoError(t, m.StartFlow(rec, r))
		return rec.Result()
	}

	resp := start("/filex")
	require.Equal(t, http.StatusFound, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "https://files.tenant-a.test/filex/api/auth/oidc/callback", loc.Query().Get("redirect_uri"))
	var state *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "filex_oidc_state" {
			state = c
		}
	}
	require.NotNil(t, state, "the flow sets its state cookie")
	require.Equal(t, "/filex", state.Path)
}

// At the root the tenant's callback is what it always was.
func TestTenantOIDCStartAtTheRoot(t *testing.T) {
	idp := fakeidp.New(t)
	_, store := testutil.NewTestDB(t)
	tenant(t, store, "tenant-b", idp)
	m := multioidc.New(store, nil)

	r := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start", nil)
	r.Host = "files.tenant-b.test"
	rec := httptest.NewRecorder()
	require.NoError(t, m.StartFlow(rec, r))
	loc, err := url.Parse(rec.Result().Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "https://files.tenant-b.test/api/auth/oidc/callback", loc.Query().Get("redirect_uri"))
}
