package ldap

import (
	"context"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
)

// who is a person entry with a permanent id and any further attributes.
func who(dn, mail, uuid string, attrs map[string][]string) *goldap.Entry {
	a := map[string][]string{"mail": {mail}, "entryUUID": {uuid}}
	for k, v := range attrs {
		a[k] = v
	}
	return goldap.NewEntry(dn, a)
}

// noGroups leaves directory groups out of sync, so a report holds only people.
var noGroups = map[string]any{"sync_groups": false}

// set changes one attribute of an entry.
func set(e *goldap.Entry, name, value string) {
	for _, a := range e.Attributes {
		if a.Name == name {
			a.Values, a.ByteValues = []string{value}, [][]byte{[]byte(value)}
			return
		}
	}
	panic("no attribute " + name)
}

// A person switched off in Active Directory is switched off here at the next
// sync — an administrator too — and back on when the directory lets them
// back in. Someone switched off in the directory with no account here gets
// none.
func TestDirectorySync_FollowsTheDirectorysSwitch(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{
		who("cn=ayse,dc=example,dc=com", "ayse@example.com", "u-ayse", map[string][]string{"userAccountControl": {"512"}}),
		who("cn=boss,dc=example,dc=com", "boss@example.com", "u-boss", map[string][]string{"userAccountControl": {"512"}}),
	}}
	d, store := newLinkedDriver(t, fc, noGroups)
	_, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	boss, err := store.GetUserByEmail(ctx, "boss@example.com")
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserRole(ctx, boss.ID, model.RoleAdmin))
	// Another administrator, so Boss is not the last one (see
	// TestDirectorySync_KeepsTheLastAdministratorOn).
	_, err = store.CreateUser(ctx, "root@example.com", "$2a$10$hash", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)

	// 514 = NORMAL_ACCOUNT | ACCOUNTDISABLE.
	set(fc.entries[0], "userAccountControl", "514")
	set(fc.entries[1], "userAccountControl", "514")
	fc.entries = append(fc.entries, who("cn=gone,dc=example,dc=com", "gone@example.com", "u-gone",
		map[string][]string{"userAccountControl": {"66050"}})) // disabled, never here
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, rep.Disabled)
	assert.Equal(t, 1, rep.Skipped)
	assert.Empty(t, rep.Problems)
	for _, em := range []string{"ayse@example.com", "boss@example.com"} {
		u, err := store.GetUserByEmail(ctx, em)
		require.NoError(t, err)
		assert.False(t, u.Enabled, em)
		assert.True(t, u.DisabledByDirectory(), em)
	}
	_, err = store.GetUserByEmail(ctx, "gone@example.com")
	assert.Error(t, err, "no account for a person the directory has switched off")

	// The administrator switches Boss off by hand too; then the directory
	// lets both back in. Only Ayse comes back: a choice made here stands.
	require.NoError(t, store.SetUserEnabled(ctx, boss.ID, false))
	set(fc.entries[0], "userAccountControl", "512")
	set(fc.entries[1], "userAccountControl", "512")
	rep, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Enabled)
	ayse, err := store.GetUserByEmail(ctx, "ayse@example.com")
	require.NoError(t, err)
	assert.True(t, ayse.Enabled)
	assert.False(t, ayse.DisabledByDirectory())
	got, err := store.GetUser(ctx, boss.ID)
	require.NoError(t, err)
	assert.False(t, got.Enabled, "switched off by hand: sync leaves it off")
}

// The other directories' switches: 389-ds's nsAccountLock, and an OpenLDAP
// ppolicy lock with no end (a lockout after wrong passwords ends on its own,
// so it is not one).
func TestSwitchedOff(t *testing.T) {
	for _, c := range []struct {
		attrs map[string][]string
		off   bool
	}{
		{nil, false},
		{map[string][]string{"userAccountControl": {"512"}}, false},
		{map[string][]string{"userAccountControl": {"514"}}, true},
		{map[string][]string{"nsAccountLock": {"TRUE"}}, true},
		{map[string][]string{"nsAccountLock": {"false"}}, false},
		{map[string][]string{"pwdAccountLockedTime": {"000001010000Z"}}, true},
		{map[string][]string{"pwdAccountLockedTime": {"20261002101500Z"}}, false},
	} {
		e := who("cn=x,dc=example,dc=com", "x@example.com", "u-x", c.attrs)
		assert.Equal(t, c.off, switchedOff(e) != "", "%v", c.attrs)
	}
}

// A person no longer listed (sync_disable_missing) and then listed again is
// switched back on.
func TestDirectorySync_MissingComesBack(t *testing.T) {
	ctx := context.Background()
	all := []*goldap.Entry{
		who("cn=ayse,dc=example,dc=com", "ayse@example.com", "u-ayse", nil),
		who("cn=bora,dc=example,dc=com", "bora@example.com", "u-bora", nil),
	}
	fc := &fakeConn{entries: all}
	d, store := newLinkedDriver(t, fc, map[string]any{"sync_disable_missing": true})
	_, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	fc.entries = all[:1]
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Disabled)
	fc.entries = all
	rep, err = d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Enabled)
	bora, err := store.GetUserByEmail(ctx, "bora@example.com")
	require.NoError(t, err)
	assert.True(t, bora.Enabled)
}

