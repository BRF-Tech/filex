package ldap

// GitHub PR #90 review (0.52): when directory sync may switch anybody off.
// Only the accounts the directory holds - a local account with its own
// password stays on; never on a partial answer - a person whose account
// could not be looked up, or whose entry lost its e-mail, is not "no longer
// listed"; and group_filter with %u (the sign-in name, which sync does not
// know) leaves memberships as they are instead of emptying them every run.

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

func TestDirectorySync_LeavesALocalPasswordAccountOn(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{
		who("cn=boss,dc=example,dc=com", "boss@example.com", "u-boss", map[string][]string{"userAccountControl": {"514"}}),
		who("cn=ayse,dc=example,dc=com", "ayse@example.com", "u-ayse", map[string][]string{"userAccountControl": {"512"}}),
	}}
	d, store := newLinkedDriver(t, fc, noGroups)
	local, err := store.CreateUser(ctx, "boss@example.com", "$2a$10$hash", model.RoleUser, "en", "UTC")
	require.NoError(t, err)

	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	got, err := store.GetUser(ctx, local.ID)
	require.NoError(t, err)
	assert.True(t, got.Enabled, "a password of its own here: the directory's switch is not the administrator's")
	assert.Zero(t, rep.Disabled)
	require.NotEmpty(t, rep.Problems)
	assert.Contains(t, strings.Join(rep.Problems, "\n"), "left on")
}

// flakyStore answers "could not look it up" for some people.
type flakyStore struct {
	db.Store
	failEmail string
	failID    string
}

func (s *flakyStore) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	if s.failEmail != "" && email == s.failEmail {
		return nil, errors.New("database is locked")
	}
	return s.Store.GetUserByEmail(ctx, email)
}

func (s *flakyStore) GetUserByDirectoryID(ctx context.Context, id string) (*model.User, error) {
	if s.failID != "" && id == s.failID {
		return nil, errors.New("database is locked")
	}
	return s.Store.GetUserByDirectoryID(ctx, id)
}

func TestDirectorySync_APartialAnswerSwitchesNobodyOff(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{entries: []*goldap.Entry{
		who("cn=ada,dc=example,dc=com", "ada@example.com", "u-ada", nil),
		who("cn=bob,dc=example,dc=com", "bob@example.com", "u-bob", nil),
	}}
	d, raw := newLinkedDriver(t, fc, map[string]any{"sync_groups": false, "sync_disable_missing": true})
	flaky := &flakyStore{Store: raw}
	d.store = flaky
	_, err := d.SyncDirectory(ctx)
	require.NoError(t, err)

	t.Run("an account that could not be looked up", func(t *testing.T) {
		flaky.failEmail, flaky.failID = "bob@example.com", "ldap:u-bob"
		defer func() { flaky.failEmail, flaky.failID = "", "" }()
		rep, err := d.SyncDirectory(ctx)
		require.NoError(t, err)
		bob, err := raw.GetUserByEmail(ctx, "bob@example.com")
		require.NoError(t, err)
		assert.True(t, bob.Enabled, "listed by the directory: a store hiccup is not \"no longer listed\"")
		assert.Zero(t, rep.Disabled)
		assert.Zero(t, rep.Missing)
	})

	t.Run("an entry that lost its e-mail", func(t *testing.T) {
		fc.entries[0] = goldap.NewEntry("cn=ada,dc=example,dc=com", map[string][]string{"entryUUID": {"u-ada"}})
		rep, err := d.SyncDirectory(ctx)
		require.NoError(t, err)
		ada, err := raw.GetUserByEmail(ctx, "ada@example.com")
		require.NoError(t, err)
		assert.True(t, ada.Enabled, "still listed, by the permanent id")
		assert.Zero(t, rep.Disabled)
	})
}

// memberUIDConn answers a group search only for the sign-in name it names.
type memberUIDConn struct {
	*fakeConn
	uid    string
	groups []*goldap.Entry
}

func (m *memberUIDConn) Search(req *goldap.SearchRequest) (*goldap.SearchResult, error) {
	if strings.Contains(req.Filter, "memberUid=") {
		if strings.Contains(req.Filter, "memberUid="+m.uid+")") {
			return &goldap.SearchResult{Entries: m.groups}, nil
		}
		return &goldap.SearchResult{}, nil
	}
	return m.fakeConn.Search(req)
}

func (m *memberUIDConn) SearchWithPaging(req *goldap.SearchRequest, _ uint32) (*goldap.SearchResult, error) {
	return m.Search(req)
}

func TestDirectorySync_GroupFilterBySignInNameLeavesMembershipsAlone(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{
		entries:      []*goldap.Entry{who("uid=alex,ou=people,dc=example,dc=com", "alex@example.com", "u-alex", nil)},
		userPassword: "pw",
	}
	mc := &memberUIDConn{fakeConn: fc, uid: "alex", groups: []*goldap.Entry{goldap.NewEntry("cn=staff,ou=groups,dc=example,dc=com", nil)}}
	d, store := newDriver(t, fc, map[string]any{
		"user_filter": "(uid=%s)", "group_filter": "(&(objectClass=posixGroup)(memberUid=%u))", "sync_groups": false,
	})
	d.dial = func(context.Context) (conn, error) { return mc, nil }
	staff := linkedGroup(t, store, "Staff", "staff")

	u, _, err := d.Login(ctx, "alex", "pw")
	require.NoError(t, err)
	require.Contains(t, membership(t, store, u.ID), staff.ID, "the sign-in joins the group its name is in")

	rep, err := d.SyncDirectory(ctx)
	require.NoError(t, err)
	assert.Contains(t, membership(t, store, u.ID), staff.ID, "sync does not know the sign-in name: it leaves the membership")
	assert.Zero(t, rep.Updated)
}
