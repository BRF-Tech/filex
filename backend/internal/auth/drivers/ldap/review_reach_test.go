package ldap

// GitHub PR #90 review (0.52): what a directory may write to. Nothing is
// written to an account outside the sign-in's realm - not its permanent id -
// directory sync on a multi-tenant install reaches only its own accounts
// (never another tenant's at the same address), and a tenant's own directory
// opens its groups in its tenant, not install-wide.

import (
	"context"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
)

func TestLDAPRealm_NoPermanentIDIsWrittenToAnotherTenantsAccount(t *testing.T) {
	fc := &fakeConn{
		entries:      []*goldap.Entry{who("cn=ayse,dc=example,dc=com", "ayse@example.com", "u-ayse", nil)},
		userPassword: "directory-pw",
	}
	d, store := newDriver(t, fc, map[string]any{"multi_tenant": true})
	seedProvider(t, store, "acme", "")
	seedProvider(t, store, "beta", "")
	beta := providerBySlug(t, store, "beta")
	ctx := context.Background()
	theirs, err := store.CreateUser(ctx, "ayse@example.com", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, theirs.ID, beta.ID, ""))

	_, _, err = d.Login(inRealm(t, store, "acme"), "ayse@example.com", "directory-pw")
	require.ErrorIs(t, err, auth.ErrUnauthorized)
	got, err := store.GetUser(ctx, theirs.ID)
	require.NoError(t, err)
	assert.Empty(t, got.DirectoryID, "another tenant's account takes no directory id from this realm's sign-in")
	assert.Empty(t, got.AuthDirectory)
}

func TestDirectorySync_DoesNotReachAnotherTenantsAccount(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{
		who("cn=ayse,dc=example,dc=com", "ayse@example.com", "u-ayse", map[string][]string{"userAccountControl": {"514"}}),
	}}
	d, store := newLinkedDriver(t, fc, map[string]any{"multi_tenant": true, "sync_groups": false})
	seedProvider(t, store, "beta", "")
	beta := providerBySlug(t, store, "beta")
	theirs, err := store.CreateUser(ctx, "ayse@example.com", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, theirs.ID, beta.ID, ""))

	_, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	got, err := store.GetUser(ctx, theirs.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled, "the platform directory's sync does not switch off another tenant's account")
	assert.Empty(t, got.DirectoryID)
	assert.Empty(t, got.AuthDirectory)
	assert.Equal(t, model.AuthSourceLocal, got.AuthSource)
}

func TestDirectorySync_ATenantsDirectoryOpensItsGroupsInItsTenant(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries: []*goldap.Entry{who("cn=ada,dc=acme", "ada@acme.example", "u-ada", nil)},
		groups:  []*goldap.Entry{dirGroup("u-staff", "staff")},
	}
	store := newStore(t)
	acmeID := seedProvider(t, store, "acme", "acme.example.com")
	d := New(store)
	require.NoError(t, d.Init(ctx, map[string]any{
		"url": "ldaps://directory.invalid", "base_dn": "dc=acme",
		"multi_tenant": true, "directory": "ldap-acme", OwnerTenantKey: acmeID,
	}))
	d.dial = func(context.Context) (conn, error) { return fc, nil }

	_, _ = d.SyncDirectory(ctx)
	staff := groupNamed(t, store, "staff")
	require.NotNil(t, staff, "the directory's group came in")
	require.NotNil(t, staff.ProviderID, "a tenant's directory does not open install-wide groups")
	assert.Equal(t, acmeID, *staff.ProviderID)
}

// A tenant's own directory cannot make a platform administrator: its people
// are homed in its tenant, an install-wide group that gives Administrator
// is matched only by the platform's own people, and the group's directory is
// the platform's - not the tenant's - even when the tenant's directory lists
// the very same DN.
func TestDirectoryAdmin_ATenantsDirectoryMakesNoPlatformAdministrator(t *testing.T) {
	ctx := context.Background()
	const dn = "cn=it-admins,ou=groups,dc=corp,dc=example"
	fc := &fakeConn{
		entries:      []*goldap.Entry{person("cn=mallory,dc=acme", "mallory@acme.example", dn)},
		userPassword: "pw",
	}
	store := newStore(t)
	acmeID := seedProvider(t, store, "acme", "")
	d := New(store)
	require.NoError(t, d.Init(ctx, map[string]any{
		"url": "ldaps://directory.invalid", "base_dn": "dc=acme", "group_attr": "memberOf",
		"multi_tenant": true, "directory": "ldap-acme", OwnerTenantKey: acmeID,
	}))
	d.dial = func(context.Context) (conn, error) { return fc, nil }
	admins, err := store.CreateGroup(ctx, &model.Group{Name: "Platform admins", GivesAdmin: true,
		Links: []model.GroupLink{{Kind: model.GroupLinkLDAP, Value: dn}}})
	require.NoError(t, err)
	require.Nil(t, admins.ProviderID, "install-wide: the platform's")

	u, _, err := d.Login(inRealm(t, store, "acme"), "mallory@acme.example", "pw")
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, acmeID, *u.ProviderID, "the tenant's directory homes its people in its tenant")
	assert.NotEqual(t, model.RoleAdmin, u.Role)
	assert.NotContains(t, membership(t, store, u.ID), admins.ID)

	_, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	got, err := store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	assert.NotEqual(t, model.RoleAdmin, got.Role, "nor does its sync")
	assert.False(t, got.AdminByGroup)
}
