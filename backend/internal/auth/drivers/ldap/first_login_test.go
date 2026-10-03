package ldap

// The first-login rule for a directory person: auto_create, allowed_groups, the
// group attribute, and the promise that an installation that sets none of it
// still lets everybody in (the address of an entry with no e-mail is the
// e-mail rule's — email_rule_test.go).

import (
	"context"
	"strings"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
)

func groupEntry(dn, mail string, groups ...string) *goldap.Entry {
	attrs := map[string][]string{"memberOf": groups}
	if mail != "" {
		attrs["mail"] = []string{mail}
	}
	return goldap.NewEntry(dn, attrs)
}

func fixture(t *testing.T, e *goldap.Entry, cfg map[string]any) (*Driver, db.Store, *fakeConn) {
	t.Helper()
	fc := &fakeConn{entries: []*goldap.Entry{e}, userPassword: "pw"}
	d, store := newDriver(t, fc, cfg)
	return d, store, fc
}

func refusedRows(t *testing.T, store db.Store) []*model.AuditEntry {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 50)
	require.NoError(t, err)
	var out []*model.AuditEntry
	for _, r := range rows {
		if r.Action == auth.AuditFirstLoginRefused {
			out = append(out, r)
		}
	}
	return out
}

// UPGRADE: an installation whose configuration says nothing about the rule
// signs people in as before — an account is opened, the search asks for the DN
// and the e-mail attribute only, no group is read or recorded.
//
// What changed is the address of an entry with no e-mail: `alex@local` (the
// e-mail rule), no longer the typed `alex`. The account an older build opened
// as `alex` is ADOPTED at that sign-in — same row, new address — rather than
// left beside a second one (email_rule_test.go holds the whole rule).
func TestFirstLogin_UpgradeAdoptsTheOlderAccount(t *testing.T) {
	ctx := context.Background()

	d, store, fc := fixture(t, groupEntry("cn=ayse,dc=example,dc=com", "ayse@example.com", "cn=Staff,dc=example,dc=com"), nil)
	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, "ayse@example.com", u.Email)
	require.Len(t, fc.searches, 1)
	assert.Equal(t, []string{"dn", "mail"}, fc.searches[0].Attributes, "no group attribute is asked for unless configured")
	groups, err := store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	assert.Empty(t, groups, "groups are not recorded unless configured")
	assert.Empty(t, refusedRows(t, store))

	// An entry with no e-mail, and the account an older build keyed by the
	// typed `alex`: that account is the person's — adopted, not doubled.
	d2, store2, _ := fixture(t, groupEntry("cn=alex,dc=example,dc=com", ""), map[string]any{"user_filter": "(uid=%s)"})
	old, err := store2.CreateUser(ctx, "alex", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	u2, _, err := d2.Login(ctx, "Alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, old.ID, u2.ID)
	assert.Equal(t, "alex@local", u2.Email)
	_, err = store2.GetUserByEmail(ctx, "alex")
	assert.Error(t, err, "the bare key is gone")
	users, err := store2.ListUsers(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 1)
	assert.Empty(t, refusedRows(t, store2))
}

func TestFirstLogin_AutoCreateOffRefusesANewPersonAndAdmitsAnExistingOne(t *testing.T) {
	ctx := context.Background()
	d, store, _ := fixture(t, groupEntry("cn=ayse,dc=example,dc=com", "ayse@example.com"), map[string]any{"auto_create": false})

	u, tok, err := d.Login(ctx, "ayse@example.com", "pw")
	require.Error(t, err)
	assert.Nil(t, u)
	assert.Empty(t, tok)
	// One answer for the person: the same error a wrong password gets.
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	_, gerr := store.GetUserByEmail(ctx, "ayse@example.com")
	assert.Error(t, gerr, "nothing may be created")

	rows := refusedRows(t, store)
	require.Len(t, rows, 1)
	assert.Equal(t, auth.ReasonAutoCreateOff, rows[0].Metadata["reason"])
	assert.Equal(t, "ldap", rows[0].Metadata["provider"])

	// An account that already exists signs in regardless.
	_, err = store.CreateUser(ctx, "ayse@example.com", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	u, _, err = d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, "ayse@example.com", u.Email)
	assert.Len(t, refusedRows(t, store), 1, "an existing account is not a refusal")
}

func TestFirstLogin_AllowedGroupsAreTheDoor(t *testing.T) {
	ctx := context.Background()
	cfg := map[string]any{"allowed_groups": "Editors, Yöneticiler"}

	// A member (the group named by its DN's leading value) gets an account.
	d, store, fc := fixture(t, groupEntry("cn=ayse,dc=example,dc=com", "ayse@example.com",
		"CN=editors,OU=Groups,DC=example,DC=com"), cfg)
	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, "ayse@example.com", u.Email)
	assert.Equal(t, []string{"dn", "mail", "memberOf"}, fc.searches[0].Attributes)
	groups, err := store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	assert.Contains(t, groups, "editors")

	// The Turkish dotted/dotless I is one letter to the door.
	d2, _, _ := fixture(t, groupEntry("cn=can,dc=example,dc=com", "can@example.com", "CN=YÖNETİCİLER,DC=example,DC=com"), cfg)
	_, _, err = d2.Login(ctx, "can@example.com", "pw")
	require.NoError(t, err)

	// Not a member: refused, ambiguous, audited, nothing created.
	d3, store3, _ := fixture(t, groupEntry("cn=bob,dc=example,dc=com", "bob@example.com", "CN=Interns,DC=example,DC=com"), cfg)
	_, _, err = d3.Login(ctx, "bob@example.com", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	_, gerr := store3.GetUserByEmail(ctx, "bob@example.com")
	assert.Error(t, gerr)
	rows := refusedRows(t, store3)
	require.Len(t, rows, 1)
	assert.Equal(t, auth.ReasonGroupNotAllowed, rows[0].Metadata["reason"])
	assert.NotContains(t, rows[0].Metadata, "groups")

	// No groups at all: also outside.
	d4, _, _ := fixture(t, groupEntry("cn=eve,dc=example,dc=com", "eve@example.com"), cfg)
	_, _, err = d4.Login(ctx, "eve@example.com", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
}

// The door only judges a NEW person: someone who already has an account keeps
// signing in when the groups change, and the recorded set follows the directory.
func TestFirstLogin_ExistingAccountIsNotJudgedAgainButGroupsFollowTheDirectory(t *testing.T) {
	ctx := context.Background()
	fc := &fakeConn{userPassword: "pw", entries: []*goldap.Entry{
		groupEntry("cn=ayse,dc=example,dc=com", "ayse@example.com", "CN=Editors,DC=example,DC=com", "CN=Audit,DC=example,DC=com"),
	}}
	d, store := newDriver(t, fc, map[string]any{"allowed_groups": "Editors"})
	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	groups, err := store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"CN=Editors,DC=example,DC=com", "Editors", "CN=Audit,DC=example,DC=com", "Audit"}, groups)

	// Removed from every group in the directory: still signs in, groups replaced.
	fc.entries = []*goldap.Entry{groupEntry("cn=ayse,dc=example,dc=com", "ayse@example.com")}
	_, _, err = d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	groups, err = store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	assert.Empty(t, groups)
}

