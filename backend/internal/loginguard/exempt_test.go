package loginguard_test

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// A public demo publishes ONE shared account. With the per-ACCOUNT limit on
// it, five wrong passwords from anybody lock it for every visitor; the
// per-ADDRESS limit is what still stops one address guessing. So a guard can
// be told which account counters the per-account limit never applies to
// (ExemptAccounts) - on a demo, the demo account's (DemoAccount).

func exempt(subjects ...string) loginguard.ExemptFunc {
	return func(context.Context) ([]string, error) { return subjects, nil }
}

func inRealm(realm, id, ip string) loginguard.Attempt {
	a := web(id, ip)
	a.Realm = realm
	return a
}

func TestExemptAccountIsNeverLockedButItsAddressIs(t *testing.T) {
	g, store, _ := newGuard(t)
	ctx := context.Background()
	g.ExemptAccounts(exempt("demo@demo.com", "demo"))

	// Nine wrong passwords for the shared account from one address: the
	// account is not counted, the address is - and the reply counts down the
	// ADDRESS's tries, never promising an account lock that will not come.
	for i := 1; i <= 9; i++ {
		o := fail(g, web("demo@demo.com", "203.0.113.1"))
		require.False(t, o.Locked, "attempt %d", i)
		require.Equal(t, model.LoginThrottleIP, o.Scope, "attempt %d speaks about the address", i)
		require.Equal(t, 10-i, o.Remaining)
		require.Equal(t, 10, o.Limit)
		require.Equal(t,
			srvtext.Plural("en", "server.login.failed_remaining_ip", 10-i, srvtext.Vars{"limit": "10"}),
			o.Message("en"))
	}
	require.False(t, g.Check(ctx, web("demo@demo.com", "198.51.100.7")).Blocked,
		"another visitor signs in to the shared account")

	// The username is the same account.
	for i := 0; i < 6; i++ {
		fail(g, web("demo", "198.51.100.8"))
	}
	require.False(t, g.Check(ctx, web("Demo", "198.51.100.9")).Blocked)

	// The address that kept guessing is stopped all the same.
	o := fail(g, web("demo@demo.com", "203.0.113.1"))
	require.True(t, o.Locked)
	require.Equal(t, model.LoginThrottleIP, o.Scope)
	v := g.Check(ctx, web("demo@demo.com", "203.0.113.1"))
	require.True(t, v.Blocked)
	require.Equal(t, model.LoginThrottleIP, v.Scope)

	// Nobody else is exempt: an ordinary account locks at its limit.
	for i := 0; i < 5; i++ {
		fail(g, web("ada@example.com", "192.0.2.1"))
	}
	v = g.Check(ctx, web("ada@example.com", "192.0.2.2"))
	require.True(t, v.Blocked)
	require.Equal(t, model.LoginThrottleAccount, v.Scope)

	// No account counter exists for the shared account at all.
	for _, row := range g.List(ctx, model.LoginThrottleAccount, nil, 0) {
		require.NotContains(t, []string{"demo@demo.com", "demo"}, row.Subject)
	}
	// Its wrong attempts are still in the trail, marked.
	var marked int
	for _, r := range audits(t, store, loginguard.ActionFailed) {
		if r.Metadata["identifier"] == "demo@demo.com" || r.Metadata["identifier"] == "demo" {
			require.Equal(t, true, r.Metadata["account_exempt"], "%v", r.Metadata)
			marked++
		}
	}
	require.Equal(t, 16, marked)
}

// A realm is part of the account counter (`acme/demo` is acme's demo, `beta/demo`
// and `demo` are other people): only the exempt account's own counter is.
func TestExemptionIsTheRealmsOwnCounter(t *testing.T) {
	g, _, _ := newGuard(t)
	ctx := context.Background()
	g.ExemptAccounts(exempt(loginguard.SubjectIn("acme", "demo")))

	for i := 0; i < 7; i++ {
		fail(g, inRealm("acme", "demo", "203.0.113."+string(rune('1'+i))))
	}
	require.False(t, g.Check(ctx, inRealm("acme", "demo", "198.51.100.1")).Blocked, "acme's demo is exempt")
	require.False(t, g.Check(ctx, web("acme/demo", "198.51.100.1")).Blocked, "typed as realm/name over a protocol too")

	for i := 0; i < 5; i++ {
		fail(g, inRealm("beta", "demo", "192.0.2.1"))
		fail(g, web("demo", "192.0.2.2"))
	}
	require.True(t, g.Check(ctx, inRealm("beta", "demo", "198.51.100.1")).Blocked, "beta's demo is somebody else")
	require.True(t, g.Check(ctx, web("demo", "198.51.100.1")).Blocked, "so is the platform's")
}

