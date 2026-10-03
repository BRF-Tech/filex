package windows

// A filex group linked to a Windows group follows the token at every sign-in,
// as it does at an OIDC one (auth.RecordSignInGroups).
//
// RED PROOF (PR #78 as submitted): only the OIDC driver kept linked groups in
// step; a Windows sign-in recorded the token's groups and nothing more.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func TestSignIn_LinkedGroupsFollowTheToken(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	fos := people()
	d, store := newDriver(t, fos, map[string]any{"auto_create": true})
	users, err := store.CreateGroup(ctx, &model.Group{Name: "Machine users",
		Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "Users"}}})
	require.NoError(t, err)

	u, _, err := d.Login(ctx, "ayse", "pw-ayse")
	require.NoError(t, err)
	ms, err := store.ListGroupMembers(ctx, users.ID)
	require.NoError(t, err)
	require.Len(t, ms, 1, "the token's Users group put her in the linked filex group")
	assert.Equal(t, u.ID, ms[0].UserID)
	assert.Equal(t, model.GroupSourceSSO, ms[0].Source)

	acct := fos.accounts[key("ayse", ".")]
	acct.groups = nil
	fos.accounts[key("ayse", ".")] = acct
	_, _, err = d.Login(ctx, "ayse", "pw-ayse")
	require.NoError(t, err)
	ms, err = store.ListGroupMembers(ctx, users.ID)
	require.NoError(t, err)
	assert.Empty(t, ms, "out of the Windows group, out of the filex group")
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
// ANOTHER tenant — or an install-wide one — never takes the account in.
func TestSignIn_LinkedGroupsStayInTheRealmsTenant(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	d, store := newDriver(t, people(), map[string]any{"auto_create": true, "multi_tenant": true})
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	beta, err := store.CreateProvider(ctx, &model.Provider{Slug: "beta", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	acmeUsers, betaUsers, everyone := linkedGroupsIn(t, store, "Users", acme.ID, beta.ID)

	ua, _, err := d.Login(windowsRealm(t, store, "acme"), "ayse", "pw-ayse")
	require.NoError(t, err)
	assert.Equal(t, []int64{ua.ID}, memberIDs(t, store, acmeUsers))
	assert.Empty(t, memberIDs(t, store, betaUsers))
	assert.Empty(t, memberIDs(t, store, everyone))

	ub, _, err := d.Login(windowsRealm(t, store, "beta"), "ayse", "pw-ayse")
	require.NoError(t, err)
	assert.Equal(t, []int64{ub.ID}, memberIDs(t, store, betaUsers))
	assert.Equal(t, []int64{ua.ID}, memberIDs(t, store, acmeUsers))
	assert.Empty(t, memberIDs(t, store, everyone))
}
