package cliclient_test

// The CLI against the real server (docs/MULTI-TENANCY.md, Realms): a realm is
// that tenant's, a tenant with an address of its own is reached through the
// handoff, a realm nobody has is a wrong password, and a single-tenant server
// ignores the realm.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/cliclient"
	"github.com/brf-tech/filex/backend/internal/config"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

const realmPass = "RealmPass!1"

// dialAll sends every request to the test server whatever host the URL names,
// so `http://files.acme.test` reaches it with that Host - as a tenant's own
// address does behind its DNS.
func dialAll(addr string) *http.Client {
	d := &net.Dialer{}
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		},
	}}
}

// whoAmI is the account a token is signed in as.
func whoAmI(t *testing.T, hc *http.Client, base, token string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/api/auth/me", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := hc.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var me struct {
		Email string `json:"email"`
		User  struct {
			Email string `json:"email"`
		} `json:"user"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&me))
	if me.Email != "" {
		return me.Email
	}
	return me.User.Email
}

func TestRealmLogin_AgainstTheServer(t *testing.T) {
	srv, _, store := testutil.NewTestServerCfg(t, func(c *config.Config) { c.MultiTenant = true })
	ctx := context.Background()
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", Host: "files.acme.test", AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "Beta", AuthType: model.AuthTypeLocal, Enabled: true})
	require.NoError(t, err)
	hash, err := authlocal.HashPassword(realmPass)
	require.NoError(t, err)
	for email, p := range map[string]*model.Provider{"alex@local": nil, "alex@acme.local": acme, "alex@beta.local": beta} {
		u, err := store.CreateUser(ctx, email, hash, model.RoleUser, "en", "UTC")
		require.NoError(t, err)
		if p != nil {
			require.NoError(t, store.SetUserProvider(ctx, u.ID, p.ID, ""))
		}
	}
	hc := dialAll(srv.Listener.Addr().String())
	login := func(realm, password string) (*cliclient.LoginResponse, error) {
		api := cliclient.New(cliclient.Conn{URL: srv.URL})
		api.HTTP = hc
		return api.Login(ctx, cliclient.LoginRequest{Email: "alex", Password: password, Realm: realm})
	}

	// No realm: the platform's own alex.
	lr, err := login("", realmPass)
	require.NoError(t, err)
	assert.Equal(t, "alex@local", whoAmI(t, hc, lr.URL, lr.Token))

	// beta has no address of its own: signed in right here, as beta's alex.
	lr, err = login("beta", realmPass)
	require.NoError(t, err)
	assert.False(t, lr.HandedOff)
	assert.Equal(t, srv.URL, lr.URL)
	assert.Equal(t, "alex@beta.local", whoAmI(t, hc, lr.URL, lr.Token))

	// acme has one: handed there, redeemed there, acme's alex.
	lr, err = login("acme", realmPass)
	require.NoError(t, err)
	assert.True(t, lr.HandedOff)
	assert.Equal(t, "http://files.acme.test", lr.URL)
	assert.Equal(t, "alex@acme.local", whoAmI(t, hc, lr.URL, lr.Token))

	// The saved address and realm sign in again, without a handoff.
	api := cliclient.New(cliclient.Conn{URL: lr.URL})
	api.HTTP = hc
	again, err := api.Login(ctx, cliclient.LoginRequest{Email: "alex", Password: realmPass, Realm: "acme"})
	require.NoError(t, err)
	assert.False(t, again.HandedOff, "already at the tenant's address")
	assert.Equal(t, "alex@acme.local", whoAmI(t, hc, again.URL, again.Token))

	// A realm nobody has: the answer a wrong password gets.
	_, errRealm := login("nobody", realmPass)
	_, errPass := login("beta", "not-it")
	require.Error(t, errRealm)
	require.Error(t, errPass)
	assert.True(t, cliclient.IsUnauthorized(errRealm))
	var a, b *cliclient.APIError
	require.ErrorAs(t, errRealm, &a)
	require.ErrorAs(t, errPass, &b)
	assert.Equal(t, b.Status, a.Status)
	assert.Equal(t, b.Message, a.Message)
}

// A single-tenant server does not read the realm: sending one changes nothing.
func TestRealmLogin_SingleTenantServerIgnoresIt(t *testing.T) {
	srv, _, store := testutil.NewTestServer(t)
	email, pass := testutil.SeedAdmin(t, store)
	api := cliclient.New(cliclient.Conn{URL: srv.URL})

	lr, err := api.Login(context.Background(), cliclient.LoginRequest{Email: email, Password: pass, Realm: "whatever"})
	require.NoError(t, err)
	assert.NotEmpty(t, lr.Token)
	assert.False(t, lr.HandedOff)
	assert.Equal(t, srv.URL, lr.URL)
	assert.Equal(t, email, whoAmI(t, http.DefaultClient, lr.URL, lr.Token))
}
