package ldap

// The e-mail rule for a directory account, and the adoption of the account an
// older build opened.
//
// The address is ALWAYS `name@domain`: the entry's e-mail attribute; else the
// sign-in name when it is already an address (a UPN); else
// `<name>@<FILEX_OS_LOGIN_EMAIL_TOKEN>` (default `local`). Before, an entry with
// no e-mail attribute was keyed by the bare name unless a token was set; the
// account such a build opened as `alex` is taken over at the next sign-in —
// web or file protocol — rather than doubled. Nobody is refused by it: a local
// password is kept, an SSO-bound account is used as it is, and another
// tenant's account is left alone (the operator is told once).

import (
	"context"
	"sync"
	"testing"

	goldap "github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// uidDirectory answers `(uid=<name>)` for the names it holds and nothing else.
// Unlike fakeConn it notices WHICH name was asked — the file protocols ask with
// the account's e-mail, and that is the difference that matters here.
type uidDirectory struct {
	fakeConn
	byUID map[string]*goldap.Entry
}

func (u *uidDirectory) Search(req *goldap.SearchRequest) (*goldap.SearchResult, error) {
	u.searches = append(u.searches, req)
	for uid, e := range u.byUID {
		if req.Filter == "(uid="+goldap.EscapeFilter(uid)+")" {
			return &goldap.SearchResult{Entries: []*goldap.Entry{e}}, nil
		}
	}
	return &goldap.SearchResult{}, nil
}

func (u *uidDirectory) filters() []string {
	var out []string
	for _, s := range u.searches {
		out = append(out, s.Filter)
	}
	return out
}

// uidFixture is a directory of people with no e-mail attribute, searched by
// `(uid=%s)` — the shape of an OpenLDAP or Samba directory nobody filled `mail`
// in. Every password is "pw".
func uidFixture(t *testing.T, cfg map[string]any, uids ...string) (*Driver, db.Store, *uidDirectory) {
	t.Helper()
	dir := &uidDirectory{fakeConn: fakeConn{userPassword: "pw"}, byUID: map[string]*goldap.Entry{}}
	for _, uid := range uids {
		dir.byUID[uid] = goldap.NewEntry("uid="+uid+",ou=people,dc=example,dc=com", map[string][]string{})
	}
	base := map[string]any{"user_filter": "(uid=%s)"}
	for k, v := range cfg {
		base[k] = v
	}
	d, store := newDriver(t, &fakeConn{}, base)
	d.dial = func(context.Context) (conn, error) { return dir, nil }
	return d, store, dir
}

// oldAccount is the row an older build opened for an entry with no e-mail
// attribute: the bare login name as its e-mail, no password hash.
func oldAccount(t *testing.T, store db.Store, name string) *model.User {
	t.Helper()
	u, err := store.CreateUser(context.Background(), name, "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.Equal(t, name, u.Email)
	return u
}

func auditRows(t *testing.T, store db.Store, action string) []*model.AuditEntry {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 100)
	require.NoError(t, err)
	var out []*model.AuditEntry
	for _, r := range rows {
		if r.Action == action {
			out = append(out, r)
		}
	}
	return out
}

func userCount(t *testing.T, store db.Store) int {
	t.Helper()
	users, err := store.ListUsers(context.Background())
	require.NoError(t, err)
	return len(users)
}

// (a)–(d): where the address comes from. The username is still the directory
// name (identity's rules; EnsureUsername as before).
func TestEmailRule_TheAddressIsAlwaysNameAtDomain(t *testing.T) {
	for _, tc := range []struct {
		name         string
		cfg          map[string]any
		mail, typed  string
		want, wantUN string
	}{
		{"no e-mail attribute, a bare name", nil, "", "Alex", "alex@local", "alex"},
		{"no e-mail attribute, a UPN", nil, "", "Alex@Corp.Example", "alex@corp.example", "alex"},
		{"the e-mail attribute wins", nil, "Alex.Smith@Corp.Example", "alex", "alex.smith@corp.example", "alex.smith"},
		{"the installation's token", map[string]any{"email_token": "evim"}, "", "alex", "alex@evim", "alex"},
		{"an attribute that is no address is no e-mail", nil, "alex", "alex", "alex@local", "alex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]any{"user_filter": "(uid=%s)"}
			for k, v := range tc.cfg {
				cfg[k] = v
			}
			d, store, _ := fixture(t, groupEntry("uid=alex,dc=example,dc=com", tc.mail), cfg)
			u, _, err := d.Login(context.Background(), tc.typed, "pw")
			require.NoError(t, err)
			assert.Equal(t, tc.want, u.Email)
			assert.Equal(t, tc.wantUN, u.Username)
			_, err = store.GetUserByEmail(context.Background(), "alex")
			assert.Error(t, err, "a bare name is never an account's e-mail")
		})
	}
}

