package multioidc_test

// RP-initiated logout in multi-tenant mode: sign-out on a tenant's host ends
// the session at THAT tenant's IdP, with that tenant's client — the same
// host → provider resolution sign-in uses.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/multioidc"
	authoidc "github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

// The dispatcher is what handlers see as the OIDC driver.
var _ auth.OIDCLogoutDriver = (*multioidc.Dispatcher)(nil)

func tenant(t *testing.T, store db.Store, slug string, idp *fakeidp.IdP) {
	t.Helper()
	_, err := store.CreateProvider(context.Background(), &model.Provider{
		Slug: slug, Host: "files." + slug + ".test", AuthType: model.AuthTypeOIDC,
		OIDCIssuer: idp.Issuer(), OIDCClientID: "filex-" + slug, OIDCClientSecret: "s3cret",
		Enabled: true,
	})
	require.NoError(t, err)
}

func idTokenFrom(idp *fakeidp.IdP, clientID string) string {
	return idp.IDToken(map[string]any{
		"iss": idp.Issuer(), "aud": clientID, "sub": "u-1",
		"exp": time.Now().Add(time.Minute).Unix(),
	})
}

func logoutOn(host string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r.Host = host
	return r
}

func TestEndSessionURLGoesToTheTenantsOwnIdP(t *testing.T) {
	idpA, idpB := fakeidp.New(t), fakeidp.New(t)
	_, store := testutil.NewTestDB(t)
	tenant(t, store, "tenant-a", idpA)
	tenant(t, store, "tenant-b", idpB)
	m := multioidc.New(store, nil)
	back := "https://files.tenant-b.test/drive/login?signed_out=1"

	got := m.EndSessionURL(logoutOn("files.tenant-b.test"), idTokenFrom(idpB, "filex-tenant-b"), back)

	u, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, idpB.EndSessionEndpoint(), u.Scheme+"://"+u.Host+u.Path)
	require.Equal(t, "filex-tenant-b", u.Query().Get("client_id"))
	require.Equal(t, back, u.Query().Get("post_logout_redirect_uri"))

	// Tenant A's token presented on tenant B's host goes to nobody.
	require.Empty(t, m.EndSessionURL(logoutOn("files.tenant-b.test"), idTokenFrom(idpA, "filex-tenant-a"), back))
}

// A host with no tenant uses the config-file driver, as sign-in does.
func TestEndSessionURLFallsBackToTheConfigFileDriver(t *testing.T) {
	idp := fakeidp.New(t)
	_, store := testutil.NewTestDB(t)
	fallback := authoidc.New(store)
	require.NoError(t, fallback.Init(context.Background(), map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": "https://files.operator.test/api/auth/oidc/callback",
	}))
	m := multioidc.New(store, fallback)

	got := m.EndSessionURL(logoutOn("files.operator.test"), idTokenFrom(idp, "filex"), "https://files.operator.test/admin/login?signed_out=1")

	require.Contains(t, got, idp.EndSessionEndpoint()+"?")
}

func TestEndSessionURLWithNoIdPForTheHost(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	m := multioidc.New(store, nil)

	require.Empty(t, m.EndSessionURL(logoutOn("files.nobody.test"), "a.b.c", "https://files.nobody.test/admin/login"))
}
