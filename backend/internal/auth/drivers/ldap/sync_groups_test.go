package ldap

import (
	"context"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
)

func dirGroup(uuid, cn string) *goldap.Entry {
	return goldap.NewEntry("cn="+cn+",ou=groups,dc=example,dc=com", map[string][]string{"cn": {cn}, "entryUUID": {uuid}})
}

func groupNamed(t *testing.T, store db.Store, name string) *model.Group {
	t.Helper()
	gs, err := store.ListGroups(context.Background())
	require.NoError(t, err)
	for _, g := range gs {
		if g.Name == name {
			return g
		}
	}
	return nil
}

// Every directory group becomes a filex group linked to it, filled with its
// members; one a filex group already links to by hand is not made twice; a
// name the tenant has gets "(LDAP)".
func TestDirectorySyncGroups_BringsEveryGroupIn(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries: []*goldap.Entry{
			person("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=dept-legal,ou=groups,dc=example,dc=com", "cn=finance,ou=groups,dc=example,dc=com"),
		},
		groups: []*goldap.Entry{dirGroup("u-legal", "dept-legal"), dirGroup("u-fin", "finance"), dirGroup("u-ops", "Ops")},
	}
	d, store := newLinkedDriver(t, fc, nil)
	byHand := linkedGroup(t, store, "Money", "finance")
	_, err := store.CreateGroup(ctx, &model.Group{Name: "Ops"})
	require.NoError(t, err)

	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, rep.GroupsFound)
	assert.Equal(t, 2, rep.GroupsCreated)
	assert.Equal(t, 1, rep.GroupsLinkedByHand)

	legal := groupNamed(t, store, "dept-legal")
	require.NotNil(t, legal)
	assert.Equal(t, "ldap:u-legal", legal.DirectoryID)
	assert.True(t, legal.Synced())
	assert.Equal(t, []model.GroupLink{{Kind: model.GroupLinkLDAP, Value: "cn=dept-legal,ou=groups,dc=example,dc=com"}}, legal.Links)
	assert.Nil(t, groupNamed(t, store, "finance"), "finance is linked by hand already")
	assert.NotNil(t, groupNamed(t, store, "Ops (LDAP)"), "the tenant has an Ops")

	ayse, err := store.GetUserByEmail(ctx, "ayse@example.com")
	require.NoError(t, err)
	assert.Equal(t, map[int64]string{legal.ID: model.GroupSourceLDAP, byHand.ID: model.GroupSourceLDAP}, membership(t, store, ayse.ID))

	// Again: nothing new.
	rep, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Zero(t, rep.GroupsCreated)
	assert.Zero(t, rep.GroupsRenamed)
}

// Renamed on the directory (same entryUUID): renamed here, keeping its role;
// renamed here by an administrator: their name stays.
func TestDirectorySyncGroups_RenameFollowsThePermanentID(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries: []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=dept-legal,ou=groups,dc=example,dc=com")},
		groups:  []*goldap.Entry{dirGroup("u-legal", "dept-legal"), dirGroup("u-ops", "ops")},
	}
	d, store := newLinkedDriver(t, fc, nil)
	_, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	legal := groupNamed(t, store, "dept-legal")
	role, err := store.CreatePermissionRule(ctx, &model.PermissionRule{Name: "Look", Enabled: true, Permissions: []string{"files.download"}})
	require.NoError(t, err)
	legal.RoleID = &role.ID
	require.NoError(t, store.UpdateGroup(ctx, legal))
	ops := groupNamed(t, store, "ops")
	ops.Name = "Operations"
	require.NoError(t, store.UpdateGroup(ctx, ops))

	fc.groups = []*goldap.Entry{dirGroup("u-legal", "legal"), dirGroup("u-ops", "operations-team")}
	fc.entries = []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=legal,ou=groups,dc=example,dc=com")}
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.GroupsRenamed)
	assert.Zero(t, rep.GroupsCreated)

	got, err := store.GetGroup(ctx, legal.ID)
	require.NoError(t, err)
	assert.Equal(t, "legal", got.Name)
	assert.Equal(t, &role.ID, got.RoleID, "its role stays")
	assert.Equal(t, "cn=legal,ou=groups,dc=example,dc=com", got.Links[0].Value, "linked to the new DN")
	ayse, err := store.GetUserByEmail(ctx, "ayse@example.com")
	require.NoError(t, err)
	assert.Contains(t, membership(t, store, ayse.ID), legal.ID, "its members stay")

	got, err = store.GetGroup(ctx, ops.ID)
	require.NoError(t, err)
	assert.Equal(t, "Operations", got.Name, "an administrator's name is kept")
	assert.Equal(t, "operations-team", got.DirectoryName)
}