// (e): the account an older build keyed `alex` is the person's — it is adopted:
// same row, new address, one audit row, no second account.
func TestEmailRule_AdoptsTheAccountAnOlderBuildOpened(t *testing.T) {
	ctx := context.Background()
	d, store, _ := uidFixture(t, nil, "alex")
	old := oldAccount(t, store, "alex")
	require.NoError(t, store.UpdateUserRole(ctx, old.ID, model.RoleAdmin))
	require.NoError(t, store.SetUserQuota(ctx, old.ID, 12345))

	u, tok, err := d.Login(ctx, "Alex", "pw")
	require.NoError(t, err)
	require.NotEmpty(t, tok)
	assert.Equal(t, old.ID, u.ID)
	assert.Equal(t, "alex@local", u.Email)
	assert.Equal(t, old.Username, u.Username, "the username is not touched")
	assert.Equal(t, model.RoleAdmin, u.Role, "what hangs off the row stays")
	assert.EqualValues(t, 12345, u.QuotaBytes)
	assert.Equal(t, 1, userCount(t, store), "no second account")
	_, err = store.GetUserByEmail(ctx, "alex")
	assert.Error(t, err)

	rows := auditRows(t, store, auth.AuditAccountAdopted)
	require.Len(t, rows, 1)
	assert.Equal(t, "ldap", rows[0].Metadata["provider"])
	assert.Equal(t, "alex", rows[0].Metadata["old_email"])
	assert.Equal(t, "alex@local", rows[0].Metadata["new_email"])
	require.NotNil(t, rows[0].UserID)
	assert.Equal(t, old.ID, *rows[0].UserID)

	// Once: the next sign-in finds the account at its address.
	u2, _, err := d.Login(ctx, "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, old.ID, u2.ID)
	assert.Len(t, auditRows(t, store, auth.AuditAccountAdopted), 1)
}

// Decision: an adoption is an EXISTING account. The first-login rule judges no
// existing account, so auto_create off and allowed_groups without a match do
// not keep the person out, and no starting role is handed out — the account
// already had its role.
func TestEmailRule_AdoptionIsAnExistingAccountTheFirstLoginRuleDoesNotJudge(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		cfg  map[string]any
	}{
		{"auto_create off", map[string]any{"auto_create": false}},
		{"allowed_groups without a match", map[string]any{"allowed_groups": "Editors"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, store, dir := uidFixture(t, tc.cfg, "alex")
			dir.byUID["alex"] = goldap.NewEntry("uid=alex,dc=example,dc=com",
				map[string][]string{"memberOf": {"CN=Interns,DC=example,DC=com"}})
			old := oldAccount(t, store, "alex")

			u, _, err := d.Login(ctx, "alex", "pw")
			require.NoError(t, err, "the account existed; the rule is for people with none")
			assert.Equal(t, old.ID, u.ID)
			assert.Equal(t, "alex@local", u.Email)
			assert.Empty(t, refusedRows(t, store))

			// A person with no older account is still judged by the rule.
			dir.byUID["bob"] = goldap.NewEntry("uid=bob,dc=example,dc=com",
				map[string][]string{"memberOf": {"CN=Interns,DC=example,DC=com"}})
			_, _, err = d.Login(ctx, "bob", "pw")
			assert.ErrorIs(t, err, auth.ErrUnauthorized)
			assert.Len(t, refusedRows(t, store), 1)
		})
	}

	// No starting role for an adopted account.
	d, store, dir := uidFixture(t, map[string]any{"group_attr": "memberOf"}, "alex")
	dir.byUID["alex"] = goldap.NewEntry("uid=alex,dc=example,dc=com",
		map[string][]string{"memberOf": {"CN=audit,DC=example,DC=com"}})
	_, err := store.CreatePermissionRule(ctx, &model.PermissionRule{
		Name: "Auditors", Enabled: true, Permissions: perm.ReadOnly.With(perm.AdminAudit).Strings(),
		Targets: []model.PermRuleTarget{{Kind: model.PermTargetSSOGroup, Value: "audit"}},
	})
	require.NoError(t, err)
	old := oldAccount(t, store, "alex")
	u, _, err := d.Login(ctx, "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, old.ID, u.ID)
	held, err := store.GetUserCustomRole(ctx, u.ID)
	require.NoError(t, err)
	assert.Zero(t, held, "a starting role is for a NEW account")
	groups, err := store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	assert.Contains(t, groups, "audit", "groups follow the directory at every sign-in, adopted or not")
}

// (f) Decision 1: an older account with a LOCAL password is adopted too — only
// the old directory path ever opened a bare-name row — and the password is not
// touched: the local sign-in (by name or by the new address) and the directory
// one both reach the same account.
func TestEmailRule_ALocalPasswordAccountIsAdoptedAndBothWaysIn(t *testing.T) {
	ctx := context.Background()
	hash, err := authlocal.HashPassword("local-pw-123")
	require.NoError(t, err)
	d, store, _ := uidFixture(t, map[string]any{"auto_create": false}, "alex")
	old := oldAccount(t, store, "alex")
	require.NoError(t, store.UpdateUserPassword(ctx, old.ID, hash))

	u, _, err := d.Login(ctx, "alex", "pw")
	require.NoError(t, err, "adopted — an existing account, so auto_create off does not refuse it")
	assert.Equal(t, old.ID, u.ID)
	assert.Equal(t, "alex@local", u.Email)
	kept, err := store.GetUser(ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, hash, kept.PasswordHash, "the local password is untouched")
	assert.Equal(t, 1, userCount(t, store))
	rows := auditRows(t, store, auth.AuditAccountAdopted)
	require.Len(t, rows, 1)
	assert.Equal(t, true, rows[0].Metadata["had_local_password"])
	assert.Empty(t, refusedRows(t, store))

	local := authlocal.New(store)
	require.NoError(t, local.Init(ctx, nil))
	for _, ident := range []string{"alex", "alex@local"} {
		lu, _, err := local.Login(ctx, ident, "local-pw-123")
		require.NoError(t, err, "the local password still signs in as %s", ident)
		assert.Equal(t, old.ID, lu.ID)
	}
	du, _, err := d.Login(ctx, "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, old.ID, du.ID)
}

// (f) Decision 2: an older account bound to SSO (an OIDC subject) is signed in
// to AS IT IS — its e-mail stays `alex`, which is what the OIDC provider finds
// it by — on the web and on the file protocols, every time. `alex@local` never
// exists, so each sign-in finds it again through the bare name: typed as the
// address, the driver's second search (`(uid=alex)`) is what reaches it.
func TestEmailRule_AnSSOAccountIsSignedInToAsItIs(t *testing.T) {
	ctx := context.Background()
	d, store, dir := uidFixture(t, map[string]any{"auto_create": false}, "alex")
	old := oldAccount(t, store, "alex")
	require.NoError(t, store.SetUserProvider(ctx, old.ID, supertenantID(t, store), "oidc-subject"))

	for i := 0; i < 2; i++ {
		u, _, err := d.Login(ctx, "alex", "pw")
		require.NoError(t, err)
		assert.Equal(t, old.ID, u.ID)
		assert.Equal(t, "alex", u.Email)
	}
	r := protocolauth.New(store, acl.New(store), false)
	r.Directory = d
	for _, ident := range []string{"alex", "alex@local"} {
		r.Forget()
		dir.searches = nil
		p, err := r.Password(ctx, ident, "pw")
		require.NoError(t, err, ident)
		assert.Equal(t, old.ID, p.User.ID, ident)
		if ident == "alex@local" {
			assert.Equal(t, []string{"(uid=alex@local)", "(uid=alex)"}, dir.filters(),
				"the address matches nobody; the bare name reaches the account")
		}
	}

	kept, err := store.GetUser(ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, "alex", kept.Email, "never renamed")
	assert.Equal(t, "oidc-subject", kept.OIDCSubject)
	assert.Equal(t, 1, userCount(t, store))
	rows := auditRows(t, store, auth.AuditAccountAdopted)
	require.Len(t, rows, 1, "recorded once")
	assert.Equal(t, false, rows[0].Metadata["renamed"])
	assert.Equal(t, auth.AdoptedSSOAccount, rows[0].Metadata["reason"])
	assert.Empty(t, refusedRows(t, store))
}

// told records what the platform operator is told about older accounts in
// another tenant, for the length of one test.
func told(t *testing.T) func() []auth.LegacyAccountElsewhere {
	t.Helper()
	var mu sync.Mutex
	var got []auth.LegacyAccountElsewhere
	auth.SetLegacyAccountAlarm(func(_ context.Context, e auth.LegacyAccountElsewhere) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e)
	})
	t.Cleanup(func() { auth.SetLegacyAccountAlarm(nil) })
	return func() []auth.LegacyAccountElsewhere {
		mu.Lock()
		defer mu.Unlock()
		return append([]auth.LegacyAccountElsewhere(nil), got...)
	}
}

