package oidc_test

// The groups an SSO sign-in carries are recorded for permission rules that
// target an SSO group (package perm), and replaced — not merged — at every
// sign-in, so the IdP stays the authority on membership.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
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
	require.Equal(t, model.AuthSourceSSO, u.AuthSource, "an account an SSO sign-in made says so on the Users page")

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

// A filex group linked to an SSO group (internal/group) follows the IdP at
// every sign-in: the account joins when the claim names the group and leaves
// when it stops — unless an administrator put it there by hand. The group's
// role moves the level underneath with it.
func TestSignInKeepsLinkedGroupsInStep(t *testing.T) {
	idp := fakeidp.New(t)
	_, store := testutil.NewTestDB(t)
	drv := oidc.New(store)
	require.NoError(t, drv.Init(context.Background(), map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": redirectURL, "role_claim": "groups",
	}))
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	lookOnly, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Look only", Enabled: true, Permissions: perm.ReadOnly.Strings(),
	})
	require.NoError(t, err)
	finance, err := store.CreateGroup(ctx, &model.Group{
		Name: "Finance", RoleID: &lookOnly.ID,
		Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "finance"}},
	})
	require.NoError(t, err)
	legal, err := store.CreateGroup(ctx, &model.Group{
		Name: "Legal", Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "legal"}},
	})
	require.NoError(t, err)
	groupsOf := func(uid int64) map[int64]string {
		ms, err := store.ListUserGroupMemberships(ctx, uid)
		require.NoError(t, err)
		out := map[int64]string{}
		for _, m := range ms {
			out[m.GroupID] = m.Source
		}
		return out
	}

	idp.SignIn("ada@example.test", "sub-ada")
	idp.SetExtraClaims(map[string]any{"groups": []any{"staff", "finance"}})
	signIn(t, drv)
	u, err := store.GetUserByEmail(ctx, "ada@example.test")
	require.NoError(t, err)
	require.Equal(t, map[int64]string{finance.ID: model.GroupSourceSSO}, groupsOf(u.ID))
	require.Equal(t, model.RoleViewer, u.Role, "the group's read-only role sets the level")

	// Added to Legal by hand, then moved at the IdP from finance to legal.
	require.NoError(t, store.AddGroupMember(ctx, legal.ID, u.ID))
	idp.SetExtraClaims(map[string]any{"groups": []any{"legal"}})
	signIn(t, drv)
	require.Equal(t, map[int64]string{legal.ID: model.GroupSourceManual}, groupsOf(u.ID),
		"left Finance with the IdP; Legal stays a manual membership")

	// No groups at all: the manual membership still stays.
	idp.SetExtraClaims(nil)
	signIn(t, drv)
	require.Equal(t, map[int64]string{legal.ID: model.GroupSourceManual}, groupsOf(u.ID))
}

// An administrator the IdP demotes at sign-in gets the level their group's
// role sets at once — no group role could set it while they were one, and
// no membership moves on that sign-in (third review).
func TestSignInDemotionFollowsTheGroupsRole(t *testing.T) {
	idp := fakeidp.New(t)
	_, store := testutil.NewTestDB(t)
	drv := oidc.New(store)
	require.NoError(t, drv.Init(context.Background(), map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": redirectURL, "role_claim": "groups", "admin_group": "admins",
	}))
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	// Another administrator first: the mapping never demotes the setup
	// account or the last admin.
	_, err := store.CreateUser(ctx, "root@example.test", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	_, err = store.CreateUser(ctx, "second@example.test", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	lookOnly, err := store.CreatePermissionRule(ctx, &model.PermissionRule{Name: "Look only", Enabled: true, Permissions: perm.ReadOnly.Strings()})
	require.NoError(t, err)
	_, err = store.CreateGroup(ctx, &model.Group{
		Name: "Auditors", RoleID: &lookOnly.ID, Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "audit"}},
	})
	require.NoError(t, err)

	idp.SignIn("ada@example.test", "sub-ada")
	idp.SetExtraClaims(map[string]any{"groups": []any{"admins", "audit"}})
	signIn(t, drv)
	u, err := store.GetUserByEmail(ctx, "ada@example.test")
	require.NoError(t, err)
	require.Equal(t, model.RoleAdmin, u.Role)

	idp.SetExtraClaims(map[string]any{"groups": []any{"audit"}})
	signIn(t, drv)
	u, err = store.GetUserByEmail(ctx, "ada@example.test")
	require.NoError(t, err)
	require.Equal(t, model.RoleViewer, u.Role, "demoted, and the read-only group role caps them at once")
}

// Ninth review: a level kept from before a group's role is forgotten once
// the IdP makes the account an administrator — the role routes forget it on
// a promotion, the admin mapping does not pass through them — so a later
// demotion lands on the mapping's role, not on the stale level.
func TestSignInPromotionForgetsTheKeptLevel(t *testing.T) {
	idp := fakeidp.New(t)
	_, store := testutil.NewTestDB(t)
	drv := oidc.New(store)
	require.NoError(t, drv.Init(context.Background(), map[string]any{
		"issuer": idp.Issuer(), "client_id": "filex", "client_secret": "s3cret",
		"redirect_url": redirectURL, "role_claim": "groups", "admin_group": "admins",
	}))
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	for _, e := range []string{"root@example.test", "second@example.test"} {
		_, err := store.CreateUser(ctx, e, "x", model.RoleAdmin, "en", "UTC")
		require.NoError(t, err)
	}
	writers, err := store.CreatePermissionRule(ctx, &model.PermissionRule{Name: "Writers", Enabled: true, Permissions: perm.Standard.Strings()})
	require.NoError(t, err)
	_, err = store.CreateGroup(ctx, &model.Group{
		Name: "Writers", RoleID: &writers.ID, Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "writers"}},
	})
	require.NoError(t, err)
	level := func() string {
		u, err := store.GetUserByEmail(ctx, "ada@example.test")
		require.NoError(t, err)
		return u.Role
	}

	idp.SignIn("ada@example.test", "sub-ada")
	idp.SetExtraClaims(nil)
	signIn(t, drv)
	u, err := store.GetUserByEmail(ctx, "ada@example.test")
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserRole(ctx, u.ID, model.RoleViewer))

	idp.SetExtraClaims(map[string]any{"groups": []any{"writers"}})
	signIn(t, drv)
	require.Equal(t, model.RoleUser, level(), "the group's writing role; Viewer kept to give back")

	idp.SetExtraClaims(map[string]any{"groups": []any{"admins", "writers"}})
	signIn(t, drv)
	require.Equal(t, model.RoleAdmin, level())

	idp.SetExtraClaims(nil)
	signIn(t, drv)
	assert.Equal(t, model.RoleUser, level(), "demoted to the mapping's role — not the Viewer kept from before the administrator")
}
