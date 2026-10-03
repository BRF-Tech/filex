package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brf-tech/filex/backend/internal/auth"
)

// The provider test used to answer "OK" for any configuration at all
// (release-candidate sweep, 2026-09-21). It now asks the identity provider,
// and every case here is an answer it must read correctly.

// idp is a minimal OpenID provider: a discovery document and a token
// endpoint that knows one client.
func idp(t *testing.T, issuerOverride string, clientID, secret string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/main/.well-known/openid-configuration":
			iss := srv.URL + "/realms/main"
			if issuerOverride != "" {
				iss = issuerOverride
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 iss,
				"authorization_endpoint": srv.URL + "/auth",
				"token_endpoint":         srv.URL + "/token",
				"jwks_uri":               srv.URL + "/certs",
			})
		case "/token":
			id, pw, ok := r.BasicAuth()
			if !ok || id != clientID || pw != secret {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
				return
			}
			// A confidential client without service accounts: authenticated,
			// not allowed this grant. That IS "the IdP knows the client".
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized_client"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func probe(cfg map[string]any) []auth.ProbeCheck {
	return (&Driver{}).Probe(context.Background(), cfg, nil)
}

func byID(checks []auth.ProbeCheck, id string) (auth.ProbeCheck, bool) {
	for _, c := range checks {
		if c.ID == id {
			return c, true
		}
	}
	return auth.ProbeCheck{}, false
}

func TestProbe_EmptyRequiredFieldsFail(t *testing.T) {
	checks := probe(map[string]any{"issuer": "", "client_id": "", "redirect_url": ""})
	assert.Len(t, checks, 1)
	assert.Equal(t, auth.ProbeFail, checks[0].Status)
	assert.Equal(t, "issuer,client_id,redirect_url", checks[0].Params["fields"])
	assert.False(t, auth.ProbeOKAll(checks))
}

func TestProbe_AWorkingProviderAndItsClient(t *testing.T) {
	srv := idp(t, "", "filex", "s3cret")
	checks := probe(map[string]any{
		"issuer": srv.URL + "/realms/main/", "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": "https://files.example.com/api/auth/oidc/callback",
	})
	for _, id := range []string{"required", "discovery", "issuer", "endpoints", "client"} {
		c, ok := byID(checks, id)
		assert.True(t, ok, id)
		assert.Equal(t, auth.ProbeOK, c.Status, id)
	}
	r, _ := byID(checks, "redirect")
	assert.Equal(t, auth.ProbeUnchecked, r.Status, "the return address is only judged inside a real sign-in, and the test says so")
	assert.True(t, auth.ProbeOKAll(checks))
}

func TestProbe_AWrongSecretIsRefused(t *testing.T) {
	srv := idp(t, "", "filex", "s3cret")
	checks := probe(map[string]any{
		"issuer": srv.URL + "/realms/main", "client_id": "filex", "client_secret": "wrong",
		"redirect_url": "https://files.example.com/cb",
	})
	c, _ := byID(checks, "client")
	assert.Equal(t, auth.ProbeFail, c.Status)
	assert.Equal(t, "credentials", c.Params["reason"])
	assert.False(t, auth.ProbeOKAll(checks))
}

func TestProbe_APublicClientCannotBeCheckedAndSaysSo(t *testing.T) {
	srv := idp(t, "", "filex", "s3cret")
	checks := probe(map[string]any{
		"issuer": srv.URL + "/realms/main", "client_id": "filex",
		"redirect_url": "https://files.example.com/cb",
	})
	c, _ := byID(checks, "client")
	assert.Equal(t, auth.ProbeUnchecked, c.Status)
	assert.Equal(t, "public", c.Params["reason"])
}

func TestProbe_AnIssuerThatAnnouncesAnotherNameFails(t *testing.T) {
	srv := idp(t, "https://elsewhere.example.com/realms/main", "filex", "s3cret")
	checks := probe(map[string]any{
		"issuer": srv.URL + "/realms/main", "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": "https://files.example.com/cb",
	})
	c, _ := byID(checks, "issuer")
	assert.Equal(t, auth.ProbeFail, c.Status)
	assert.Equal(t, "https://elsewhere.example.com/realms/main", c.Params["announced"])
}

func TestProbe_AnAddressWithNoDiscoveryDocumentFails(t *testing.T) {
	srv := idp(t, "", "filex", "s3cret")
	checks := probe(map[string]any{
		"issuer": srv.URL + "/realms/wrong", "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": "https://files.example.com/cb",
	})
	c, _ := byID(checks, "discovery")
	assert.Equal(t, auth.ProbeFail, c.Status)
	assert.Equal(t, "status", c.Params["reason"])
	assert.Equal(t, "404", c.Params["status"])
}

// TestProbe_KeycloaksAnswerForAClientWithoutServiceAccounts: Keycloak answers
// the client-credentials question for a confidential client without service
// accounts - the usual client, deploy/keycloak-client-filex.json - with 401
// and `unauthorized_client` (ClientCredentialsGrantType: "Client not enabled
// to retrieve service account"). The client authenticated: the test passes.
// Measured against Keycloak 26.8 in e2e/realenv, where the 401 was read as a
// refused secret and the operator's SSO could not be saved without
// confirming a failed test. A wrong secret still fails.
func TestProbe_KeycloaksAnswerForAClientWithoutServiceAccounts(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/corp/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": srv.URL + "/realms/corp", "authorization_endpoint": srv.URL + "/auth",
				"token_endpoint": srv.URL + "/token", "jwks_uri": srv.URL + "/certs",
				"token_endpoint_auth_methods_supported": []string{"private_key_jwt", "client_secret_basic", "client_secret_post", "tls_client_auth", "client_secret_jwt"},
			})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			if id, pw, ok := r.BasicAuth(); !ok || id != "filex" || pw != "corp-client-secret" {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": "Invalid client or Invalid client credentials"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized_client", "error_description": "Client not enabled to retrieve service account"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := func(secret string) map[string]any {
		return map[string]any{
			"issuer": srv.URL + "/realms/corp", "client_id": "filex", "client_secret": secret,
			"redirect_url": "https://files.example.com/api/auth/oidc/callback",
		}
	}

	c, _ := byID(probe(cfg("corp-client-secret")), "client")
	assert.Equal(t, auth.ProbeOK, c.Status, "the client authenticated; it is only not allowed client_credentials")

	c, _ = byID(probe(cfg("wrong")), "client")
	assert.Equal(t, auth.ProbeFail, c.Status)
	assert.Equal(t, "credentials", c.Params["reason"])
}
