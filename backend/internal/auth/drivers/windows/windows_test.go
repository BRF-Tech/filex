package windows

// The Windows sign-in provider, judged on any machine: the operating-system
// call is a fake that answers like LogonUserW does (a SID and token groups, or
// a Win32 code), so what is tested is everything filex decides around it — how
// a typed name becomes an account, which accounts are never let in, the
// first-login rule, what the person is told against what the operator is told,
// and that the password reaches nothing but the call.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

const (
	sidPerson = "S-1-5-21-1000-2000-3000-1105"
	sidAdmin  = "S-1-5-21-1000-2000-3000-500"
)

// fakeOS answers logonFunc like Windows: it knows accounts by "user|domain",
// each with a password, a SID, groups, and optionally a Win32 failure.
type fakeOS struct {
	mu       sync.Mutex
	accounts map[string]fakeAccount
	calls    []string
	// fail, when non-zero, is the Win32 code every call answers (a machine that
	// cannot be asked).
	fail uint32
}

type fakeAccount struct {
	password string
	sid      string
	groups   []string
	code     uint32 // a Win32 failure even with the right password (locked, ...)
}

func key(user, domain string) string { return strings.ToLower(user) + "|" + strings.ToLower(domain) }

func (f *fakeOS) logon(user, domain, password string) (*Logon, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, user+"|"+domain)
	if f.fail != 0 {
		return nil, &LogonError{Code: f.fail}
	}
	a, ok := f.accounts[key(user, domain)]
	if !ok || a.password != password {
		return nil, &LogonError{Code: errLogonFailure}
	}
	if a.code != 0 {
		return nil, &LogonError{Code: a.code}
	}
	return &Logon{SID: a.sid, Groups: a.groups}, nil
}

func (f *fakeOS) called() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newStore(t *testing.T) db.Store {
	t.Helper()
	_, raw := dbtest.NewTestDB(t)
	return identitystore.New(raw)
}

func newDriver(t *testing.T, os *fakeOS, cfg map[string]any) (*Driver, db.Store) {
	t.Helper()
	store := newStore(t)
	d := New(store)
	d.logon = os.logon
	require.NoError(t, d.Init(context.Background(), cfg))
	return d, store
}

func people() *fakeOS {
	return &fakeOS{accounts: map[string]fakeAccount{
		key("ayse", "."):           {password: "pw-ayse", sid: sidPerson, groups: []string{`MACHINE\Users`, "Users"}},
		key("ayse", "corp"):        {password: "pw-corp", sid: sidPerson, groups: []string{`CORP\Editors`, "Editors"}},
		key("ayse@corp.test", ""):  {password: "pw-upn", sid: sidPerson},
		key("administrator", "."):  {password: "pw-admin", sid: sidAdmin},
		key("bilgiislem", "."):     {password: "pw-renamed", sid: sidAdmin},
		key("guest", "."):          {password: "pw-guest", sid: "S-1-5-21-1-2-3-501"},
		key("kilitli", "."):        {password: "pw-locked", sid: sidPerson, code: errAccountLockedOut},
		key("svc", "NT SERVICE"):   {password: "x", sid: "S-1-5-80-1-2-3"},
		key("sistem", "."):         {password: "pw-sys", sid: "S-1-5-18"},
		key("altin", "corp.test"):  {password: "pw-x", sid: sidPerson},
		key("altin@corp.test", ""): {password: "pw-y", sid: sidPerson},
	}}
}

func auditRows(t *testing.T, store db.Store) []*model.AuditEntry {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 100)
	require.NoError(t, err)
	var out []*model.AuditEntry
	for _, r := range rows {
		if r.Action == auth.AuditFirstLoginRefused {
			out = append(out, r)
		}
	}
	return out
}

// ── names ───────────────────────────────────────────────────────────────