// (g) Decision 3: multi-tenant — only an account in the sign-in's own tenant is
// adopted. Another tenant's is never returned nor changed; the person gets the
// first sign-in rule in their own tenant, and the platform operator is told
// once.
func TestEmailRule_MultiTenantAdoptsOnlyInTheSignInsTenant(t *testing.T) {
	ctx := context.Background()
	mt := map[string]any{"multi_tenant": true}

	// Another tenant's `alex` stays theirs; this tenant gets its own account.
	calls := told(t)
	d, store, _ := uidFixture(t, mt, "alex")
	mine := seedProvider(t, store, "globex", "globex.example.com")
	theirs := seedProvider(t, store, "initech", "initech.example.com")
	old := oldAccount(t, store, "alex")
	require.NoError(t, store.SetUserProvider(ctx, old.ID, theirs, ""))
	u, _, err := d.Login(auth.WithLoginHost(ctx, "globex.example.com"), "alex", "pw")
	require.NoError(t, err)
	assert.NotEqual(t, old.ID, u.ID)
	require.NotNil(t, u.ProviderID)
	assert.Equal(t, mine, *u.ProviderID)
	kept, err := store.GetUser(ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, "alex", kept.Email)
	require.NotNil(t, kept.ProviderID)
	assert.Equal(t, theirs, *kept.ProviderID)
	rows := auditRows(t, store, auth.AuditAccountNotAdopted)
	require.Len(t, rows, 1)
	assert.Equal(t, auth.NotAdoptedOtherTenant, rows[0].Metadata["reason"])
	require.Len(t, calls(), 1)
	assert.Equal(t, auth.LegacyAccountElsewhere{Driver: "ldap", Account: "alex", UserID: old.ID,
		AccountTenant: "initech", LoginTenant: "globex"}, calls()[0])

	// With auto_create off the person is refused in their own tenant, as any
	// new person is, however often they try — and the operator is still told
	// only once. The other tenant's account is never touched.
	callsOff := told(t)
	dOff, storeOff, _ := uidFixture(t, map[string]any{"multi_tenant": true, "auto_create": false}, "alex")
	seedProvider(t, storeOff, "globex", "globex.example.com")
	theirsOff := seedProvider(t, storeOff, "initech", "initech.example.com")
	oldOff := oldAccount(t, storeOff, "alex")
	require.NoError(t, storeOff.SetUserProvider(ctx, oldOff.ID, theirsOff, ""))
	before, err := storeOff.GetUser(ctx, oldOff.ID)
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		_, _, err = dOff.Login(auth.WithLoginHost(ctx, "globex.example.com"), "alex", "pw")
		assert.ErrorIs(t, err, auth.ErrUnauthorized)
	}
	after, err := storeOff.GetUser(ctx, oldOff.ID)
	require.NoError(t, err)
	assert.Equal(t, before, after, "another tenant's account is never changed")
	assert.Len(t, callsOff(), 1, "told once")
	assert.Len(t, auditRows(t, storeOff, auth.AuditLegacyAccountElsewhere), 1)
	assert.Equal(t, 1, userCount(t, storeOff))

	// The same tenant's `alex` is adopted, and stays in its tenant.
	d2, store2, _ := uidFixture(t, mt, "alex")
	mine2 := seedProvider(t, store2, "globex", "globex.example.com")
	old2 := oldAccount(t, store2, "alex")
	require.NoError(t, store2.SetUserProvider(ctx, old2.ID, mine2, ""))
	u2, _, err := d2.Login(auth.WithLoginHost(ctx, "globex.example.com"), "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, old2.ID, u2.ID)
	// In its tenant's realm (#128): `alex@globex.local`, not the platform's
	// `alex@local`.
	assert.Equal(t, "alex@globex.local", u2.Email)

	// A file-protocol sign-in with no host and no pin cannot say which tenant
	// it is for: nothing is adopted, nothing is created.
	d3, store3, _ := uidFixture(t, mt, "alex")
	mine3 := seedProvider(t, store3, "globex", "globex.example.com")
	old3 := oldAccount(t, store3, "alex")
	require.NoError(t, store3.SetUserProvider(ctx, old3.ID, mine3, ""))
	_, err = d3.VerifyPassword(ctx, "alex", "pw")
	assert.ErrorIs(t, err, auth.ErrNoTenantForLogin)
	kept3, err := store3.GetUser(ctx, old3.ID)
	require.NoError(t, err)
	assert.Equal(t, "alex", kept3.Email)
	assert.Equal(t, 1, userCount(t, store3))

	// With the tenant pinned it can, and the account is adopted in place.
	d4, store4, _ := uidFixture(t, map[string]any{"multi_tenant": true, "provider": "globex"}, "alex")
	mine4 := seedProvider(t, store4, "globex", "globex.example.com")
	old4 := oldAccount(t, store4, "alex")
	require.NoError(t, store4.SetUserProvider(ctx, old4.ID, mine4, ""))
	u4, err := d4.VerifyPassword(ctx, "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, old4.ID, u4.ID)
	assert.Equal(t, "alex@globex.local", u4.Email, "the pinned tenant's realm")
}

// (h): the file protocols (protocolauth → Directory.VerifyPassword) reach the
// same account the web does — before the adoption, at it, and after it, by the
// name or by the address.
func TestEmailRule_FileProtocolsReachTheSameAccount(t *testing.T) {
	ctx := context.Background()
	d, store, dir := uidFixture(t, nil, "alex", "bob")
	old := oldAccount(t, store, "alex")
	r := protocolauth.New(store, acl.New(store), false)
	r.Directory = d

	// The first protocol sign-in after the upgrade adopts.
	p, err := r.Password(ctx, "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, old.ID, p.User.ID)
	fresh, err := store.GetUser(ctx, old.ID)
	require.NoError(t, err)
	assert.Equal(t, "alex@local", fresh.Email)
	require.Len(t, auditRows(t, store, auth.AuditAccountAdopted), 1)

	// After it the resolver asks the directory with the account's e-mail; the
	// driver asks `(uid=alex)` when `(uid=alex@local)` matches nobody.
	for _, ident := range []string{"alex", "alex@local", "ALEX@LOCAL"} {
		r.Forget()
		dir.searches = nil
		p, err = r.Password(ctx, ident, "pw")
		require.NoError(t, err, ident)
		assert.Equal(t, old.ID, p.User.ID, ident)
		assert.Equal(t, []string{"(uid=alex@local)", "(uid=alex)"}, dir.filters(), ident)
	}
	r.Forget()
	_, err = r.Password(ctx, "alex", "wrong")
	assert.ErrorIs(t, err, protocolauth.ErrUnauthorized)

	// The web form lands on the same account too.
	u, _, err := d.Login(ctx, "alex", "pw")
	require.NoError(t, err)
	assert.Equal(t, old.ID, u.ID)

	// A person with no older account: created at the first protocol sign-in,
	// found again at the next one.
	r.Forget()
	pb, err := r.Password(ctx, "bob", "pw")
	require.NoError(t, err)
	assert.Equal(t, "bob@local", pb.User.Email)
	r.Forget()
	pb2, err := r.Password(ctx, "bob", "pw")
	require.NoError(t, err)
	assert.Equal(t, pb.User.ID, pb2.User.ID)

	assert.Equal(t, 2, userCount(t, store), "alex and bob, one account each")
}

// The second search is only for an address this driver made up: a real
// mailbox that matched nobody is never taken apart into a login name.
func TestEmailRule_OnlyAMadeUpAddressIsAskedAgainAsAName(t *testing.T) {
	ctx := context.Background()
	d, _, dir := uidFixture(t, nil, "alex")
	_, err := d.VerifyPassword(ctx, "alex@corp.example", "pw")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Equal(t, []string{"(uid=alex@corp.example)"}, dir.filters())
}
