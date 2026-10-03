package proxyheader

// The roles header is the person's groups, recorded through the one rule every
// provider shares (auth.RecordSignInGroups) - with or without allowed_groups -
// and only when the set changed, because this driver runs on every request.
//
//   - the roles header ABSENT: the proxy says nothing about groups; what the
//     last request recorded stays;
//   - PRESENT and empty (or only commas): no groups; the linked groups are left;
//   - a new account starts with the role its groups name; an existing one does
//     not, but its groups are recorded and its linked groups follow;
//   - the admin value (admin_role) is a group like any other in that list, and
//     makes only a NEW account an administrator;
//   - several header lines are one list (RFC 9110: fields of one name join
//     with commas);
//   - on a multi-tenant install the linked groups are the account's own
//     tenant's, whichever tenant's address the request came to.
//
// RED PROOF (before 0.50's fix): without allowed_groups nothing was recorded -
// no starting role, no linked group; with it, a request with no roles header
// wiped the recorded groups; a second header line was never read.

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// withRoleLines is a request from the trusted proxy whose roles header is the
// given lines, one header line each. No lines = no roles header at all; one ""
// = the header present and empty.
func withRoleLines(host, user string, lines ...string) *http.Request {
	r := phRequest(host, user)
	for _, l := range lines {
		r.Header.Add("X-Auth-Roles", l)
	}
	return r
}

func ssoGroups(t *testing.T, store db.Store, uid int64) []string {
	t.Helper()
	gs, err := store.ListUserSSOGroups(context.Background(), uid)
	require.NoError(t, err)
	return gs
}

// memberships maps the account's groups to how it is in them.
func memberships(t *testing.T, store db.Store, uid int64) map[int64]string {
	t.Helper()
	ms, err := store.ListUserGroupMemberships(context.Background(), uid)
	require.NoError(t, err)
	out := map[int64]string{}
	for _, m := range ms {
		out[m.GroupID] = m.Source
	}
	return out
}

func linkedGroup(t *testing.T, store db.Store, name, link string, tenant *int64, role *int64) *model.Group {
	t.Helper()
	g, err := store.CreateGroup(context.Background(), &model.Group{
		Name: name, ProviderID: tenant, RoleID: role,
		Links: []model.GroupLink{{Kind: model.GroupLinkSSO, Value: link}},
	})
	require.NoError(t, err)
	return g
}

func TestGroups_RecordedWithoutAllowedGroups(t *testing.T) {
	t.Cleanup(perm.Invalidate)
	d, store := initDriverWithStore(t, nil)
	staff := linkedGroup(t, store, "Staff", "staff", nil, nil)

	u, err := d.Authenticate(withRoles("files.example.com", "ayse@example.com", "guests, staff"))
	require.NoError(t, err)
	assert.Equal(t, []string{"guests", "staff"}, ssoGroups(t, store, u.ID),
		"recorded with no allowed_groups set")
	assert.Equal(t, map[int64]string{staff.ID: model.GroupSourceSSO}, memberships(t, store, u.ID))

	_, err = d.Authenticate(withRoles("files.example.com", "ayse@example.com", "guests"))
	require.NoError(t, err)
	assert.Equal(t, []string{"guests"}, ssoGroups(t, store, u.ID))
	assert.Empty(t, memberships(t, store, u.ID), "out of the header's group, out of the filex group")
}

func TestGroups_StartingRoleWithoutAllowedGroups(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	d, store := initDriverWithStore(t, nil)
	auditors, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Auditors", Enabled: true, Permissions: perm.ReadOnly.Strings(),
		Targets: []model.PermRuleTarget{{Kind: model.PermTargetSSOGroup, Value: "audit"}},
	})
	require.NoError(t, err)

	// A new account starts with the role its header's groups name.
	u, err := d.Authenticate(withRoles("files.example.com", "ayse@example.com", "staff, audit"))
	require.NoError(t, err)
	held, err := store.GetUserCustomRole(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, auditors.ID, held, "a new account starts with the role its group names")
	assert.Equal(t, model.RoleViewer, u.Role, "and with that role's level")

	// An account that already exists: its groups are recorded, but the
	// starting role is only ever given at creation.
	bob, err := store.CreateUser(ctx, "bob@example.com", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	got, err := d.Authenticate(withRoles("files.example.com", "bob@example.com", "audit"))
	require.NoError(t, err)
	assert.Equal(t, bob.ID, got.ID)
	assert.Equal(t, []string{"audit"}, ssoGroups(t, store, bob.ID))
	held, err = store.GetUserCustomRole(ctx, bob.ID)
	require.NoError(t, err)
	assert.Zero(t, held, "an existing account is not handed a starting role")
	assert.Equal(t, model.RoleUser, got.Role)
}

