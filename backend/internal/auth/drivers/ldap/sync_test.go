package ldap

import (
	"context"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

// Directory sync without anybody signing in: every person gets an account
// labelled LDAP, their groups are recorded and the linked filex groups fill.
func TestDirectorySync_MakesAccountsAndFillsGroups(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{
		person("cn=ayse,dc=example,dc=com", "Ayse@Example.com", "cn=finance,ou=groups,dc=example,dc=com"),
		person("cn=bora,dc=example,dc=com", "bora@example.com", "cn=staff,ou=groups,dc=example,dc=com"),
		goldap.NewEntry("cn=printer,dc=example,dc=com", map[string][]string{"memberOf": {"cn=staff,ou=groups,dc=example,dc=com"}}),
	}}
	d, store := newLinkedDriver(t, fc, map[string]any{
		"bind_dn": "cn=svc,dc=example,dc=com", "bind_password": "svc",
		"user_filter": "(&(objectClass=person)(|(mail=%s)(uid=%s)))",
	})
	finance := linkedGroup(t, store, "Finance", "finance")

	var _ auth.DirectorySyncer = d
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Found)
	assert.Equal(t, 2, rep.Created)
	assert.Equal(t, 1, rep.Updated, "Ayse joined Finance")
	assert.Equal(t, 1, rep.Skipped, "an entry with no e-mail is no account")

	search := fc.searches[len(fc.searches)-1] // groups are read first, people last
	assert.Equal(t, "(&(objectClass=person)(|(mail=*)(uid=*)))", search.Filter, "user_filter with the name as *")
	assert.Equal(t, "cn=svc,dc=example,dc=com", fc.binds[0].dn, "read as the service account")

	ayse, err := store.GetUserByEmail(ctx, "ayse@example.com")
	require.NoError(t, err)
	assert.Equal(t, model.AuthSourceLDAP, ayse.AuthSource)
	assert.Equal(t, map[int64]string{finance.ID: model.GroupSourceLDAP}, membership(t, store, ayse.ID))
	groups, err := store.ListUserLDAPGroups(ctx, ayse.ID)
	require.NoError(t, err)
	assert.Contains(t, groups, "finance")

	// Again, with nothing changed: nothing to do.
	fc.searches, fc.binds = nil, nil
	rep, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Found)
	assert.Zero(t, rep.Created)
	assert.Zero(t, rep.Updated)
}

// A person the directory stops listing leaves their LDAP groups; with
// sync_disable_missing they are switched off — never an administrator, and
// never an account made here.
func TestDirectorySync_PeopleNoLongerListed(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{
		person("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=finance,dc=example,dc=com"),
		person("cn=bora,dc=example,dc=com", "bora@example.com", "cn=finance,dc=example,dc=com"),
		person("cn=boss,dc=example,dc=com", "boss@example.com"),
	}}
	d, store := newLinkedDriver(t, fc, map[string]any{"sync_disable_missing": "true"})
	finance := linkedGroup(t, store, "Finance", "finance")
	local, err := store.CreateUser(ctx, "local@example.com", "$2a$10$hash", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	_, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	boss, err := store.GetUserByEmail(ctx, "boss@example.com")
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserRole(ctx, boss.ID, model.RoleAdmin))
	bora, err := store.GetUserByEmail(ctx, "bora@example.com")
	require.NoError(t, err)
	require.NoError(t, store.AddGroupMember(ctx, finance.ID, bora.ID)) // now by hand too

	// Bora and the administrator leave the directory.
	fc.entries = fc.entries[:1]
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Missing)
	assert.Equal(t, 1, rep.Disabled, "the administrator stays on")

	got, err := store.GetUser(ctx, bora.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled)
	assert.Equal(t, map[int64]string{finance.ID: model.GroupSourceManual}, membership(t, store, bora.ID),
		"a membership added by hand stays; LDAP ones end")
	got, err = store.GetUser(ctx, boss.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
	got, err = store.GetUser(ctx, local.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled, "an account made here is not the directory's to switch off")
}

// Without sync_disable_missing nobody is switched off.
func TestDirectorySync_MissingStaysOnByDefault(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{
		person("cn=ayse,dc=example,dc=com", "ayse@example.com"),
		person("cn=bora,dc=example,dc=com", "bora@example.com"),
	}}
	d, store := newLinkedDriver(t, fc, nil)
	_, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	fc.entries = fc.entries[:1]
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Missing)
	assert.Zero(t, rep.Disabled)
	bora, err := store.GetUserByEmail(ctx, "bora@example.com")
	require.NoError(t, err)
	assert.True(t, bora.Enabled)
}

// A search that finds nobody (a wrong base or filter) changes nothing.
func TestDirectorySync_EmptyAnswerChangesNothing(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=finance,dc=example,dc=com")}}
	d, store := newLinkedDriver(t, fc, map[string]any{"sync_disable_missing": true})
	finance := linkedGroup(t, store, "Finance", "finance")
	_, err := d.SyncDirectory(ctx)
	require.NoError(t, err)

	fc.entries = nil
	rep, err := d.SyncDirectory(ctx)
	require.Error(t, err)
	assert.NotEmpty(t, rep.Error)
	ayse, err := store.GetUserByEmail(ctx, "ayse@example.com")
	require.NoError(t, err)
	assert.True(t, ayse.Enabled)
	assert.Contains(t, membership(t, store, ayse.ID), finance.ID)
}

func TestDirectorySync_Settings(t *testing.T) {
	d, _ := newLinkedDriver(t, &fakeConn{}, map[string]any{"sync_filter": "(objectClass=inetOrgPerson)", "sync_interval": "6h"})
	assert.Equal(t, "(objectClass=inetOrgPerson)", d.syncFilter())
	assert.Equal(t, "6h0m0s", d.SyncInterval().String())

	d2, _ := newLinkedDriver(t, &fakeConn{}, map[string]any{"user_filter": "(uid=%[1]s)"})
	assert.Equal(t, "(uid=*)", d2.syncFilter())
	assert.Zero(t, d2.SyncInterval(), "off unless set")

	for _, bad := range []string{"soon", "1m"} {
		err := New(newStore(t)).Init(context.Background(), map[string]any{
			"url": "ldaps://directory.invalid", "base_dn": "dc=example,dc=com", "sync_interval": bad,
		})
		assert.Error(t, err, bad)
	}
}
