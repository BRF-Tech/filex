package oidc_test

// The first-login rule for an SSO sign-in: auto_create, allowed_groups (judged
// against the same claim the role mapping reads), one ambiguous answer, one
// audit row that says why — and the tenant homing an OIDC account gets when the
// driver is bound to a tenant, unchanged.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

func flDriver(t *testing.T, idp *fakeidp.IdP, extra map[string]any) (*oidc.Driver, db.Store) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	drv := oidc.New(store)
	cfg := map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": redirectURL, "role_claim": "groups",
	}
	for k, v := range extra {
		cfg[k] = v
	}
	require.NoError(t, drv.Init(context.Background(), cfg))
	return drv, store
}

// callback runs the browser's code flow and returns the callback's own result.
func callback(t *testing.T, drv *oidc.Driver) (*model.User, string, error) {
	t.Helper()
	start := httptest.NewRecorder()
	require.NoError(t, drv.StartFlow(start, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start", nil)))
	var state *http.Cookie
	for _, c := range start.Result().Cookies() {
		if c.Name == "filex_oidc_state" {
			state = c
		}
	}
	require.NotNil(t, state)
	cb := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=any&state="+url.QueryEscape(state.Value), nil)
	cb.AddCookie(state)
	return drv.HandleCallback(httptest.NewRecorder(), cb)
}

func refusals(t *testing.T, store db.Store) []string {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	var out []string
	for _, r := range rows {
		if r.Action == auth.AuditFirstLoginRefused {
			out = append(out, r.Metadata["reason"].(string))
		}
	}
	return out
}

// UPGRADE: with no first-login setting a new person still gets an account.
func TestFirstLogin_DefaultOpensAnAccount(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := flDriver(t, idp, nil)
	idp.SignIn("ada@example.test", "sub-ada")
	u, tok, err := callback(t, drv)
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, "ada@example.test", u.Email)
	assert.Empty(t, refusals(t, store))
}

func TestFirstLogin_AutoCreateOffRefusesANewPersonAndAdmitsAnExistingOne(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := flDriver(t, idp, map[string]any{"auto_create": false})
	idp.SignIn("ada@example.test", "sub-ada")

	u, tok, err := callback(t, drv)
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrFirstLoginRefused)
	assert.Nil(t, u)
	assert.Empty(t, tok)
	_, gerr := store.GetUserByEmail(context.Background(), "ada@example.test")
	assert.Error(t, gerr, "nothing may be created")
	assert.Equal(t, []string{auth.ReasonAutoCreateOff}, refusals(t, store))

	_, err = store.CreateUser(context.Background(), "ada@example.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	u, _, err = callback(t, drv)
	require.NoError(t, err)
	assert.Equal(t, "ada@example.test", u.Email)
	assert.Len(t, refusals(t, store), 1, "an existing account is not a refusal")
}

func TestFirstLogin_AllowedGroupsAreTheDoor(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := flDriver(t, idp, map[string]any{"allowed_groups": "staff, Yöneticiler"})

	idp.SignIn("ada@example.test", "sub-ada")
	idp.SetExtraClaims(map[string]any{"groups": []any{"contractors", "STAFF"}})
	u, _, err := callback(t, drv)
	require.NoError(t, err)
	groups, err := store.ListUserSSOGroups(context.Background(), u.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"contractors", "STAFF"}, groups)

	idp.SignIn("can@example.test", "sub-can")
	idp.SetExtraClaims(map[string]any{"groups": []any{"YÖNETİCİLER"}})
	_, _, err = callback(t, drv)
	require.NoError(t, err, "the Turkish dotted/dotless I is one letter")

	idp.SignIn("bob@example.test", "sub-bob")
	idp.SetExtraClaims(map[string]any{"groups": []any{"interns"}})
	u, _, err = callback(t, drv)
	assert.ErrorIs(t, err, auth.ErrFirstLoginRefused)
	assert.Nil(t, u)
	_, gerr := store.GetUserByEmail(context.Background(), "bob@example.test")
	assert.Error(t, gerr)
	assert.Equal(t, []string{auth.ReasonGroupNotAllowed}, refusals(t, store))

	idp.SignIn("eve@example.test", "sub-eve")
	idp.SetExtraClaims(nil)
	_, _, err = callback(t, drv)
	assert.ErrorIs(t, err, auth.ErrFirstLoginRefused, "no groups claim at all is outside every group")
}

// The tenant-bound driver (one realm per tenant) still homes the new account in
// its tenant and stamps the subject, through the ONE creation path — and a
// refusal there creates nothing either.
func TestFirstLogin_TenantBoundDriverKeepsItsHoming(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := flDriver(t, idp, nil)
	ctx := context.Background()
	p, err := store.CreateProvider(ctx, &model.Provider{Slug: "globex", Name: "globex", Host: "globex.example.com", AuthType: "oidc", Enabled: true})
	require.NoError(t, err)
	drv.SetProviderID(p.ID)

	idp.SignIn("ayse@globex.example.com", "sub-ayse")
	u, _, err := callback(t, drv)
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, p.ID, *u.ProviderID)
	assert.Equal(t, "sub-ayse", u.OIDCSubject)

	closed, cstore := flDriver(t, idp, map[string]any{"auto_create": false})
	p2, err := cstore.CreateProvider(ctx, &model.Provider{Slug: "initech", Name: "initech", Host: "initech.example.com", AuthType: "oidc", Enabled: true})
	require.NoError(t, err)
	closed.SetProviderID(p2.ID)
	idp.SignIn("mert@initech.example.com", "sub-mert")
	_, _, err = callback(t, closed)
	assert.ErrorIs(t, err, auth.ErrFirstLoginRefused)
	users, err := cstore.ListUsers(ctx)
	require.NoError(t, err)
	for _, x := range users {
		assert.NotEqual(t, "mert@initech.example.com", x.Email)
	}
}
