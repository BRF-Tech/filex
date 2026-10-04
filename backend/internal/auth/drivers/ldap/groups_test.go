package ldap

import (
	"context"
	"errors"
	"strings"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

// groupConn is fakeConn with a directory of groups: a search whose filter
// names a member is answered with groups (or groupErr).
type groupConn struct {
	*fakeConn
	groups   []*goldap.Entry
	groupErr error
}

func (g *groupConn) Search(req *goldap.SearchRequest) (*goldap.SearchResult, error) {
	if strings.Contains(req.Filter, "member") {
		g.searches = append(g.searches, req)
		if g.groupErr != nil {
			return nil, g.groupErr
		}
		return &goldap.SearchResult{Entries: g.groups}, nil
	}
	return g.fakeConn.Search(req)
}

func person(dn, mail string, memberOf ...string) *goldap.Entry {
	return goldap.NewEntry(dn, map[string][]string{"mail": {mail}, "memberOf": memberOf})
}

func linkedGroup(t *testing.T, store db.Store, name string, values ...string) *model.Group {
	t.Helper()
	g := &model.Group{Name: name}
	for _, v := range values {
		g.Links = append(g.Links, model.GroupLink{Kind: model.GroupLinkLDAP, Value: v})
	}
	out, err := store.CreateGroup(context.Background(), g)
	require.NoError(t, err)
	return out
}

func membership(t *testing.T, store db.Store, userID int64) map[int64]string {
	t.Helper()
	ms, err := store.ListUserGroupMemberships(context.Background(), userID)
	require.NoError(t, err)
	out := map[int64]string{}
	for _, m := range ms {
		out[m.GroupID] = m.Source
	}
	return out
}

// A browser sign-in reads memberOf and puts the account in every filex group
// linked to one of those LDAP groups — by DN or by common name, in any case
// or spacing — and takes it out when the directory stops saying so.
func TestLDAPGroups_SignInJoinsAndLeavesLinkedGroups(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries: []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com",
			"CN=Finance, OU=Groups, DC=example, DC=com", "cn=Ops,ou=groups,dc=example,dc=com")},
		userPassword: "pw",
	}
	d, store := newLinkedDriver(t, fc, nil)
	byDN := linkedGroup(t, store, "Finance", "cn=finance,ou=groups,dc=example,dc=com")
	byName := linkedGroup(t, store, "Ops", "OPS")
	other := linkedGroup(t, store, "Sales", "sales")

	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, map[int64]string{byDN.ID: model.GroupSourceLDAP, byName.ID: model.GroupSourceLDAP}, membership(t, store, u.ID))
	assert.NotContains(t, membership(t, store, u.ID), other.ID)

	stored, err := store.ListUserLDAPGroups(ctx, u.ID)
	require.NoError(t, err)
	assert.Contains(t, stored, "cn=finance,ou=groups,dc=example,dc=com")
	assert.Contains(t, stored, "ops")

	// The directory drops Ops: the next sign-in leaves it.
	fc.entries = []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=finance,ou=groups,dc=example,dc=com")}
	_, _, err = d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, map[int64]string{byDN.ID: model.GroupSourceLDAP}, membership(t, store, u.ID))
}

// Someone added by hand stays whatever the directory says.
func TestLDAPGroups_ManualMembershipStays(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries:      []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "pw",
	}
	d, store := newLinkedDriver(t, fc, nil)
	g := linkedGroup(t, store, "Finance", "finance")
	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	require.NoError(t, store.AddGroupMember(ctx, g.ID, u.ID))

	_, _, err = d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, map[int64]string{g.ID: model.GroupSourceManual}, membership(t, store, u.ID))
}

// With group_filter the groups are found by a search, as the service
// account, with %s the person's DN and %u the name they signed in with.
func TestLDAPGroups_GroupSearch(t *testing.T) {
	ctx := context.Background()
	gc := &groupConn{
		fakeConn: &fakeConn{
			entries:      []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=ignored,dc=example,dc=com")},
			userPassword: "pw",
		},
		groups: []*goldap.Entry{goldap.NewEntry("cn=finance,ou=groups,dc=example,dc=com", nil)},
	}
	store := newStore(t)
	d := New(store)
	require.NoError(t, d.Init(ctx, map[string]any{
		"url": "ldaps://directory.invalid", "base_dn": "dc=example,dc=com",
		"bind_dn": "cn=svc,dc=example,dc=com", "bind_password": "svc",
		"group_filter":  "(|(member=%s)(memberUid=%u))",
		"group_base_dn": "ou=groups,dc=example,dc=com",
	}))
	d.dial = func(context.Context) (conn, error) { return gc, nil }
	finance := linkedGroup(t, store, "Finance", "finance")
	ignored := linkedGroup(t, store, "Ignored", "ignored")

	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, map[int64]string{finance.ID: model.GroupSourceLDAP}, membership(t, store, u.ID),
		"the search decides; memberOf is not read beside it")
	assert.NotContains(t, membership(t, store, u.ID), ignored.ID)

	last := gc.searches[len(gc.searches)-1]
	assert.Equal(t, "ou=groups,dc=example,dc=com", last.BaseDN)
	assert.Equal(t, `(|(member=cn=ayse,dc=example,dc=com)(memberUid=ayse@example.com))`, last.Filter)
	lastBind := gc.binds[len(gc.binds)-1]
	assert.Equal(t, "cn=svc,dc=example,dc=com", lastBind.dn, "the group search runs as the service account")
}