func TestParseAccount(t *testing.T) {
	cases := []struct {
		typed, domain, token string
		user, dom, email     string
		ok                   bool
	}{
		{"alex", "", "", "alex", ".", "alex@local", true},
		{"Alex", "", "", "Alex", ".", "alex@local", true},
		{`.\alex`, "", "", "alex", ".", "alex@local", true},
		{`CORP\Alex`, "", "", "Alex", "CORP", "alex@corp", true},
		{`corp\alex`, "", "", "alex", "corp", "alex@corp", true},
		{"alex@corp.example", "", "", "alex@corp.example", "", "alex@corp.example", true},
		{"Alex@Corp.Example", "", "", "Alex@Corp.Example", "", "alex@corp.example", true},
		// A one-label suffix is a NetBIOS domain: what CORP\alex became.
		{"alex@corp", "", "", "alex", "corp", "alex@corp", true},
		// The address this provider gave a machine account comes back as the
		// machine's account, not a domain called "local".
		{"alex@local", "", "", "alex", ".", "alex@local", true},
		// The installation's own token.
		{"alex", "", "evim", "alex", ".", "alex@evim", true},
		{"alex@evim", "", "evim", "alex", ".", "alex@evim", true},
		// The default domain: dotted is a UPN, one label is NetBIOS.
		{"alex", "corp.example", "", "alex@corp.example", "", "alex@corp.example", true},
		{"alex", "CORP", "", "alex", "CORP", "alex@corp", true},
		{"alex@corp.example", "corp.example", "", "alex@corp.example", "", "alex@corp.example", true},
		// Not a Windows login at all.
		{"", "", "", "", "", "", false},
		{`\alex`, "", "", "", "", "", false},
		{`CORP\`, "", "", "", "", "", false},
		{`a\b\c`, "", "", "", "", "", false},
		{"@corp", "", "", "", "", "", false},
		{"alex@", "", "", "", "", "", false},
		{"a@b@c", "", "", "", "", "", false},
		{"al\x00ex", "", "", "", "", "", false},
		{strings.Repeat("a", 300), "", "", "", "", "", false},
	}
	for _, c := range cases {
		a, ok := parseAccount(c.typed, c.domain, "", c.token)
		require.Equal(t, c.ok, ok, "%q", c.typed)
		if !ok {
			continue
		}
		assert.Equal(t, c.user, a.LogonUser, "%q user", c.typed)
		assert.Equal(t, c.dom, a.LogonDomain, "%q domain", c.typed)
		assert.Equal(t, c.email, a.Email, "%q email", c.typed)
	}
}

func TestSIDIsSystem(t *testing.T) {
	for sid, want := range map[string]bool{
		sidPerson:                      false,
		"S-1-5-21-1-2-3-1000":          false,
		"S-1-12-1-111-222-333-444":     false,
		sidAdmin:                       true, // 500
		"S-1-5-21-1-2-3-501":           true, // Guest
		"S-1-5-21-1-2-3-502":           true,
		"S-1-5-21-1-2-3-503":           true,
		"S-1-5-21-1-2-3-504":           true,
		"S-1-5-21-1-2-3-999":           true,
		"S-1-5-18":                     true, // SYSTEM
		"S-1-5-19":                     true,
		"S-1-5-20":                     true,
		"S-1-5-80-1-2-3":               true, // NT SERVICE
		"S-1-5-82-1-2-3":               true,
		"S-1-5-90-0-1":                 true,
		"S-1-1-0":                      true, // Everyone
		"":                             true,
		"garbage":                      true,
		"S-1-5-21-1-2-3-":              true,
		"S-1-5-21-1-2-3-12x":           true,
		"S-1-5-21-1-2":                 true,
		"s-1-5-21-1000-2000-3000-1105": false,
	} {
		assert.Equal(t, want, sidIsSystem(sid), "%q", sid)
	}
}

func TestJudgeWin32Codes(t *testing.T) {
	v, _ := judge(errLogonFailure)
	assert.Equal(t, verdictWrong, v)
	for code, why := range map[uint32]string{
		errAccountLockedOut: "account_locked_out", errAccountDisabled: "account_disabled",
		errAccountExpired: "account_expired", errPasswordExpired: "password_expired",
		errPasswordMustChange: "password_must_change", errAccountRestriction: "account_restriction",
		errInvalidLogonHours: "logon_hours", errInvalidWorkstation: "workstation_restriction",
		errLogonTypeNotGrant: "network_logon_not_granted",
	} {
		v, r := judge(code)
		assert.Equal(t, verdictRefused, v, code)
		assert.Equal(t, why, r)
	}
	// No domain controller, access denied, anything unexpected: cannot judge.
	for _, code := range []uint32{1311, 1355, 5, 1722, 9999} {
		v, _ := judge(code)
		assert.Equal(t, verdictUnknown, v, code)
	}
}

// ── signing in ──────────────────────────────────────────────────────────

func TestLogin_AnExistingAccountSignsInWhicheverWayTheNameIsWritten(t *testing.T) {
	ctx := context.Background()
	os := people()
	d, store := newDriver(t, os, nil)
	want, err := store.CreateUser(ctx, "ayse@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	corp, err := store.CreateUser(ctx, "ayse@corp", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	upn, err := store.CreateUser(ctx, "ayse@corp.test", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	for _, typed := range []string{"ayse", "Ayse", `.\ayse`, `.\AYSE`, "ayse@local"} {
		u, tok, err := d.Login(ctx, typed, "pw-ayse")
		require.NoError(t, err, typed)
		assert.Equal(t, want.ID, u.ID, typed)
		assert.NotEmpty(t, tok, "a usable session is minted")
	}
	u, err := d.VerifyPassword(ctx, `CORP\ayse`, "pw-corp")
	require.NoError(t, err)
	assert.Equal(t, corp.ID, u.ID)
	// The protocol path arrives with the account's own e-mail.
	u, err = d.VerifyPassword(ctx, "ayse@corp", "pw-corp")
	require.NoError(t, err)
	assert.Equal(t, corp.ID, u.ID)
	u, err = d.VerifyPassword(ctx, "ayse@corp.test", "pw-upn")
	require.NoError(t, err)
	assert.Equal(t, upn.ID, u.ID)
	u, err = d.VerifyPassword(ctx, "ayse@local", "pw-ayse")
	require.NoError(t, err)
	assert.Equal(t, want.ID, u.ID)
}

func TestLogin_WrongPasswordAndUnknownAccountAreTheSameAnswer(t *testing.T) {
	ctx := context.Background()
	d, store := newDriver(t, people(), map[string]any{"auto_create": true})
	for _, c := range [][2]string{{"ayse", "nope"}, {"nobody", "x"}, {"ayse", ""}, {"", "pw-ayse"}} {
		u, tok, err := d.Login(ctx, c[0], c[1])
		assert.Nil(t, u)
		assert.Empty(t, tok)
		assert.ErrorIs(t, err, auth.ErrUnauthorized, "%q", c)
	}
	_, gerr := store.GetUserByEmail(ctx, "ayse@local")
	assert.Error(t, gerr, "a wrong password creates nothing")
}

func TestLogin_AnEmptyPasswordNeverReachesTheOperatingSystem(t *testing.T) {
	os := people()
	d, _ := newDriver(t, os, map[string]any{"auto_create": true})
	_, _, err := d.Login(context.Background(), "ayse", "")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Zero(t, os.called(), "a null-session logon is never attempted")
}

// The first-login rule: OFF by default for this provider; an account that
// already exists signs in either way.
func TestFirstLogin_OffByDefaultForAnOperatingSystemProvider(t *testing.T) {
	ctx := context.Background()
	d, store := newDriver(t, people(), nil)

	u, _, err := d.Login(ctx, "ayse", "pw-ayse")
	assert.Nil(t, u)
	assert.ErrorIs(t, err, auth.ErrUnauthorized, "one answer for the person")
	_, gerr := store.GetUserByEmail(ctx, "ayse@local")
	assert.Error(t, gerr, "nothing may be created")
	rows := auditRows(t, store)
	require.Len(t, rows, 1)
	assert.Equal(t, auth.ReasonAutoCreateOff, rows[0].Metadata["reason"])
	assert.Equal(t, "windows", rows[0].Metadata["provider"])

	_, err = store.CreateUser(ctx, "ayse@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	u, _, err = d.Login(ctx, "ayse", "pw-ayse")
	require.NoError(t, err)
	assert.Equal(t, "ayse@local", u.Email)
	assert.Len(t, auditRows(t, store), 1, "an existing account is not a refusal")
}

func TestFirstLogin_AutoCreateOpensAnAccountAndRecordsTheTokenGroups(t *testing.T) {
	ctx := context.Background()
	d, store := newDriver(t, people(), map[string]any{"auto_create": true})
	u, tok, err := d.Login(ctx, "ayse", "pw-ayse")
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, "ayse@local", u.Email)
	assert.Equal(t, model.RoleUser, u.Role, "a new account is never an administrator")
	groups, err := store.ListUserSSOGroups(ctx, u.ID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{`MACHINE\Users`, "Users"}, groups)
}

func TestFirstLogin_AllowedGroupsAreJudgedAgainstTheTokenGroups(t *testing.T) {
	ctx := context.Background()
	cfg := map[string]any{"auto_create": true, "allowed_groups": "editors", "domain": "CORP"}
	d, store := newDriver(t, people(), cfg)

	// Member (bare group name of the token): in.
	u, _, err := d.Login(ctx, "ayse", "pw-corp")
	require.NoError(t, err)
	assert.Equal(t, "ayse@corp", u.Email, "the default domain decides the address")

	// Not a member: refused, the reason for the operator, one answer for her.
	os2 := people()
	os2.accounts[key("ayse", ".")] = fakeAccount{password: "pw-ayse", sid: sidPerson, groups: []string{"Users"}}
	d2, store2 := newDriver(t, os2, map[string]any{"auto_create": true, "allowed_groups": "editors"})
	_, _, err = d2.Login(ctx, "ayse", "pw-ayse")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	rows := auditRows(t, store2)
	require.Len(t, rows, 1)
	assert.Equal(t, auth.ReasonGroupNotAllowed, rows[0].Metadata["reason"])
	_ = store
}

func TestFirstLogin_TheEmailTokenIsTheInstallations(t *testing.T) {
	ctx := context.Background()
	d, _ := newDriver(t, people(), map[string]any{"auto_create": true, "email_token": "evim"})
	u, _, err := d.Login(ctx, "ayse", "pw-ayse")
	require.NoError(t, err)
	assert.Equal(t, "ayse@evim", u.Email)

	store := newStore(t)
	bad := New(store)
	bad.logon = people().logon
	assert.Error(t, bad.Init(ctx, map[string]any{"email_token": "not a token!"}), "a bad token stops the provider")
}

func TestFirstLogin_TheDefaultDomainReachesTheOperatingSystemAsAUPN(t *testing.T) {
	ctx := context.Background()
	os := people()
	d, _ := newDriver(t, os, map[string]any{"auto_create": true, "domain": "corp.test"})
	u, _, err := d.Login(ctx, "altin", "pw-y")
	require.NoError(t, err)
	assert.Equal(t, "altin@corp.test", u.Email)
	assert.Equal(t, []string{"altin@corp.test|"}, os.calls)
}

// ── the accounts that are never let in ──────────────────────────────────

func TestForbiddenAccounts_NeverSignInAndTheirPasswordIsNeverOffered(t *testing.T) {
	ctx := context.Background()
	os := people()
	d, store := newDriver(t, os, map[string]any{"auto_create": true})
	// Even an account that already exists (somebody made one by hand).
	_, err := store.CreateUser(ctx, "administrator@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	for _, typed := range []string{
		"Administrator", `.\Administrator`, `MACHINE\administrator`, "Guest", "DefaultAccount",
		"WDAGUtilityAccount", "krbtgt", "root", `NT AUTHORITY\SYSTEM`, `NT AUTHORITY\LOCAL SERVICE`,
		`NT SERVICE\svc`, `IIS APPPOOL\DefaultAppPool`, "WORKSTATION$", `CORP\FILESERVER$`,
	} {
		u, _, err := d.Login(ctx, typed, "pw-admin")
		assert.Nil(t, u, typed)
		assert.ErrorIs(t, err, auth.ErrUnauthorized, typed)
	}
	assert.Zero(t, os.called(), "by name, no password is ever offered to Windows")
	rows := auditRows(t, store)
	require.NotEmpty(t, rows)
	for _, r := range rows {
		assert.Equal(t, "forbidden_account", r.Metadata["reason"])
	}
}

// The built-in Administrator under another name (a localised Windows, a renamed
// account) has no forbidden NAME: the SID gives it away, after the password.
func TestForbiddenAccounts_TheSIDCatchesARenamedAdministrator(t *testing.T) {
	ctx := context.Background()
	os := people()
	d, store := newDriver(t, os, map[string]any{"auto_create": true})
	for _, c := range [][2]string{{"bilgiislem", "pw-renamed"}, {"sistem", "pw-sys"}} {
		u, _, err := d.Login(ctx, c[0], c[1])
		assert.Nil(t, u, c[0])
		assert.ErrorIs(t, err, auth.ErrUnauthorized, c[0])
		_, gerr := store.GetUserByEmail(ctx, c[0]+"@local")
		assert.Error(t, gerr, "%s must not exist", c[0])
	}
	assert.Equal(t, 2, os.called())
	require.Len(t, auditRows(t, store), 2)
}

// ── what the person hears against what the operator hears ───────────────

func TestErrors_TheOSJudgedItIsUnauthorizedAndCouldNotJudgeIsNot(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	os := people()
	d, store := newDriver(t, os, map[string]any{"auto_create": true})
	_, err := store.CreateUser(ctx, "kilitli@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)

	// Locked out: the person hears the ordinary answer; the operator's log says why.
	_, _, err = d.Login(ctx, "kilitli", "pw-locked")
	assert.ErrorIs(t, err, auth.ErrUnauthorized)
	assert.Contains(t, buf.String(), "account_locked_out")

	// The domain controller cannot be reached: NOT "wrong password".
	os.fail = 1311
	_, _, err = d.Login(ctx, "ayse", "pw-ayse")
	require.Error(t, err)
	assert.False(t, errors.Is(err, auth.ErrUnauthorized), "the chain must not read it as a wrong password")
	assert.Contains(t, buf.String(), "could not be judged")

	// In a chain, the last error is the one that is not a plain refusal.
	chain := auth.NewLoginChain(d)
	_, _, err = chain.Login(ctx, "ayse", "pw-ayse")
	require.Error(t, err)
	assert.False(t, errors.Is(err, auth.ErrUnauthorized))
}

// ⚠ The password reaches the operating-system call and nothing else: not a log
// line, not an audit row, not an error the caller can read.
func TestThePasswordNeverLeavesTheCall(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	os := people()
	d, store := newDriver(t, os, map[string]any{"auto_create": false})
	const sentinel = "S3nt1nel-Pw-2026"
	os.accounts[key("ayse", ".")] = fakeAccount{password: sentinel, sid: sidPerson}
	os.accounts[key("kilitli", ".")] = fakeAccount{password: sentinel, sid: sidPerson, code: errAccountLockedOut}
	os.accounts[key("bilgiislem", ".")] = fakeAccount{password: sentinel, sid: sidAdmin}

	var errs []string
	for _, typed := range []string{"ayse", "ayse@local", "kilitli", "bilgiislem", "Administrator", `NT AUTHORITY\SYSTEM`, "nobody"} {
		_, _, err := d.Login(ctx, typed, sentinel)
		if err != nil {
			errs = append(errs, err.Error())
		}
	}
	os.fail = 1311
	_, _, err := d.Login(ctx, "ayse", sentinel)
	require.Error(t, err)
	errs = append(errs, err.Error())

	assert.NotContains(t, buf.String(), sentinel, "log")
	assert.NotContains(t, strings.Join(errs, "\n"), sentinel, "errors")
	rows, lerr := store.ListAuditRecent(ctx, 100)
	require.NoError(t, lerr)
	raw, _ := json.Marshal(rows)
	assert.NotContains(t, string(raw), sentinel, "audit")
	require.NotEmpty(t, rows, "the refusals were audited (or this test proves nothing)")
}

// ── off Windows ─────────────────────────────────────────────────────────

func TestInit_OffWindowsTheProviderRefusesToStartWithAClearReason(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this is the behaviour of every other operating system")
	}
	d := New(newStore(t))
	err := d.Init(context.Background(), map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "only on Windows")
	// And it is a registered provider, so the admin page can list it.
	drv, gerr := auth.Get(DriverName)
	require.NoError(t, gerr)
	assert.Equal(t, "windows", drv.Name())
}

// ── the test ────────────────────────────────────────────────────────────

func probe(t *testing.T, d *Driver, cfg map[string]any, ta *auth.TestAccount) []auth.ProbeCheck {
	t.Helper()
	return d.Probe(auth.WithTestAccount(context.Background(), ta), cfg, nil)
}

func step(checks []auth.ProbeCheck, id string) *auth.ProbeCheck {
	for i := range checks {
		if checks[i].ID == id {
			return &checks[i]
		}
	}
	return nil
}

func TestProbe_OffWindowsItFailsWithUnsupportedOS(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("not on Windows")
	}
	d := &Driver{}
	checks := probe(t, d, map[string]any{}, &auth.TestAccount{Username: "ayse", Password: "pw-ayse"})
	require.Len(t, checks, 1)
	assert.Equal(t, "unsupported_os", checks[0].ID)
	assert.Equal(t, auth.ProbeFail, checks[0].Status)
	assert.False(t, auth.ProbeOKAll(checks))
}

func TestProbe_ASignInProvesItAndNamesTheAccount(t *testing.T) {
	d := &Driver{logon: people().logon}
	ta := &auth.TestAccount{Username: "ayse", Password: "pw-ayse"}
	checks := probe(t, d, map[string]any{}, ta)
	assert.True(t, auth.ProbeOKAll(checks), "%+v", checks)
	assert.Equal(t, "ayse@local", ta.Email, "the prober says which filex account the test account is")
	assert.Equal(t, auth.ProbeOK, step(checks, "logon").Status)
	assert.Equal(t, auth.ProbeOK, step(checks, "account").Status)
	assert.NotNil(t, step(checks, "first_login_closed"), "auto_create is off unless said")
	for _, c := range checks {
		for _, v := range c.Params {
			assert.NotContains(t, v, "pw-ayse", "no step carries the password")
		}
	}
	assert.True(t, d.StrictProbe())
}

func TestProbe_EachWayItCanFail(t *testing.T) {
	d := &Driver{logon: people().logon}
	cases := []struct {
		name string
		ta   *auth.TestAccount
		step string
		why  string
	}{
		{"no account given", nil, "test_account", "missing"},
		{"no password", &auth.TestAccount{Username: "ayse"}, "test_account", "no_password"},
		{"a name that is not one", &auth.TestAccount{Username: `\`, Password: "x"}, "test_account", "invalid_name"},
		{"wrong password", &auth.TestAccount{Username: "ayse", Password: "nope"}, "logon", "credentials"},
		{"locked", &auth.TestAccount{Username: "kilitli", Password: "pw-locked"}, "logon", "account_locked_out"},
		{"Administrator by name", &auth.TestAccount{Username: "Administrator", Password: "pw-admin"}, "account", "forbidden_account"},
		{"a service", &auth.TestAccount{Username: `NT SERVICE\svc`, Password: "x"}, "account", "forbidden_account"},
		{"Administrator by SID", &auth.TestAccount{Username: "bilgiislem", Password: "pw-renamed"}, "account", "forbidden_account"},
	}
	for _, c := range cases {
		checks := probe(t, d, map[string]any{}, c.ta)
		require.False(t, auth.ProbeOKAll(checks), c.name)
		s := step(checks, c.step)
		require.NotNil(t, s, c.name)
		assert.Equal(t, auth.ProbeFail, s.Status, c.name)
		assert.Equal(t, c.why, s.Params["reason"], c.name)
		if c.ta != nil {
			assert.Empty(t, c.ta.Email, "%s: a failed test names no account", c.name)
		}
	}
	// The machine cannot be asked.
	os := people()
	os.fail = 1311
	checks := probe(t, &Driver{logon: os.logon}, map[string]any{}, &auth.TestAccount{Username: "ayse", Password: "pw-ayse"})
	assert.Equal(t, "other", step(checks, "logon").Params["reason"])
	assert.Equal(t, "1311", step(checks, "logon").Params["code"])
}

func TestProbe_AllowedGroupsWithoutAnyAutoCreateIsStillSaid(t *testing.T) {
	d := &Driver{logon: people().logon}
	checks := probe(t, d, map[string]any{"auto_create": true, "allowed_groups": "Editors, Yöneticiler"},
		&auth.TestAccount{Username: "ayse", Password: "pw-ayse"})
	require.NotNil(t, step(checks, "first_login_groups"))
	assert.Equal(t, "token", step(checks, "first_login_groups").Params["from"])
	// A bad configuration is a failed test, not a panic.
	checks = probe(t, d, map[string]any{"domain": `bad\domain`}, nil)
	assert.Equal(t, "config", checks[0].ID)
	assert.Equal(t, auth.ProbeFail, checks[0].Status)
}

// ── the super administrator exception ───────────────────────────────────

func TestGrantSuperAdmin(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)

	changed, err := auth.GrantSuperAdmin(ctx, store, "Ayse@Local")
	require.NoError(t, err)
	assert.True(t, changed)
	u, err := store.GetUserByEmail(ctx, "ayse@local")
	require.NoError(t, err)
	assert.Equal(t, model.RoleAdmin, u.Role)
	assert.True(t, u.Enabled)
	assert.Empty(t, u.PasswordHash, "it signs in through the provider, never with a password of its own")

	changed, err = auth.GrantSuperAdmin(ctx, store, "ayse@local")
	require.NoError(t, err)
	assert.False(t, changed, "already an administrator: nothing to do")

	plain, err := store.CreateUser(ctx, "can@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	changed, err = auth.GrantSuperAdmin(ctx, store, "can@local")
	require.NoError(t, err)
	assert.True(t, changed)
	plain, _ = store.GetUser(ctx, plain.ID)
	assert.Equal(t, model.RoleAdmin, plain.Role)

	// A disabled account is not switched back on by a test.
	off, err := store.CreateUser(ctx, "kapali@local", "", model.RoleUser, "en", model.TimezoneUnset)
	require.NoError(t, err)
	require.NoError(t, store.SetUserEnabled(ctx, off.ID, false))
	_, err = auth.GrantSuperAdmin(ctx, store, "kapali@local")
	assert.ErrorIs(t, err, auth.ErrAccountDisabled)
	off, _ = store.GetUser(ctx, off.ID)
	assert.Equal(t, model.RoleUser, off.Role)

	_, err = auth.GrantSuperAdmin(ctx, store, "  ")
	assert.Error(t, err)
}
