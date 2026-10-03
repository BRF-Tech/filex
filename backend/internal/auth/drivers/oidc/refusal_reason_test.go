package oidc_test

// Every way HandleCallback refuses a person, and the reason code each one hands
// the sign-in page (auth.SSOReason; docs/SSO.md "When an SSO sign-in is
// refused"). An account in another tenant hands none: the page answers it as
// it answers any failure.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

// callbackEdited is callback with the callback query rewritten first (a
// cancelled sign-in, another tab's state, a code the IdP never issued).
func callbackEdited(t *testing.T, drv *oidc.Driver, edit func(q url.Values)) error {
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
	q := url.Values{"code": {"any"}, "state": {state.Value}}
	edit(q)
	cb := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?"+q.Encode(), nil)
	cb.AddCookie(state)
	_, _, err := drv.HandleCallback(httptest.NewRecorder(), cb)
	return err
}

func TestRefusalReason_EachRefusalNamesItsCode(t *testing.T) {
	idp := fakeidp.New(t)
	drv, _ := flDriver(t, idp, map[string]any{"allowed_groups": "staff"})

	idp.SignIn("ada@example.test", "sub-ada")
	idp.SetExtraClaims(map[string]any{"groups": []any{"interns"}})
	_, _, err := callback(t, drv)
	require.Error(t, err)
	assert.Equal(t, auth.SSOReasonGroupNotAllowed, auth.SSOReason(err))

	closed, _ := flDriver(t, idp, map[string]any{"auto_create": false})
	_, _, err = callback(t, closed)
	assert.Equal(t, auth.SSOReasonAutoCreateOff, auth.SSOReason(err))

	idp.SetExtraClaims(map[string]any{"groups": []any{"staff"}})
	idp.SignIn("", "sub-nobody")
	_, _, err = callback(t, drv)
	require.Error(t, err)
	assert.Equal(t, auth.SSOReasonNoEmail, auth.SSOReason(err))
	assert.Equal(t, "oidc: id_token missing email claim", err.Error(), "the log line is the one it always was")

	err = callbackEdited(t, drv, func(q url.Values) { q.Set("state", "another-tab") })
	assert.Equal(t, auth.SSOReasonExpired, auth.SSOReason(err))
	assert.Equal(t, "oidc: state mismatch", err.Error())

	// A code the IdP never issued: WithClient makes its token endpoint refuse it.
	strict, _ := flDriver(t, fakeidp.New(t, fakeidp.WithClient("filex", "s3cret")), nil)
	err = callbackEdited(t, strict, func(q url.Values) { q.Set("code", "never-issued") })
	assert.Equal(t, auth.SSOReasonIdPError, auth.SSOReason(err))

	// The IdP's own error: a code for the page, its words for the log only.
	err = callbackEdited(t, drv, func(q url.Values) {
		q.Del("code")
		q.Set("error", "access_denied")
		q.Set("error_description", "user cancelled")
	})
	assert.Equal(t, auth.SSOReasonIdPDenied, auth.SSOReason(err))
	assert.Contains(t, err.Error(), "access_denied")
	assert.Contains(t, err.Error(), "user cancelled")
}

// The state is checked before the IdP's error: a callback this browser never
// started is "expired" whatever it claims the IdP said.
func TestRefusalReason_AForeignCallbackIsExpiredWhateverItSays(t *testing.T) {
	idp := fakeidp.New(t)
	drv, _ := flDriver(t, idp, nil)
	err := callbackEdited(t, drv, func(q url.Values) {
		q.Set("state", "forged")
		q.Set("error", "access_denied")
	})
	assert.Equal(t, auth.SSOReasonExpired, auth.SSOReason(err))
}

// ⚠⚠ A tenant-bound driver whose new account would collide with an account of
// another tenant: refused, and with NO reason code, so the page cannot tell it
// from any other failure.
func TestRefusalReason_AnAccountInAnotherTenantCarriesNoCode(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := flDriver(t, idp, nil)
	ctx := context.Background()
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "beta", Enabled: true})
	require.NoError(t, err)
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "acme", Enabled: true})
	require.NoError(t, err)
	u, err := store.CreateUser(ctx, "ada@example.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, u.ID, acme.ID, ""))
	drv.SetProviderID(beta.ID)

	idp.SignIn("ada@example.test", "sub-ada")
	got, _, err := callback(t, drv)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Empty(t, auth.SSOReason(err))
	assert.False(t, errors.Is(err, auth.ErrFirstLoginRefused), "not a first-login refusal: the rule let it through")
	assert.Contains(t, err.Error(), "registered to another tenant", "the log says why")
}
