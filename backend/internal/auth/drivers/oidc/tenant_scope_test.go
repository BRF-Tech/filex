package oidc_test

// Which account an SSO sign-in opens, at the driver (docs/SSO.md): it never
// leaves the tenant the driver signs people in to - Store.GetUserByEmail, the
// lookup across the whole platform, is not asked at all - it reads
// `email_verified` (true only when it says true), and it binds the account to
// the identity (issuer, sub) that first opened it.
//
// Written against what the code before 0.50 has (an unpinned driver, the
// SetProviderID pin), so they run against it too (red: it asked
// GetUserByEmail, ignored email_verified and the subject).

import (
	"context"
	"sync/atomic"
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

// spyStore counts the platform-wide lookup.
type spyStore struct {
	db.Store
	byEmail atomic.Int32
}

func (s *spyStore) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	s.byEmail.Add(1)
	return s.Store.GetUserByEmail(ctx, email)
}

func spyDriver(t *testing.T, idp *fakeidp.IdP, extra map[string]any) (*oidc.Driver, *spyStore) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	spy := &spyStore{Store: store}
	drv := oidc.New(spy)
	cfg := map[string]any{"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": redirectURL, "role_claim": "groups"}
	for k, v := range extra {
		cfg[k] = v
	}
	require.NoError(t, drv.Init(context.Background(), cfg))
	return drv, spy
}

func inTenant(t *testing.T, store db.Store, tenantID int64, email string) *model.User {
	t.Helper()
	ctx := context.Background()
	u, err := store.CreateUser(ctx, email, "", model.RoleAdmin, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, u.ID, tenantID, ""))
	return u
}

// ⚠⚠ The OIDC path never asks the platform-wide lookup: a new person, an
// existing account (by its address, then by its identity), a pinned driver.
func TestTenantScope_GetUserByEmailIsNeverAsked(t *testing.T) {
	idp := fakeidp.New(t)
	drv, spy := spyDriver(t, idp, nil)
	ctx := context.Background()

	idp.SignIn("new@example.test", "sub-new")
	_, _, err := callback(t, drv)
	require.NoError(t, err)

	_, err = spy.CreateUser(ctx, "old@example.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	idp.SignIn("old@example.test", "sub-old")
	_, _, err = callback(t, drv)
	require.NoError(t, err)
	_, _, err = callback(t, drv)
	require.NoError(t, err)

	pinned, pspy := spyDriver(t, idp, nil)
	beta, err := pspy.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "beta", Enabled: true})
	require.NoError(t, err)
	pinned.SetProviderID(beta.ID)
	idp.SignIn("someone@beta.example", "sub-b")
	_, _, err = callback(t, pinned)
	require.NoError(t, err)
	_, _, err = callback(t, pinned)
	require.NoError(t, err)

	assert.Zero(t, spy.byEmail.Load(), "an OIDC sign-in looked an account up across the whole platform")
	assert.Zero(t, pspy.byEmail.Load(), "a pinned OIDC sign-in looked an account up across the whole platform")
}

// A driver nobody pinned signs people in to the platform's own tenant: the
// address of another tenant's administrator opens nothing, says nothing.
func TestTenantScope_AnUnpinnedDriverStaysInThePlatformsTenant(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := spyDriver(t, idp, nil)
	ctx := context.Background()
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "beta", Enabled: true})
	require.NoError(t, err)
	victim := inTenant(t, store, beta.ID, "victim@beta.example")

	for _, verified := range []bool{false, true} {
		idp.SignIn("victim@beta.example", "attacker")
		idp.SetExtraClaims(map[string]any{"email_verified": verified})
		got, _, err := callback(t, drv)
		require.Error(t, err, "email_verified=%v: signed in to another tenant's account", verified)
		assert.Nil(t, got)
		assert.Empty(t, auth.SSOReason(err), "no reason code: the page says nothing about another tenant")
	}
	again, err := store.GetUser(ctx, victim.ID)
	require.NoError(t, err)
	require.NotNil(t, again.ProviderID)
	assert.Equal(t, beta.ID, *again.ProviderID)
}

