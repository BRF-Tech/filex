//go:build linux

package pam

// A filex group linked to an operating-system group follows the machine at
// every PAM sign-in, as it does at an OIDC one (auth.RecordSignInGroups).
//
// RED PROOF (PR #78 as submitted): only the OIDC driver kept linked groups in
// step; a PAM sign-in recorded `id -Gn` and nothing more.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func TestSignIn_LinkedGroupsFollowTheMachine(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	r := newRig(t, map[string]any{"auto_create": true})
	staff, err := r.store.CreateGroup(ctx, &model.Group{Name: "Staff",
		Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "staff"}}})
	require.NoError(t, err)

	u, err := r.d.VerifyPassword(ctx, "alice", realPW)
	require.NoError(t, err)
	ms, err := r.store.ListGroupMembers(ctx, staff.ID)
	require.NoError(t, err)
	require.Len(t, ms, 1, "the machine's staff group put her in the linked filex group")
	assert.Equal(t, u.ID, ms[0].UserID)
	assert.Equal(t, model.GroupSourceSSO, ms[0].Source)

	r.w.group["alice"] = "alice"
	_, err = r.d.VerifyPassword(ctx, "alice", realPW)
	require.NoError(t, err)
	ms, err = r.store.ListGroupMembers(ctx, staff.ID)
	require.NoError(t, err)
	assert.Empty(t, ms, "out of the machine's group, out of the filex group")
}

// linkedGroupsIn makes the same linked group in two tenants and once
// install-wide — the fixture of every realm test below.
func linkedGroupsIn(t *testing.T, store db.Store, value string, acme, beta int64) (acmeG, betaG, everyone int64) {
	t.Helper()
	ctx := context.Background()
	mk := func(name string, provider *int64) int64 {
		g, err := store.CreateGroup(ctx, &model.Group{Name: name, ProviderID: provider,
			Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: value}}})
		require.NoError(t, err)
		return g.ID
	}
	return mk("Staff", &acme), mk("Staff", &beta), mk("Everyone", nil)
}

func memberIDs(t *testing.T, store db.Store, groupID int64) []int64 {
	t.Helper()
	ms, err := store.ListGroupMembers(context.Background(), groupID)
	require.NoError(t, err)
	out := []int64{}
	for _, m := range ms {
		out = append(out, m.UserID)
	}
	return out
}

// In a tenant's realm (#128) the same one rule runs, and a linked group of
// ANOTHER tenant — or an install-wide one, whose links match only the
// supertenant's sign-ins — never takes the account in.
func TestSignIn_LinkedGroupsStayInTheRealmsTenant(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	r := newRig(t, map[string]any{"auto_create": true, "multi_tenant": true})
	acme, err := r.store.CreateProvider(ctx, &model.Provider{Slug: "acme", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	beta, err := r.store.CreateProvider(ctx, &model.Provider{Slug: "beta", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	acmeStaff, betaStaff, everyone := linkedGroupsIn(t, r.store, "staff", acme.ID, beta.ID)

	ua, err := r.d.VerifyPassword(realmCtx(t, r.store, "acme"), "alice", realPW)
	require.NoError(t, err)
	assert.Equal(t, []int64{ua.ID}, memberIDs(t, r.store, acmeStaff), "acme's linked group takes acme's alice")
	assert.Empty(t, memberIDs(t, r.store, betaStaff), "beta's does not")
	assert.Empty(t, memberIDs(t, r.store, everyone), "nor does an install-wide group, from a tenant's sign-in")
	recorded, err := r.store.ListUserSSOGroups(ctx, ua.ID)
	require.NoError(t, err)
	assert.Contains(t, recorded, "staff", "the groups were recorded by the same helper")

	ub, err := r.d.VerifyPassword(realmCtx(t, r.store, "beta"), "alice", realPW)
	require.NoError(t, err)
	assert.Equal(t, []int64{ub.ID}, memberIDs(t, r.store, betaStaff))
	assert.Equal(t, []int64{ua.ID}, memberIDs(t, r.store, acmeStaff), "beta's sign-in leaves acme's group alone")
	assert.Empty(t, memberIDs(t, r.store, everyone))
}