// The recommendation of the release: absent = the proxy says nothing about
// groups; present and empty = no groups. The same with and without
// allowed_groups (which only judges a person with no account yet).
func TestGroups_AbsentHeaderSaysNothing_EmptyHeaderIsNoGroups(t *testing.T) {
	for name, cfg := range map[string]map[string]any{
		"no allowed_groups":   nil,
		"with allowed_groups": {"allowed_groups": "staff"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(perm.Invalidate)
			d, store := initDriverWithStore(t, cfg)
			staff := linkedGroup(t, store, "Staff", "staff", nil, nil)
			in := map[int64]string{staff.ID: model.GroupSourceSSO}
			const host, who = "files.example.com", "ayse@example.com"

			u, err := d.Authenticate(withRoleLines(host, who, "staff"))
			require.NoError(t, err)
			require.Equal(t, []string{"staff"}, ssoGroups(t, store, u.ID))
			require.Equal(t, in, memberships(t, store, u.ID))

			// No roles header at all: nothing is said, nothing moves.
			_, err = d.Authenticate(withRoleLines(host, who))
			require.NoError(t, err)
			assert.Equal(t, []string{"staff"}, ssoGroups(t, store, u.ID), "an absent header keeps the recorded groups")
			assert.Equal(t, in, memberships(t, store, u.ID), "and the linked groups")

			// The header, empty: no groups.
			_, err = d.Authenticate(withRoleLines(host, who, ""))
			require.NoError(t, err)
			assert.Empty(t, ssoGroups(t, store, u.ID), "an empty header is no groups")
			assert.Empty(t, memberships(t, store, u.ID))

			// Only separators is empty too.
			_, err = d.Authenticate(withRoleLines(host, who, "staff"))
			require.NoError(t, err)
			require.Equal(t, in, memberships(t, store, u.ID))
			_, err = d.Authenticate(withRoleLines(host, who, " , ,"))
			require.NoError(t, err)
			assert.Empty(t, ssoGroups(t, store, u.ID))
			assert.Empty(t, memberships(t, store, u.ID))
		})
	}
}

// A new account whose request carries no roles header records nothing and
// joins nothing - and is refused only when allowed_groups asks for a group
// (first_login_test.go).
func TestGroups_NewAccountWithNoHeader(t *testing.T) {
	t.Cleanup(perm.Invalidate)
	d, store := initDriverWithStore(t, nil)
	linkedGroup(t, store, "Staff", "staff", nil, nil)

	u, err := d.Authenticate(withRoleLines("files.example.com", "ayse@example.com"))
	require.NoError(t, err)
	assert.Empty(t, ssoGroups(t, store, u.ID))
	assert.Empty(t, memberships(t, store, u.ID))
	assert.Equal(t, model.RoleUser, u.Role)
}

func TestGroups_SeveralHeaderLinesAreOneList(t *testing.T) {
	t.Cleanup(perm.Invalidate)
	d, store := initDriverWithStore(t, nil)
	staff := linkedGroup(t, store, "Staff", "staff", nil, nil)

	u, err := d.Authenticate(withRoleLines("files.example.com", "boss@example.com", "guests", "staff, admin"))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"guests", "staff", "admin"}, ssoGroups(t, store, u.ID))
	assert.Equal(t, map[int64]string{staff.ID: model.GroupSourceSSO}, memberships(t, store, u.ID))
	assert.Equal(t, model.RoleAdmin, u.Role, "the admin mapping reads the same list: every line")
}