// email_verified counts only when it says true. An existing account bound to
// nobody is not opened by an unverified address; a provider the operator
// trusts (trust_email) opens it whatever the claim says.
func TestTenantScope_AnUnverifiedAddressOpensNoUnboundAccount(t *testing.T) {
	for _, tc := range []struct {
		name  string
		claim any
		trust bool
		ok    bool
	}{
		{"true", true, false, true},
		{"the string true", "true", false, true},
		{"false", false, false, false},
		{"absent", nil, false, false},
		{"a string that is not true", "yes", false, false},
		{"false, trusted provider", false, true, true},
		{"absent, trusted provider", nil, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idp := fakeidp.New(t)
			drv, store := spyDriver(t, idp, map[string]any{"trust_email": tc.trust})
			u, err := store.CreateUser(context.Background(), "ada@example.test", "", model.RoleUser, "en", model.TimezoneUnset)
			require.NoError(t, err)
			idp.SignIn("ada@example.test", "sub-ada")
			idp.SetExtraClaims(map[string]any{"email_verified": tc.claim})
			got, _, err := callback(t, drv)
			if tc.ok {
				require.NoError(t, err)
				assert.Equal(t, u.ID, got.ID)
				return
			}
			require.Error(t, err)
			assert.Nil(t, got)
			assert.Equal(t, "email_unverified", auth.SSOReason(err))
		})
	}
}

// Once bound, the identity is what counts: the account signs in by it, even
// with an unverified address; the same address with another identity is
// refused (identity_mismatch).
func TestTenantScope_TheFirstIdentityKeepsTheAccount(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := spyDriver(t, idp, nil)
	_, err := store.CreateUser(context.Background(), "ada@example.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	idp.SignIn("ada@example.test", "sub-ada")
	first, _, err := callback(t, drv)
	require.NoError(t, err)

	idp.SetExtraClaims(map[string]any{"email_verified": false})
	again, _, err := callback(t, drv)
	require.NoError(t, err, "the bound identity signs in without a verified address")
	assert.Equal(t, first.ID, again.ID)

	idp.SetExtraClaims(nil)
	idp.SignIn("ada@example.test", "sub-mallory")
	got, _, err := callback(t, drv)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, "identity_mismatch", auth.SSOReason(err))
}

// An account that kept only a subject before 0.50 (no issuer): the same
// subject completes the bind, another one is refused.
func TestTenantScope_ASubjectKeptBeforeCompletesItsBind(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := spyDriver(t, idp, nil)
	ctx := context.Background()
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", Name: "beta", Enabled: true})
	require.NoError(t, err)
	u, err := store.CreateUser(ctx, "ayse@beta.example", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, u.ID, beta.ID, "sub-ayse"))
	drv.SetProviderID(beta.ID)

	idp.SignIn("ayse@beta.example", "sub-other")
	got, _, err := callback(t, drv)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, "identity_mismatch", auth.SSOReason(err))

	idp.SignIn("ayse@beta.example", "sub-ayse")
	idp.SetExtraClaims(map[string]any{"email_verified": false})
	got, _, err = callback(t, drv)
	require.NoError(t, err, "the subject it kept is the same person")
	assert.Equal(t, u.ID, got.ID)
}

// A new person whose address is not verified gets an account opened switched
// off, waiting for an administrator, and the sign-in says so.
func TestTenantScope_ANewUnverifiedPersonWaitsForAnAdministrator(t *testing.T) {
	idp := fakeidp.New(t)
	drv, store := spyDriver(t, idp, nil)
	idp.SignIn("new@example.test", "sub-new")
	idp.SetExtraClaims(map[string]any{"email_verified": nil})
	got, _, err := callback(t, drv)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.Equal(t, "account_pending", auth.SSOReason(err))
	u, err := store.GetUserByEmail(context.Background(), "new@example.test")
	require.NoError(t, err)
	assert.False(t, u.Enabled)
}
