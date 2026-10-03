package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// Only a classified failure carries a code to the sign-in page; everything
// else (an account in another tenant among them) is the one generic answer.
func TestSSOReason_OnlyAClassifiedFailureCarriesACode(t *testing.T) {
	assert.Empty(t, SSOReason(nil))
	assert.Empty(t, SSOReason(errors.New("oidc: lookup user: database is locked")))
	assert.Empty(t, SSOReason(fmt.Errorf("oidc: this email is registered to another tenant: %w",
		errors.New("UNIQUE constraint failed: users.email"))))

	e := SSORefused(SSOReasonExpired, errors.New("oidc: state mismatch"))
	assert.Equal(t, SSOReasonExpired, SSOReason(e))
	assert.Equal(t, "oidc: state mismatch", e.Error(), "the log line does not change")
	assert.Equal(t, SSOReasonExpired, SSOReason(fmt.Errorf("outer: %w", e)), "a wrapped refusal keeps its code")
	assert.ErrorIs(t, SSORefused(SSOReasonIdPError, ErrNoTenantForLogin), ErrNoTenantForLogin)

	assert.Equal(t, SSOReasonAutoCreateOff, SSOReason(&FirstLoginRefusal{Reason: ReasonAutoCreateOff}))
	assert.Equal(t, SSOReasonGroupNotAllowed, SSOReason(&FirstLoginRefusal{Reason: ReasonGroupNotAllowed}))
	// The operating-system providers' own refusals are not SSO answers.
	assert.Empty(t, SSOReason(&FirstLoginRefusal{Reason: ReasonForbiddenAccount}))
	assert.Empty(t, SSOReason(&FirstLoginRefusal{Reason: ReasonInvalidName}))
}

func TestLoginBlockReason_NamesTheGate(t *testing.T) {
	_, store := dbtest.NewTestDB(t)
	ctx := context.Background()
	gone, err := store.CreateProvider(ctx, &model.Provider{Slug: "gone", Name: "Gone", Enabled: false})
	require.NoError(t, err)
	live, err := store.CreateProvider(ctx, &model.Provider{Slug: "live", Name: "Live", Enabled: true})
	require.NoError(t, err)

	assert.Empty(t, LoginBlockReason(ctx, store, true, nil))
	assert.Empty(t, LoginBlockReason(ctx, store, true, &model.User{Enabled: true}))
	assert.Equal(t, SSOReasonAccountDisabled, LoginBlockReason(ctx, store, true, &model.User{Enabled: false, ProviderID: &gone.ID}),
		"the account's own gate is named before its tenant's")
	assert.Equal(t, SSOReasonTenantSuspended, LoginBlockReason(ctx, store, true, &model.User{Enabled: true, ProviderID: &gone.ID}))
	assert.Equal(t, SSOReasonMaintenance, LoginBlockReason(ctx, store, false, &model.User{Enabled: true, ProviderID: &live.ID}))
	assert.Empty(t, LoginBlockReason(ctx, store, true, &model.User{Enabled: true, ProviderID: &live.ID}))
	assert.True(t, LoginAllowed(ctx, store, true, &model.User{Enabled: true, ProviderID: &live.ID}))
	assert.False(t, LoginAllowed(ctx, store, true, &model.User{Enabled: true, ProviderID: &gone.ID}))
}

// ⚠⚠ A password provider's refusal after a right password is the wrong-password
// answer unless its operator switched show_refusal_reason on; then only the
// reasons the person may hear travel, still as ErrUnauthorized.
func TestRefusedAfterPassword_OffIsTheWrongPasswordAnswer(t *testing.T) {
	for _, r := range []string{ReasonAutoCreateOff, ReasonGroupNotAllowed, ReasonForbiddenAccount, ReasonInvalidName} {
		assert.Same(t, ErrUnauthorized, RefusedAfterPassword(false, &FirstLoginRefusal{Reason: r}), r)
	}
	for _, r := range []string{ReasonAutoCreateOff, ReasonGroupNotAllowed, ReasonForbiddenAccount} {
		err := RefusedAfterPassword(true, &FirstLoginRefusal{Reason: r})
		assert.ErrorIs(t, err, ErrUnauthorized, r)
		assert.Equal(t, r, SSOReason(err), r)
	}
	assert.Same(t, ErrUnauthorized, RefusedAfterPassword(true, &FirstLoginRefusal{Reason: ReasonInvalidName}))
	assert.Same(t, ErrUnauthorized, RefusedAfterPassword(true, errors.New("ldap: dial")))
	assert.Equal(t, ReasonForbiddenAccount, SSOReasonForbiddenAccount, "the code the web reads is the audit's reason")

	assert.False(t, TellsRefusal(nil), "off by default")
	assert.False(t, TellsRefusal(map[string]any{ShowRefusalReasonKey: ""}))
	assert.True(t, TellsRefusal(map[string]any{ShowRefusalReasonKey: "true"}))
	assert.True(t, TellsRefusal(map[string]any{ShowRefusalReasonKey: true}))
}
