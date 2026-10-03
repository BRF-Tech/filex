package loginguard_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// clock is a movable "now" for the guard.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newGuard(t *testing.T) (*loginguard.Guard, db.Store, *clock) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	c := &clock{t: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	g := loginguard.New(store)
	g.Now = c.now
	return g, store, c
}

func set(t *testing.T, store db.Store, g *loginguard.Guard, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		require.NoError(t, store.UpsertSetting(context.Background(), k, v))
	}
	g.Invalidate()
}

func web(id, ip string) loginguard.Attempt {
	return loginguard.Attempt{Identifier: id, IP: ip, Protocol: loginguard.ProtoWeb}
}

func fail(g *loginguard.Guard, a loginguard.Attempt) loginguard.Outcome {
	return g.Failed(context.Background(), a, loginguard.ReasonCredentials)
}

func audits(t *testing.T, store db.Store, prefix string) []*model.AuditEntry {
	t.Helper()
	rows, err := store.ListAuditRecent(context.Background(), 500)
	require.NoError(t, err)
	var out []*model.AuditEntry
	for _, r := range rows {
		if strings.HasPrefix(r.Action, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func TestAccountLocksAtTheLimitAndSaysSoOnTheWay(t *testing.T) {
	g, _, _ := newGuard(t)
	ctx := context.Background()
	a := web("ada@example.com", "203.0.113.1")

	for i := 1; i <= 4; i++ {
		require.False(t, g.Check(ctx, a).Blocked, "attempt %d is judged", i)
		o := fail(g, a)
		require.False(t, o.Locked)
		require.Equal(t, 5-i, o.Remaining, "the reply counts the tries down")
		require.Equal(t, 5, o.Limit)
		require.Equal(t, model.LoginThrottleAccount, o.Scope)
	}
	o := fail(g, a)
	require.True(t, o.Locked, "the fifth wrong attempt locks the account")
	require.Equal(t, model.LoginThrottleAccount, o.Scope)
	require.Equal(t, time.Minute, o.RetryAfter, "the first lock lasts the base length")

	v := g.Check(ctx, a)
	require.True(t, v.Blocked)
	require.Equal(t, model.LoginThrottleAccount, v.Scope)
	require.Equal(t, time.Minute, v.RetryAfter)
}

func TestAnAccountThatDoesNotExistIsCountedAndAnsweredIdentically(t *testing.T) {
	g, _, _ := newGuard(t)
	// No users row for either name: the counter is keyed by the text.
	real := web("ada@example.com", "203.0.113.1")
	ghost := web("nobody-by-this-name@example.com", "203.0.113.2")
	for i := 0; i < 4; i++ {
		a, b := fail(g, real), fail(g, ghost)
		require.Equal(t, a, b, "attempt %d: the same numbers for a real and a made-up name", i+1)
		require.Equal(t, a.Message("en"), b.Message("en"))
	}
	require.Equal(t, fail(g, real), fail(g, ghost), "and the lock falls at the same attempt")
	require.True(t, g.Check(context.Background(), real).Blocked)
	require.True(t, g.Check(context.Background(), ghost).Blocked)
}

func TestIdentifierIsNormalisedBeforeCounting(t *testing.T) {
	g, _, _ := newGuard(t)
	for _, id := range []string{"Ada@Example.com", " ada@example.com", "ADA@EXAMPLE.COM", "ada@example.com", "aDa@example.com "} {
		fail(g, web(id, "203.0.113.1"))
	}
	require.True(t, g.Check(context.Background(), web("ada@example.com", "203.0.113.50")).Blocked,
		"five spellings of one name are one counter")
}

func TestAddressLimitCountsAcrossIdentifiers(t *testing.T) {
	g, _, _ := newGuard(t)
	ctx := context.Background()
	var last loginguard.Outcome
	for i := 0; i < 9; i++ {
		last = fail(g, web("user"+string(rune('a'+i))+"@example.com", "198.51.100.7"))
		require.False(t, last.Locked, "nine different names, none locked yet")
	}
	require.Equal(t, model.LoginThrottleIP, last.Scope, "the address is the counter with fewer tries left")
	require.Equal(t, 1, last.Remaining)
	require.Equal(t, 10, last.Limit)
	o := fail(g, web("userz@example.com", "198.51.100.7"))
	require.True(t, o.Locked)
	require.Equal(t, model.LoginThrottleIP, o.Scope)

	// Every name from that address is refused, even one never tried.
	v := g.Check(ctx, web("innocent@example.com", "198.51.100.7"))
	require.True(t, v.Blocked)
	require.Equal(t, model.LoginThrottleIP, v.Scope)
	// Another address is unaffected.
	require.False(t, g.Check(ctx, web("innocent@example.com", "198.51.100.8")).Blocked)
}

func TestLockRefusesTheRightPasswordAndDoesNotCountTheRefusal(t *testing.T) {
	g, store, c := newGuard(t)
	ctx := context.Background()
	a := web("ada@example.com", "203.0.113.1")
	for i := 0; i < 5; i++ {
		fail(g, a)
	}
	// The caller asks Check first; a blocked attempt never reaches Failed.
	for i := 0; i < 20; i++ {
		require.True(t, g.Check(ctx, a).Blocked)
	}
	row, err := store.GetLoginThrottle(ctx, model.LoginThrottleAccount, "ada@example.com")
	require.NoError(t, err)
	require.Equal(t, 0, row.Fails, "refused attempts are not counted")
	require.Equal(t, 1, row.LockLevel)

	c.advance(61 * time.Second)
	require.False(t, g.Check(ctx, a).Blocked, "the lock ends by itself")
}

func TestLockEscalatesByDoublingUpToTheCeiling(t *testing.T) {
	g, _, c := newGuard(t)
	ctx := context.Background()
	a := web("ada@example.com", "203.0.113.1")
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for lvl, w := range want {
		var o loginguard.Outcome
		for i := 0; i < 5; i++ {
			require.False(t, g.Check(ctx, a).Blocked)
			o = fail(g, a)
		}
		require.True(t, o.Locked)
		require.Equal(t, w, o.RetryAfter, "lock %d", lvl+1)
		c.advance(w + time.Second)
	}
}

func TestEscalationForgetsAfterAQuietSpellAndAfterASuccess(t *testing.T) {
	g, _, c := newGuard(t)
	ctx := context.Background()
	a := web("ada@example.com", "203.0.113.1")
	lockOnce := func() loginguard.Outcome {
		var o loginguard.Outcome
		for i := 0; i < 5; i++ {
			o = fail(g, a)
		}
		return o
	}
	require.Equal(t, time.Minute, lockOnce().RetryAfter)
	c.advance(2 * time.Minute)
	require.Equal(t, 2*time.Minute, lockOnce().RetryAfter, "a second lock in a row doubles")

	// Quiet for longer than ceiling + window: the doubling starts over.
	c.advance(30 * time.Minute)
	require.Equal(t, time.Minute, lockOnce().RetryAfter)

	// A success starts it over too.
	c.advance(2 * time.Minute)
	g.Succeeded(ctx, a)
	require.Equal(t, time.Minute, lockOnce().RetryAfter)
}

func TestWindowExpiryResetsTheCount(t *testing.T) {
	g, _, c := newGuard(t)
	a := web("ada@example.com", "203.0.113.1")
	for i := 0; i < 4; i++ {
		fail(g, a)
	}
	c.advance(11 * time.Minute)
	o := fail(g, a)
	require.False(t, o.Locked, "the four old failures fell out of the ten-minute window")
	require.Equal(t, 4, o.Remaining)
}

func TestSuccessResetsTheAccountButNotTheAddress(t *testing.T) {
	g, store, _ := newGuard(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		fail(g, web("ada@example.com", "203.0.113.1"))
	}
	g.Succeeded(ctx, web("ada@example.com", "203.0.113.1"))
	acct, err := store.GetLoginThrottle(ctx, model.LoginThrottleAccount, "ada@example.com")
	require.NoError(t, err)
	require.Nil(t, acct, "the account's counter is gone")
	ip, err := store.GetLoginThrottle(ctx, model.LoginThrottleIP, "203.0.113.1")
	require.NoError(t, err)
	require.NotNil(t, ip)
	require.Equal(t, 4, ip.Fails, "the address keeps its count: a login between guesses must not launder a spray")
}

func TestAllowlistedAddressIsExemptFromTheAddressLimit(t *testing.T) {
	g, store, _ := newGuard(t)
	ctx := context.Background()
	set(t, store, g, map[string]string{loginguard.KeyIPAllowlist: "203.0.113.0/24"})
	for i := 0; i < 30; i++ {
		o := fail(g, web("user"+string(rune('a'+i%20))+"@example.com", "203.0.113.9"))
		require.True(t, o.Unlimited)
		require.False(t, g.Check(ctx, web("x@example.com", "203.0.113.9")).Blocked)
	}
	rows, err := store.ListLoginThrottles(ctx, "", nil, 0)
	require.NoError(t, err)
	require.Empty(t, rows, "an allow-listed address's mistakes are counted against nobody")
	require.Len(t, audits(t, store, loginguard.ActionFailed), 30, "but every one is audited")
}

func TestAllowlistedAddressMaySignInToALockedAccount(t *testing.T) {
	g, store, _ := newGuard(t)
	ctx := context.Background()
	set(t, store, g, map[string]string{loginguard.KeyIPAllowlist: "192.0.2.10, 2001:db8:77::/48"})
	// An attacker locks the account from elsewhere.
	for i := 0; i < 5; i++ {
		fail(g, web("admin@local", "198.51.100.66"))
	}
	require.True(t, g.Check(ctx, web("admin@local", "198.51.100.66")).Blocked)
	require.True(t, g.Check(ctx, web("admin@local", "198.51.100.67")).Blocked, "the lock is on the account, so another address is refused too")

	for _, ip := range []string{"192.0.2.10", "2001:db8:77::5"} {
		v := g.Check(ctx, web("admin@local", ip))
		require.False(t, v.Blocked, "%s: the lock is not applied to an allow-listed address", ip)
		require.True(t, v.Allowlisted)
		g.Succeeded(ctx, web("admin@local", ip))
	}
	pass := audits(t, store, loginguard.ActionAllowlistPass)
	require.Len(t, pass, 2, "each such sign-in is audited")
	require.ElementsMatch(t, []string{"192.0.2.10", "2001:db8:77::5"}, []string{pass[0].IP, pass[1].IP})
	for _, e := range pass {
		require.Equal(t, "admin@local", e.Metadata["identifier"])
		require.Equal(t, model.LoginThrottleAccount, e.Metadata["lock_scope"])
	}

	require.True(t, g.Check(ctx, web("admin@local", "198.51.100.67")).Blocked,
		"getting in through the allow-list does not lift the lock for anybody else")
}

func TestNoAccountIsPrivileged(t *testing.T) {
	g, _, _ := newGuard(t)
	// The bootstrap administrator's name gets no special treatment.
	for i := 0; i < 5; i++ {
		fail(g, web("admin@local", "198.51.100.66"))
	}
	require.True(t, g.Check(context.Background(), web("admin@local", "198.51.100.66")).Blocked)
}

func TestAllowlistDoesNotMatchAnAddressOutsideIt(t *testing.T) {
	g, store, _ := newGuard(t)
	set(t, store, g, map[string]string{loginguard.KeyIPAllowlist: "203.0.113.0/24"})
	require.False(t, g.Check(context.Background(), web("x@example.com", "203.0.114.1")).Allowlisted)
	require.False(t, g.Check(context.Background(), web("x@example.com", "")).Allowlisted)
	require.False(t, g.Check(context.Background(), web("x@example.com", "not-an-ip")).Allowlisted)
}

func TestAuditTrail(t *testing.T) {
	g, store, c := newGuard(t)
	ctx := context.Background()
	a := loginguard.Attempt{Identifier: "Ada@Example.com", IP: "203.0.113.1", Protocol: loginguard.ProtoDAV}
	for i := 0; i < 5; i++ {
		g.Failed(ctx, a, loginguard.ReasonCredentials)
	}
	failed := audits(t, store, loginguard.ActionFailed)
	require.Len(t, failed, 5)
	for _, e := range failed {
		require.Equal(t, "203.0.113.1", e.IP)
		require.Equal(t, "ada@example.com", e.Metadata["identifier"])
		require.Equal(t, "dav", e.Metadata["protocol"])
		require.Equal(t, loginguard.ReasonCredentials, e.Metadata["reason"])
		require.Equal(t, "login", e.TargetType)
	}
	locks := audits(t, store, loginguard.ActionLocked)
	require.Len(t, locks, 1, "one lock, one row (the address is nowhere near its limit)")
	require.Equal(t, model.LoginThrottleAccount, locks[0].Metadata["scope"])
	require.EqualValues(t, 1, locks[0].Metadata["level"])

	// Release: noticed on the next touch, stamped with when it really ended.
	c.advance(5 * time.Minute)
	g.Sweep(ctx)
	rel := audits(t, store, loginguard.ActionUnlocked)
	require.Len(t, rel, 1)
	require.Equal(t, "expired", rel[0].Metadata["reason"])
	require.Equal(t, "ada@example.com", rel[0].Metadata["identifier"])
	g.Sweep(ctx)
	require.Len(t, audits(t, store, loginguard.ActionUnlocked), 1, "released once, not on every sweep")

	// Nothing that resembles a secret is ever stored.
	for _, e := range audits(t, store, "login.") {
		for k := range e.Metadata {
			require.NotContains(t, strings.ToLower(k), "pass")
			require.NotContains(t, strings.ToLower(k), "secret")
		}
	}
}

func TestIPLockIsAuditedUnderTheAddress(t *testing.T) {
	g, store, _ := newGuard(t)
	for i := 0; i < 10; i++ {
		fail(g, web("user"+string(rune('a'+i))+"@example.com", "198.51.100.7"))
	}
	locks := audits(t, store, loginguard.ActionLocked)
	require.Len(t, locks, 1)
	require.Equal(t, "198.51.100.7", locks[0].TargetID)
	require.Equal(t, model.LoginThrottleIP, locks[0].Metadata["scope"])
}

func TestCountersSurviveANewGuard(t *testing.T) {
	// The reason they live in the database: a restart is not a free reset.
	g, store, c := newGuard(t)
	a := web("ada@example.com", "203.0.113.1")
	for i := 0; i < 5; i++ {
		fail(g, a)
	}
	g2 := loginguard.New(store)
	g2.Now = c.now
	require.True(t, g2.Check(context.Background(), a).Blocked)
}

func TestSettingsApplyAndAreBounded(t *testing.T) {
	g, store, _ := newGuard(t)
	ctx := context.Background()
	set(t, store, g, map[string]string{
		loginguard.KeyAccountMax: "2", loginguard.KeyLockBase: "30", loginguard.KeyLockMax: "45",
	})
	a := web("ada@example.com", "203.0.113.1")
	require.False(t, fail(g, a).Locked)
	o := fail(g, a)
	require.True(t, o.Locked, "two is the limit now")
	require.Equal(t, 30*time.Second, o.RetryAfter)
	require.Equal(t, 2, o.Limit)

	// A ceiling below the base cannot make a lock shrink.
	set(t, store, g, map[string]string{loginguard.KeyLockMax: "5"})
	require.Equal(t, 30*time.Second, g.Config(ctx).LockMax)
	// Nonsense falls back to the default rather than turning the guard off.
	set(t, store, g, map[string]string{loginguard.KeyAccountMax: "banana", loginguard.KeyIPMax: "-4"})
	require.Equal(t, 5, g.Config(ctx).AccountMax)
	require.Equal(t, 1, g.Config(ctx).IPMax, "out of range clamps to the nearest bound")
}

func TestDisabledGuardCountsNothing(t *testing.T) {
	g, store, _ := newGuard(t)
	ctx := context.Background()
	set(t, store, g, map[string]string{loginguard.KeyEnabled: "false"})
	a := web("ada@example.com", "203.0.113.1")
	for i := 0; i < 50; i++ {
		require.True(t, fail(g, a).Unlimited)
		require.False(t, g.Check(ctx, a).Blocked)
	}
	rows, _ := store.ListLoginThrottles(ctx, "", nil, 0)
	require.Empty(t, rows)
	require.Empty(t, audits(t, store, "login."))
}

func TestUnlockClearsTheCounter(t *testing.T) {
	g, _, _ := newGuard(t)
	ctx := context.Background()
	a := web("ada@example.com", "203.0.113.1")
	for i := 0; i < 5; i++ {
		fail(g, a)
	}
	ok, err := g.Unlock(ctx, model.LoginThrottleAccount, "ada@example.com")
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, g.Check(ctx, a).Blocked)
	ok, _ = g.Unlock(ctx, model.LoginThrottleAccount, "ada@example.com")
	require.False(t, ok)
}

func TestSubjectShortensLongIdentifiersWithoutMergingThem(t *testing.T) {
	long1 := strings.Repeat("a", 400) + "1"
	long2 := strings.Repeat("a", 400) + "2"
	s1, s2 := loginguard.Subject(long1), loginguard.Subject(long2)
	require.LessOrEqual(t, len([]rune(s1)), 190)
	require.NotEqual(t, s1, s2)
	require.Equal(t, s1, loginguard.Subject(strings.ToUpper(long1)), "still case-insensitive")
	require.Equal(t, "(empty)", loginguard.Subject("  "))
	require.Equal(t, "ab", loginguard.Subject("a\x00b\n"), "control characters never reach the table or the audit log")
	require.Equal(t, "müşteri@örnek.com", loginguard.Subject("MÜŞTERI@örnek.com"))
}

func TestSubjectFoldsTheOperatingSystemSpellingsOfOneAccount(t *testing.T) {
	for _, typed := range []string{"alex", "Alex", ".\\alex", ".\\ALEX", "CORP\\alex", "corp\\Alex", " NT SERVICE\\alex "} {
		require.Equal(t, "alex", loginguard.Subject(typed), typed)
	}
	// The address form is not folded onto the bare name: it may be somebody
	// else's e-mail, and it is read the way identity.Resolve reads it.
	require.Equal(t, "alex@corp.example", loginguard.Subject("Alex@Corp.Example"))
	require.NotEqual(t, loginguard.Subject("alex"), loginguard.Subject("alex@local"))
	// Whatever has an @ is an address and is left alone, backslash or not.
	require.Equal(t, "corp\\alex@example.com", loginguard.Subject("CORP\\Alex@Example.com"))
	// A backslash with nothing after it is not a name; it stays as typed.
	require.Equal(t, "corp\\", loginguard.Subject("CORP\\"))
	require.Equal(t, "(empty)", loginguard.Subject("   "))
}

func TestOneOperatingSystemAccountSharesOneAllowanceWhateverTheSpelling(t *testing.T) {
	g, _, _ := newGuard(t)
	ctx := context.Background()
	spellings := []string{"alex", ".\\alex", "CORP\\alex", "OTHER\\Alex", "alex"}
	for _, typed := range spellings {
		fail(g, web(typed, "203.0.113.9"))
	}
	require.True(t, g.Check(ctx, web("CORP\\alex", "203.0.113.10")).Blocked,
		"five wrong attempts in five spellings lock the one account")
}

func TestMessagesTellTheTruthInBothLanguages(t *testing.T) {
	en := loginguard.Outcome{Scope: model.LoginThrottleAccount, Remaining: 3, Limit: 5}.Message("en")
	require.Equal(t, "Wrong credentials. 3 attempts left; the account is locked at failed attempt 5.", en)
	require.Contains(t, loginguard.Outcome{Scope: model.LoginThrottleAccount, Remaining: 1, Limit: 5}.Message("en"), "1 attempt left")

	tr := loginguard.Outcome{Scope: model.LoginThrottleAccount, Remaining: 3, Limit: 5}.Message("tr")
	require.Equal(t, "Kullanıcı adı ya da parola hatalı. 3 hakkınız kaldı; 5. hatalı denemede hesap kilitlenir.", tr)

	ip := loginguard.Outcome{Scope: model.LoginThrottleIP, Remaining: 2, Limit: 10}.Message("en")
	require.Contains(t, ip, "from this address")

	locked := loginguard.Verdict{Blocked: true, Scope: model.LoginThrottleAccount, RetryAfter: 90 * time.Second}
	require.Equal(t, "Too many failed attempts: this account is locked. Try again in 2 minutes.", locked.Message("en"),
		"90 seconds is announced as 2 minutes: never promise a door opens sooner than it does")
	require.Equal(t, "Çok fazla hatalı deneme: hesap kilitlendi. 2 dakika sonra yeniden deneyin.", locked.Message("tr"))
	require.Contains(t, loginguard.Verdict{Blocked: true, Scope: model.LoginThrottleIP, RetryAfter: 20 * time.Second}.Message("en"), "20 seconds")
	require.Contains(t, loginguard.Verdict{Blocked: true, Scope: model.LoginThrottleIP, RetryAfter: 200 * time.Millisecond}.Message("en"), "1 second")

	// A key missing from the catalogue prints as the key itself.
	for _, m := range []string{en, tr, ip, locked.Message("en")} {
		require.NotContains(t, m, "server.login.")
	}
	require.Equal(t, 60, loginguard.RetryAfterSeconds(time.Minute))
	require.Equal(t, 1, loginguard.RetryAfterSeconds(0))
	require.Equal(t, 2, loginguard.RetryAfterSeconds(1100*time.Millisecond))
}
