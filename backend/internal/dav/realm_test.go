package dav

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/model"
)

// WebDAV on a multi-tenant install (#128): the tenant comes from the Host the
// client reached — the same resolver the web page uses — so a drive mapped to
// a tenant's own address needs no realm; without one, `realm/name`; a bare name
// is the platform's own tenant. Each alex has their own password, so landing
// on the wrong account is a 401.
func TestDAV_RealmFromHost(t *testing.T) {
	ha := newTenantHarness(t)
	ctx := context.Background()
	acme, err := ha.raw.CreateProvider(ctx, &model.Provider{Slug: "acme", Host: "files.acme.test", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	mk := func(email, pass string, p *model.Provider) {
		hash, err := local.HashPassword(pass)
		require.NoError(t, err)
		u, err := ha.raw.CreateUser(ctx, email, hash, model.RoleUser, "en", "UTC")
		require.NoError(t, err)
		if p != nil {
			require.NoError(t, ha.raw.SetUserProvider(ctx, u.ID, p.ID, ""))
		}
	}
	mk("alex@local", "MainPass!1", nil)
	mk("alex@acme.local", "AcmePass!1", acme)

	propfind := func(host, user, pass string) int {
		r, err := http.NewRequest("PROPFIND", ha.srv.URL+"/dav/", nil)
		require.NoError(t, err)
		r.Header.Set("Depth", "0")
		r.SetBasicAuth(user, pass)
		if host != "" {
			r.Host = host
		}
		resp, err := ha.srv.Client().Do(r)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	require.Equal(t, http.StatusMultiStatus, propfind("files.acme.test", "alex", "AcmePass!1"), "acme's address: acme's alex")
	require.Equal(t, http.StatusUnauthorized, propfind("files.acme.test", "alex", "MainPass!1"), "the platform's alex is not acme's")
	require.Equal(t, http.StatusUnauthorized, propfind("files.acme.test", "/alex", "MainPass!1"), "the address and the realm disagree")
	require.Equal(t, http.StatusMultiStatus, propfind("", "alex", "MainPass!1"), "the platform's address: the platform's alex")
	require.Equal(t, http.StatusMultiStatus, propfind("", "acme/alex", "AcmePass!1"), "the realm in the user name")
	require.Equal(t, http.StatusUnauthorized, propfind("", "alex", "AcmePass!1"), "acme's alex without naming acme")
}
