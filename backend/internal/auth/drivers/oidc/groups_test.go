package oidc_test

// The groups an SSO sign-in carries are recorded for permission rules that
// target an SSO group (package perm), and replaced — not merged — at every
// sign-in, so the IdP stays the authority on membership.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth/drivers/oidc"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/testutil/fakeidp"
)

func TestSignInRecordsTheGroupsClaim(t *testing.T) {
	idp := fakeidp.New(t)
	_, store := testutil.NewTestDB(t)
	drv := oidc.New(store)
	require.NoError(t, drv.Init(context.Background(), map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": redirectURL, "role_claim": "groups",
	}))
	ctx := context.Background()

	idp.SignIn("ada@example.test", "sub-ada")
	idp.SetExtraClaims(map[string]any{"groups": []any{"staff", "contractors"}})
	signIn(t, drv)
	u, err := store.GetUserByEmail(ctx, "ada@example.test")
	require.NoError(t, err)
	groups, err := store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"contractors", "staff"}, groups)

	// Removed from "contractors" at the IdP: gone here at the next sign-in.
	idp.SetExtraClaims(map[string]any{"groups": []any{"staff"}})
	signIn(t, drv)
	groups, err = store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"staff"}, groups)

	// A token without the claim carries no groups.
	idp.SetExtraClaims(nil)
	signIn(t, drv)
	groups, err = store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	require.Empty(t, groups)
}

func TestFirstSignInGivesTheGroupsStartingRole(t *testing.T) {
	idp := fakeidp.New(t)
	_, store := testutil.NewTestDB(t)
	drv := oidc.New(store)
	require.NoError(t, drv.Init(context.Background(), map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": redirectURL, "role_claim": "groups",
	}))
	ctx := context.Background()
	auditors, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Auditors", Enabled: true, Permissions: perm.ReadOnly.With(perm.AdminAudit).Strings(),
		Targets: []model.PermRuleTarget{{Kind: model.PermTargetSSOGroup, Value: "audit"}},
	})
	require.NoError(t, err)

	idp.SignIn("ada@example.test", "sub-ada")
	idp.SetExtraClaims(map[string]any{"groups": []any{"staff", "audit"}})
	signIn(t, drv)
	u, err := store.GetUserByEmail(ctx, "ada@example.test")
	require.NoError(t, err)
	held, err := store.GetUserCustomRole(ctx, u.ID)
	require.NoError(t, err)
	require.Equal(t, auditors.ID, held, "a new account starts with the role its group names")
	require.Equal(t, model.RoleViewer, u.Role, "and with that role's base")

	// Only at creation: once the account exists, the role is the person's.
	require.NoError(t, store.SetUserCustomRole(ctx, u.ID, 0))
	signIn(t, drv)
	held, err = store.GetUserCustomRole(ctx, u.ID)
	require.NoError(t, err)
	require.Zero(t, held, "a later sign-in does not hand the role back")

	// A group no role names: the account starts with no custom role.
	idp.SignIn("bob@example.test", "sub-bob")
	idp.SetExtraClaims(map[string]any{"groups": []any{"staff"}})
	signIn(t, drv)
	bob, err := store.GetUserByEmail(ctx, "bob@example.test")
	require.NoError(t, err)
	held, err = store.GetUserCustomRole(ctx, bob.ID)
	require.NoError(t, err)
	require.Zero(t, held)
}