// A sign-in is refused for a person the directory has locked, even by a
// directory that lets the bind through; and a sign-in switches back on an
// account sync had switched off, once the directory lets the person in.
func TestSignIn_FollowsTheDirectorysSwitch(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries:      []*goldap.Entry{who("cn=ayse,dc=example,dc=com", "ayse@example.com", "u-ayse", map[string][]string{"nsAccountLock": {"true"}})},
		userPassword: "pw",
	}
	d, store := newLinkedDriver(t, fc, nil)
	_, err := d.VerifyPassword(ctx, "ayse@example.com", "pw")
	require.Error(t, err)
	_, err = store.GetUserByEmail(ctx, "ayse@example.com")
	assert.Error(t, err, "no account made for a locked person")

	fc.entries[0] = who("cn=ayse,dc=example,dc=com", "ayse@example.com", "u-ayse", nil)
	u, err := d.VerifyPassword(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	require.NoError(t, store.SetUserEnabledByDirectory(ctx, u.ID, false))
	u, err = d.VerifyPassword(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.True(t, u.Enabled)
	got, err := store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
	assert.False(t, got.DisabledByDirectory())
}

// A person whose e-mail changes in the directory keeps their account; its
// e-mail follows — at a sign-in, and at a sync.
func TestPeopleAreFoundByPermanentID(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries:      []*goldap.Entry{who("cn=ayse,dc=example,dc=com", "ayse.kaya@example.com", "U-AYSE", nil)},
		userPassword: "pw",
	}
	d, store := newLinkedDriver(t, fc, nil)
	first, err := d.VerifyPassword(ctx, "ayse.kaya@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, "ldap:u-ayse", first.DirectoryID)

	fc.entries[0] = who("cn=ayse,dc=example,dc=com", "ayse.demir@example.com", "u-ayse", nil)
	again, err := d.VerifyPassword(ctx, "ayse.demir@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID, "the same person, the same account")
	assert.Equal(t, "ayse.demir@example.com", again.Email)

	fc.entries[0] = who("cn=ayse,dc=example,dc=com", "ayse@example.com", "u-ayse", nil)
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.EmailsChanged)
	assert.Zero(t, rep.Created)
	got, err := store.GetUser(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, "ayse@example.com", got.Email)
	users, err := store.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1)
}

// An address the directory gives to somebody new does not hand them the
// previous owner's account; the administrator decides.
func TestRecycledAddressIsNotHandedOver(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries:      []*goldap.Entry{who("cn=jo,dc=example,dc=com", "jo@example.com", "u-jo-1", nil)},
		userPassword: "pw",
	}
	d, store := newLinkedDriver(t, fc, noGroups)
	old, err := d.VerifyPassword(ctx, "jo@example.com", "pw")
	require.NoError(t, err)

	fc.entries[0] = who("cn=jo2,dc=example,dc=com", "jo@example.com", "u-jo-2", nil)
	_, err = d.VerifyPassword(ctx, "jo@example.com", "pw")
	require.Error(t, err)
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, rep.Skipped)
	require.Len(t, rep.Problems, 1)
	assert.Contains(t, rep.Problems[0], "previous owner")
	got, err := store.GetUser(ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, "ldap:u-jo-1", got.DirectoryID)
}

// An account from before permanent ids takes its id at the next sign-in; an
// Active Directory objectGUID is kept in hex; a directory with neither works
// by e-mail as before.
func TestPermanentIDIsRecorded(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{entry("cn=ayse,dc=example,dc=com", "ayse@example.com")}, userPassword: "pw"}
	d, store := newLinkedDriver(t, fc, nil)
	u, err := d.VerifyPassword(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Empty(t, u.DirectoryID, "no permanent id: e-mail alone")

	guid := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}
	e := entry("cn=ayse,dc=example,dc=com", "ayse@example.com")
	e.Attributes = append(e.Attributes, &goldap.EntryAttribute{Name: "objectGUID", Values: []string{string(guid)}, ByteValues: [][]byte{guid}})
	fc.entries[0] = e
	again, err := d.VerifyPassword(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, u.ID, again.ID)
	got, err := store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "ldap:0102030405060708090a0b0c0d0e0f10", got.DirectoryID)
}

// Directory sync never switches off the last administrator still on: with
// the local administrator gone, that would leave nobody to administer filex.
func TestDirectorySync_KeepsTheLastAdministratorOn(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{
		who("cn=boss,dc=example,dc=com", "boss@example.com", "u-boss", map[string][]string{"userAccountControl": {"512"}}),
	}}
	d, store := newLinkedDriver(t, fc, noGroups)
	_, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	boss, err := store.GetUserByEmail(ctx, "boss@example.com")
	require.NoError(t, err)
	require.NoError(t, store.UpdateUserRole(ctx, boss.ID, model.RoleAdmin))
	users, err := store.ListUsers(ctx)
	require.NoError(t, err)
	for _, u := range users {
		if u.ID != boss.ID && u.IsAdmin() {
			require.NoError(t, store.DeleteUser(ctx, u.ID))
		}
	}

	set(fc.entries[0], "userAccountControl", "514")
	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Zero(t, rep.Disabled)
	require.Len(t, rep.Problems, 1)
	assert.Contains(t, rep.Problems[0], "last administrator")
	got, err := store.GetUser(ctx, boss.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled)
}