// admin_role reads the same header: it is recorded as one of the groups, it
// makes a NEW account an administrator (bound by no role, so no starting role
// and no level from a group), and it does not promote or demote an account that
// exists - that account's role is its own.
func TestGroups_AdminValueIsAGroupToo(t *testing.T) {
	ctx := context.Background()
	t.Cleanup(perm.Invalidate)
	d, store := initDriverWithStore(t, nil)
	lookOnly, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Look only", Enabled: true, Permissions: perm.ReadOnly.Strings(),
		Targets: []model.PermRuleTarget{{Kind: model.PermTargetSSOGroup, Value: "staff"}},
	})
	require.NoError(t, err)
	staff := linkedGroup(t, store, "Staff", "staff", nil, &lookOnly.ID)

	boss, err := d.Authenticate(withRoles("files.example.com", "boss@example.com", "admin, staff"))
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, boss.Role)
	assert.Equal(t, []string{"admin", "staff"}, ssoGroups(t, store, boss.ID))
	assert.Equal(t, map[int64]string{staff.ID: model.GroupSourceSSO}, memberships(t, store, boss.ID))
	held, err := store.GetUserCustomRole(ctx, boss.ID)
	require.NoError(t, err)
	assert.Zero(t, held, "an administrator gets no starting role")

	// The admin value gone from the header: still an administrator - the
	// read-only group role does not cap one either.
	again, err := d.Authenticate(withRoles("files.example.com", "boss@example.com", "staff"))
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, again.Role, "the header decides admin only at creation")
	assert.Equal(t, []string{"staff"}, ssoGroups(t, store, boss.ID))

	// An existing account the header names admin: not promoted.
	ayse, err := store.CreateUser(ctx, "ayse@example.com", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	got, err := d.Authenticate(withRoles("files.example.com", "ayse@example.com", "admin"))
	require.NoError(t, err)
	assert.Equal(t, model.RoleUser, got.Role)
	assert.Equal(t, []string{"admin"}, ssoGroups(t, store, ayse.ID))
}

// countingStore counts the writes of the recorded groups.
type countingStore struct {
	db.Store
	writes int
}

func (s *countingStore) SetUserSSOGroups(ctx context.Context, userID int64, groups []string) error {
	s.writes++
	return s.Store.SetUserSSOGroups(ctx, userID, groups)
}

// Every request is a sign-in here, so the groups are written only when the
// set differs from the one recorded (order and repeats aside).
func TestGroups_WrittenOnlyWhenTheSetChanges(t *testing.T) {
	_, raw := dbtest.NewTestDB(t)
	store := &countingStore{Store: identitystore.New(raw)}
	d := New(store)
	require.NoError(t, d.Init(context.Background(), map[string]any{"trusted_proxies": []string{"127.0.0.0/8"}}))
	const host, who = "files.example.com", "ayse@example.com"
	step := func(want int, lines ...string) {
		t.Helper()
		_, err := d.Authenticate(withRoleLines(host, who, lines...))
		require.NoError(t, err)
		assert.Equal(t, want, store.writes, "roles header %q", lines)
	}

	step(1, "a, b")   // the new account
	step(1, "a, b")   // the same set
	step(1, "b, a")   // order aside
	step(1, "b,a,a")  // repeats aside
	step(1, "a", "b") // two lines, the same set
	step(2, "a")      // changed
	step(2)           // absent: nothing said
	step(3, "")       // present and empty: changed to none
	step(3, "")       // still none
	step(3, " , ")    // still none
	step(4, "a")      // back
}

// On a multi-tenant install the groups are the account's: it joins its own
// tenant's linked groups - never another tenant's, never an install-wide
// group - whichever tenant's address the request came to.
func TestGroups_FollowTheAccountsTenant(t *testing.T) {
	t.Cleanup(perm.Invalidate)
	d, store := initDriverWithStore(t, map[string]any{"multi_tenant": true})
	acme := phSeedProvider(t, store, "acme", "files.acme.example")
	beta := phSeedProvider(t, store, "beta", "files.beta.example")
	acmeStaff := linkedGroup(t, store, "Acme staff", "staff", &acme, nil)
	linkedGroup(t, store, "Beta staff", "staff", &beta, nil)
	linkedGroup(t, store, "Everyone's staff", "staff", nil, nil)
	in := map[int64]string{acmeStaff.ID: model.GroupSourceSSO}

	u, err := d.Authenticate(withRoles("files.acme.example", "ayse@acme.example", "staff"))
	require.NoError(t, err)
	require.NotNil(t, u.ProviderID)
	require.Equal(t, acme, *u.ProviderID)
	assert.Equal(t, in, memberships(t, store, u.ID), "acme's group only")

	// The same person through beta's address: still acme's account, still
	// acme's group.
	again, err := d.Authenticate(withRoles("files.beta.example", "ayse@acme.example", "staff, beta-only"))
	require.NoError(t, err)
	assert.Equal(t, u.ID, again.ID)
	assert.ElementsMatch(t, []string{"staff", "beta-only"}, ssoGroups(t, store, u.ID))
	assert.Equal(t, in, memberships(t, store, u.ID), "never beta's group, never the install-wide one")

	_, err = d.Authenticate(withRoleLines("files.beta.example", "ayse@acme.example", ""))
	require.NoError(t, err)
	assert.Empty(t, memberships(t, store, u.ID), "leaves acme's group when the header drops it")
}
