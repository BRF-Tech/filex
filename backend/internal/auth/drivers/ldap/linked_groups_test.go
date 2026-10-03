package ldap

// A filex group linked to a directory group follows the directory at every
// LDAP sign-in, exactly as it does at an OIDC one (auth.RecordSignInGroups):
// the person joins, and leaves when the directory drops them; the role the
// group gives moves the level underneath with them.
//
// RED PROOF (PR #78 as submitted): only the OIDC driver kept linked groups in
// step. An LDAP sign-in recorded its groups (user_sso_groups) and nothing
// more — the person never joined; and one put in by a link edit (which reads
// those recorded groups, group.SyncStoredSSO) never left when the directory
// dropped them.

import (
	"context"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func TestSignIn_LinkedGroupsFollowTheDirectory(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	fc := &fakeConn{userPassword: "pw", entries: []*goldap.Entry{
		groupEntry("cn=ayse,dc=example,dc=com", "ayse@example.com", "CN=Editors,DC=example,DC=com"),
	}}
	d, store := newDriver(t, fc, map[string]any{"group_attr": "memberOf"})
	look, err := store.CreatePermissionRule(ctx, &model.PermissionRule{Name: "Look", Enabled: true, Permissions: perm.ReadOnly.Strings()})
	require.NoError(t, err)
	editors, err := store.CreateGroup(ctx, &model.Group{Name: "Editors", RoleID: &look.ID,
		Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: "Editors"}}})
	require.NoError(t, err)

	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	ms, err := store.ListGroupMembers(ctx, editors.ID)
	require.NoError(t, err)
	require.Len(t, ms, 1, "the directory's Editors put her in the linked filex group")
	assert.Equal(t, model.GroupSourceSSO, ms[0].Source)
	assert.Equal(t, model.RoleViewer, u.Role, "the group's read-only role sets her level, in the account the sign-in answers")

	fc.entries = []*goldap.Entry{groupEntry("cn=ayse,dc=example,dc=com", "ayse@example.com")}
	u, _, err = d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	ms, err = store.ListGroupMembers(ctx, editors.ID)
	require.NoError(t, err)
	assert.Empty(t, ms, "out of the directory group, out of the filex group")
	assert.Equal(t, model.RoleUser, u.Role, "and the level from before comes back")
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
	t.Cleanup(perm.Invalidate)
	d, store, dir := uidFixture(t, map[string]any{"multi_tenant": true, "group_attr": "memberOf"}, "alex")
	dir.byUID["alex"] = goldap.NewEntry("uid=alex,ou=people,dc=example,dc=com",
		map[string][]string{"memberOf": {"CN=Staff,OU=Groups,DC=example,DC=com"}})
	acme := seedProvider(t, store, "acme", "")
	beta := seedProvider(t, store, "beta", "")
	acmeStaff, betaStaff, everyone := linkedGroupsIn(t, store, "Staff", acme, beta)

	ua, _, err := d.Login(inRealm(t, store, "acme"), "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, []int64{ua.ID}, memberIDs(t, store, acmeStaff))
	assert.Empty(t, memberIDs(t, store, betaStaff))
	assert.Empty(t, memberIDs(t, store, everyone))

	ub, _, err := d.Login(inRealm(t, store, "beta"), "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, []int64{ub.ID}, memberIDs(t, store, betaStaff))
	assert.Equal(t, []int64{ua.ID}, memberIDs(t, store, acmeStaff))
	assert.Empty(t, memberIDs(t, store, everyone))
}