// Gone from the directory: kept, flagged removed, its LDAP members gone;
// back: restored. An empty group search changes nothing.
func TestDirectorySyncGroups_RemovedAndBack(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries: []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=ops,ou=groups,dc=example,dc=com")},
		groups:  []*goldap.Entry{dirGroup("u-ops", "ops"), dirGroup("u-fin", "finance")},
	}
	d, store := newLinkedDriver(t, fc, nil)
	_, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	ops := groupNamed(t, store, "ops")
	ayse, err := store.GetUserByEmail(ctx, "ayse@example.com")
	require.NoError(t, err)
	require.Contains(t, membership(t, store, ayse.ID), ops.ID)

	fc.groups = []*goldap.Entry{dirGroup("u-fin", "finance")}
	fc.entries = []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com")}
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.GroupsRemoved)
	got, err := store.GetGroup(ctx, ops.ID)
	require.NoError(t, err)
	assert.Equal(t, model.GroupDirectoryRemoved, got.DirectoryState)
	assert.False(t, got.Synced())
	assert.NotContains(t, membership(t, store, ayse.ID), ops.ID)

	// An empty answer is a wrong filter, not an empty directory.
	fc.groups = nil
	rep, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Zero(t, rep.GroupsRemoved)
	fin := groupNamed(t, store, "finance")
	assert.Empty(t, fin.DirectoryState, "finance is not flagged by an empty answer")

	fc.groups = []*goldap.Entry{dirGroup("u-ops", "ops"), dirGroup("u-fin", "finance")}
	rep, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.GroupsRestored)
	got, err = store.GetGroup(ctx, ops.ID)
	require.NoError(t, err)
	assert.Empty(t, got.DirectoryState)
}

// The directory decides which groups exist: one deleted here is made again.
func TestDirectorySyncGroups_DeletedHereComesBack(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com")}, groups: []*goldap.Entry{dirGroup("u-ops", "ops")}}
	d, store := newLinkedDriver(t, fc, nil)
	_, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	require.NoError(t, store.DeleteGroup(ctx, groupNamed(t, store, "ops").ID))
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.GroupsCreated)
	assert.NotNil(t, groupNamed(t, store, "ops"))
}

// sync_groups off: no group is made; sync_group_filter picks which are.
func TestDirectorySyncGroups_Settings(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{person("cn=ayse,dc=example,dc=com", "ayse@example.com")}, groups: []*goldap.Entry{dirGroup("u-ops", "ops")}}
	d, store := newLinkedDriver(t, fc, map[string]any{"sync_groups": "false"})
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Zero(t, rep.GroupsFound)
	assert.Nil(t, groupNamed(t, store, "ops"))

	d2, _ := newLinkedDriver(t, &fakeConn{}, map[string]any{"sync_group_filter": "(&(objectClass=groupOfNames)(cn=dept-*))"})
	assert.Equal(t, "(&(objectClass=groupOfNames)(cn=dept-*))", d2.syncGroupFilter())
	d3, _ := newLinkedDriver(t, &fakeConn{}, nil)
	assert.Equal(t, defaultSyncGroupFilter, d3.syncGroupFilter())
	assert.True(t, d3.importGroupsOn, "on unless switched off")
}