// A lock the shared account earned before it was exempt (a restored copy, a
// demo switched on later) is not in force, and is not listed as if it were.
func TestExemptionLiftsAStaleLock(t *testing.T) {
	g, store, _ := newGuard(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		fail(g, web("demo@demo.com", "203.0.113.1"))
	}
	require.True(t, g.Check(ctx, web("demo@demo.com", "198.51.100.1")).Blocked)

	g.ExemptAccounts(exempt("demo@demo.com"))
	require.False(t, g.Check(ctx, web("demo@demo.com", "198.51.100.1")).Blocked)
	for _, row := range g.List(ctx, "", nil, 0) {
		require.False(t, row.Scope == model.LoginThrottleAccount && row.Subject == "demo@demo.com", "listed: %+v", row)
	}
	rows, err := store.ListLoginThrottles(ctx, model.LoginThrottleAccount, nil, 100)
	require.NoError(t, err)
	for _, r := range rows {
		require.NotEqual(t, "demo@demo.com", r.Subject, "the table forgets it too, so a restart does not bring it back")
	}
}

// The account is looked up again every tick (a demo account seeded after the
// start is exempt within a minute); a lookup the database refuses keeps the
// last answer.
func TestExemptionIsReadAgainAtEveryTick(t *testing.T) {
	g, _, _ := newGuard(t)
	ctx := context.Background()
	answer, fails := []string{"first@demo.com"}, false
	g.ExemptAccounts(func(context.Context) ([]string, error) {
		if fails {
			return nil, errors.New("database is gone")
		}
		return answer, nil
	})
	for i := 0; i < 6; i++ {
		fail(g, web("first@demo.com", "203.0.113.1"))
	}
	require.False(t, g.Check(ctx, web("first@demo.com", "198.51.100.1")).Blocked)

	answer = []string{"second@demo.com"}
	g.Tick(ctx)
	for i := 0; i < 6; i++ {
		fail(g, web("second@demo.com", "203.0.113.2"))
	}
	require.False(t, g.Check(ctx, web("second@demo.com", "198.51.100.1")).Blocked, "the new answer is in force")
	for i := 0; i < 5; i++ {
		fail(g, web("first@demo.com", "203.0.113.3"))
	}
	require.True(t, g.Check(ctx, web("first@demo.com", "198.51.100.1")).Blocked, "the old one is not")

	fails = true
	g.Tick(ctx)
	for i := 0; i < 6; i++ {
		fail(g, web("second@demo.com", "203.0.113.4"))
	}
	require.False(t, g.Check(ctx, web("second@demo.com", "198.51.100.1")).Blocked, "a failed lookup keeps the last answer")
}

// DemoAccount names the account FILEX_DEMO_USER names - by its e-mail and by
// its username, in its own realm - never a name written into the code.
func TestDemoAccountNamesTheAccountByEveryName(t *testing.T) {
	_, raw := testutil.NewTestDB(t)
	store := identitystore.New(raw)
	ctx := context.Background()
	u, err := store.CreateUser(ctx, "demo@demo.com", "x", model.RoleAdmin, "en", "UTC")
	require.NoError(t, err)
	u, err = store.GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.NotEmpty(t, u.Username, "the identity wrapper gives every account a username")

	sorted := func(s []string) []string { sort.Strings(s); return s }
	want := sorted([]string{"demo@demo.com", u.Username})
	for _, configured := range []string{"demo@demo.com", " DEMO@demo.com ", u.Username} {
		got, err := loginguard.DemoAccount(store, configured, "")(ctx)
		require.NoError(t, err)
		require.Equal(t, want, sorted(dedupe(got)), "configured as %q", configured)
	}

	// An account in a tenant is exempt in that tenant's realm only.
	acme, err := store.CreateProvider(ctx, &model.Provider{Slug: "acme", Name: "Acme", AuthType: "local", Enabled: true})
	require.NoError(t, err)
	acme, err = store.GetProvider(ctx, acme.ID)
	require.NoError(t, err)
	require.NotEmpty(t, acme.Realm)
	t2, err := store.CreateUser(ctx, "show@acme.example", "x", model.RoleUser, "en", "UTC")
	require.NoError(t, err)
	require.NoError(t, store.SetUserProvider(ctx, t2.ID, acme.ID, ""))
	t2, err = store.GetUser(ctx, t2.ID)
	require.NoError(t, err)
	got, err := loginguard.DemoAccount(store, "show@acme.example", "")(ctx)
	require.NoError(t, err)
	require.Equal(t, sorted([]string{
		loginguard.SubjectIn(acme.Realm, "show@acme.example"),
		loginguard.SubjectIn(acme.Realm, t2.Username),
	}), sorted(dedupe(got)))

	// No such account yet: the name as configured, so the first sign-ins are
	// not lockable either; the account is looked up again at the next tick.
	got, err = loginguard.DemoAccount(store, "later@demo.com", "")(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"later@demo.com"}, dedupe(got))
	got, err = loginguard.DemoAccount(store, "  ", "")(ctx)
	require.NoError(t, err)
	require.Empty(t, got, "no demo user configured, nobody is exempt")
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