// group → role is the permission rules' SSO-group targets — the same table OIDC
// uses — applied to a NEW account only.
func TestFirstLogin_GroupNamesTheStartingRole(t *testing.T) {
	ctx := context.Background()
	d, store, _ := fixture(t, groupEntry("cn=ayse,dc=example,dc=com", "ayse@example.com", "CN=audit,DC=example,DC=com"),
		map[string]any{"group_attr": "memberOf"})
	auditors, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Auditors", Enabled: true, Permissions: perm.ReadOnly.With(perm.AdminAudit).Strings(),
		Targets: []model.PermRuleTarget{{Kind: model.PermTargetSSOGroup, Value: "audit"}},
	})
	require.NoError(t, err)

	u, _, err := d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	held, err := store.GetUserCustomRole(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, auditors.ID, held)

	// Only at creation: a later sign-in does not hand the role back.
	require.NoError(t, store.SetUserCustomRole(ctx, u.ID, 0))
	_, _, err = d.Login(ctx, "ayse@example.com", "pw")
	require.NoError(t, err)
	held, err = store.GetUserCustomRole(ctx, u.ID)
	require.NoError(t, err)
	assert.Zero(t, held)
}

func TestFirstLogin_EmailTokenDerivesTheAddressOfAnEmaillessEntry(t *testing.T) {
	ctx := context.Background()
	cfg := map[string]any{"user_filter": "(uid=%s)"}

	cfg["email_token"] = "evim"
	d, store, _ := fixture(t, groupEntry("cn=alex,dc=example,dc=com", ""), cfg)
	u, _, err := d.Login(ctx, "Alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, "alex@evim", u.Email)
	_, err = store.GetUserByEmail(ctx, "alex")
	assert.Error(t, err)

	// An identifier that is already an address stays as typed.
	d2, _, _ := fixture(t, groupEntry("cn=alex,dc=example,dc=com", ""), cfg)
	u2, _, err := d2.Login(ctx, "alex@example.com", "pw")
	require.NoError(t, err)
	assert.Equal(t, "alex@example.com", u2.Email)

	// A token that cannot follow an @ stops the driver at Init.
	bad := New(newStore(t))
	err = bad.Init(ctx, map[string]any{"url": "ldaps://x.invalid", "base_dn": "dc=x", "email_token": "not ok!"})
	assert.Error(t, err)
}

func TestFirstLogin_ProbeSaysWhatTheConfigurationDoes(t *testing.T) {
	step := func(cfg map[string]any) auth.ProbeCheck {
		base := map[string]any{"url": "ldaps://dc.example.com", "base_dn": "dc=example,dc=com"}
		for k, v := range cfg {
			base[k] = v
		}
		for _, c := range probeWith(&fakeConn{}, nil, base) {
			if strings.HasPrefix(c.ID, "first_login") {
				return c
			}
		}
		t.Fatal("no first_login step")
		return auth.ProbeCheck{}
	}
	assert.Equal(t, "first_login_open", step(nil).ID)
	assert.Equal(t, "first_login_closed", step(map[string]any{"auto_create": "false"}).ID)
	c := step(map[string]any{"allowed_groups": "a,b"})
	assert.Equal(t, "first_login_groups", c.ID)
	assert.Equal(t, auth.ProbeOK, c.Status)
	assert.Equal(t, "memberOf", c.Params["from"], "allowed_groups reads the default group attribute")
}