// A directory that cannot answer the group search takes nobody out of their
// groups: the sign-in succeeds and the memberships stay as they were.
func TestLDAPGroups_UnreadableGroupsChangeNothing(t *testing.T) {
	ctx := context.Background()
	gc := &groupConn{
		fakeConn: &fakeConn{
			entries:      []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com")},
			userPassword: "pw",
		},
		groups: []*goldap.Entry{goldap.NewEntry("cn=finance,ou=groups,dc=example,dc=com", nil)},
	}
	store := newStore(t)
	d := New(store)
	require.NoError(t, d.Init(ctx, map[string]any{
		"url": "ldaps://directory.invalid", "base_dn": "dc=example,dc=com",
		"group_filter": "(member=%s)",
	}))
	d.dial = func(context.Context) (conn, error) { return gc, nil }
	finance := linkedGroup(t, store, "Finance", "finance")

	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	require.Contains(t, membership(t, store, u.ID), finance.ID)

	gc.groupErr = goldap.NewError(goldap.LDAPResultUnavailable, errors.New("busy"))
	_, _, err = d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err, "the password was right; the group read failing does not refuse the sign-in")
	assert.Contains(t, membership(t, store, u.ID), finance.ID, "an unreadable directory takes nobody out")
}

// The protocols (VerifyPassword) never touch memberships: they present the
// password on every request.
func TestLDAPGroups_ProtocolLoginDoesNotSync(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries:      []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=finance,dc=example,dc=com")},
		userPassword: "pw",
	}
	d, store := newLinkedDriver(t, fc, nil)
	g := linkedGroup(t, store, "Finance", "finance")
	u, err := d.VerifyPassword(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.NotContains(t, membership(t, store, u.ID), g.ID)
}

// A directory account says so on the Users page; one with a password here
// stays Local; one from before the label is claimed at its next sign-in.
func TestLDAPGroups_AccountSource(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries:      []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com")},
		userPassword: "pw",
	}
	d, store := newLinkedDriver(t, fc, nil)

	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, model.AuthSourceLDAP, u.AuthSource)
	fresh, err := store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AuthSourceLDAP, fresh.AuthSource)

	// Made before migration 00081: labelled Local, no password here.
	old, err := store.CreateUser(ctx, "old@example.com", "", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.Equal(t, model.AuthSourceLocal, old.AuthSource)
	fc.entries = []*goldap.Entry{person("cn=old,dc=example,dc=com", "old@example.com")}
	_, _, err = d.Login(ctx, "old@example.com", "pw")
	require.NoError(t, err)
	fresh, err = store.GetUser(ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AuthSourceLDAP, fresh.AuthSource)

	// Made here, with a password: Local whatever it signs in with.
	local, err := store.CreateUser(ctx, "local@example.com", "$2a$10$hash", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	fc.entries = []*goldap.Entry{person("cn=local,dc=example,dc=com", "local@example.com")}
	_, _, err = d.Login(ctx, "local@example.com", "pw")
	require.NoError(t, err)
	fresh, err = store.GetUser(ctx, local.ID)
	require.NoError(t, err)
	assert.Equal(t, model.AuthSourceLocal, fresh.AuthSource)
}

func TestGroupValues(t *testing.T) {
	got := groupValues([]string{"CN=Domain Users, CN=Users, DC=corp, DC=local", "cn=a\\,b,ou=x", "not a dn"})
	assert.Equal(t, []string{
		"cn=domain users,cn=users,dc=corp,dc=local", "domain users",
		"cn=a\\,b,ou=x", "a,b",
		"not a dn",
	}, got)
}

// newLinkedDriver is newDriver reading people's groups for the LDAP links —
// group_attr set, as the Identity providers page sets it (memberOf).
func newLinkedDriver(t *testing.T, fc *fakeConn, cfg map[string]any) (*Driver, db.Store) {
	t.Helper()
	with := map[string]any{"group_attr": "memberOf"}
	for k, v := range cfg {
		with[k] = v
	}
	return newDriver(t, fc, with)
}
