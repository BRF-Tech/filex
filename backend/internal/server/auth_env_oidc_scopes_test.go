package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// GitHub #137 (an IdP that sends a person's groups only for a scope of its
// own, VoidAuth): the provider built from FILEX_OIDC_* asked for openid,
// profile and email and nothing else, so with an environment-only setup there
// was no way to ask for groups. FILEX_OIDC_EXTRA_SCOPES now reaches the
// driver: the sign-in redirect names every scope once, the base ones first.
func TestEnvAuthEntries_OIDCAsksForTheExtraScopes(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"jwks_uri":               srv.URL + "/jwks",
		})
	}))
	t.Cleanup(srv.Close)

	scopesOf := func(t *testing.T, extra string) string {
		t.Helper()
		_, store := testutil.NewTestDB(t)
		cfg := config.Config{}
		cfg.Auth.Drivers = []string{"oidc"}
		cfg.Auth.DriversFrom = "FILEX_AUTH_DRIVERS"
		cfg.Auth.OIDC.Issuer = srv.URL
		cfg.Auth.OIDC.ClientID = "filex"
		cfg.Auth.OIDC.ClientSecret = "test-secret"
		cfg.Auth.OIDC.RedirectURL = "https://files.example.com/api/auth/oidc/callback"
		cfg.Auth.OIDC.ExtraScopes = extra

		entries, err := envAuthEntries(context.Background(), cfg, store, "")
		require.NoError(t, err)
		require.Len(t, entries, 1)
		e := entries[0]
		require.True(t, e.Live(), "the provider starts: %s", e.Err)
		flow, ok := e.Driver.(interface {
			StartFlow(http.ResponseWriter, *http.Request) error
		})
		require.True(t, ok, "the OIDC driver starts a sign-in")
		rec := httptest.NewRecorder()
		require.NoError(t, flow.StartFlow(rec, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/login", nil)))
		loc, err := url.Parse(rec.Header().Get("Location"))
		require.NoError(t, err)
		require.Equal(t, srv.URL+"/authorize", loc.Scheme+"://"+loc.Host+loc.Path)
		return loc.Query().Get("scope")
	}

	require.Equal(t, "openid profile email", scopesOf(t, ""), "nothing extra by default")
	require.Equal(t, "openid profile email groups offline_access", scopesOf(t, "groups, offline_access email groups"),
		"space or comma, each scope once, the base ones not twice")
}
