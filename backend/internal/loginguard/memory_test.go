package loginguard_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The limiter decides from MEMORY and writes every change through to the
// table: the table is what a restart comes back to, never what an attempt
// waits on, and a table that stops answering neither opens the door nor
// locks everybody out.

var errDown = errors.New("database is down")

// flakyStore is a store whose table can stop answering — reads, writes or
// both — and that counts what the limiter asks of it.
type flakyStore struct {
	db.Store
	failReads, failWrites         atomic.Bool
	gets, loads, settings, prunes atomic.Int64
}

func (f *flakyStore) GetLoginThrottle(ctx context.Context, scope, subject string) (*model.LoginThrottle, error) {
	f.gets.Add(1)
	if f.failReads.Load() {
		return nil, errDown
	}
	return f.Store.GetLoginThrottle(ctx, scope, subject)
}

func (f *flakyStore) LoadLoginThrottles(ctx context.Context, since time.Time, limit int) ([]*model.LoginThrottle, error) {
	f.loads.Add(1)
	if f.failReads.Load() {
		return nil, errDown
	}
	return f.Store.LoadLoginThrottles(ctx, since, limit)
}

func (f *flakyStore) ListLoginThrottles(ctx context.Context, scope string, lockedAt *time.Time, limit int) ([]*model.LoginThrottle, error) {
	if f.failReads.Load() {
		return nil, errDown
	}
	return f.Store.ListLoginThrottles(ctx, scope, lockedAt, limit)
}

func (f *flakyStore) GetSetting(ctx context.Context, key string) (string, error) {
	f.settings.Add(1)
	if f.failReads.Load() {
		return "", errDown
	}
	return f.Store.GetSetting(ctx, key)
}

func (f *flakyStore) SaveLoginThrottle(ctx context.Context, t *model.LoginThrottle) error {
	if f.failWrites.Load() {
		return errDown
	}
	return f.Store.SaveLoginThrottle(ctx, t)
}

func (f *flakyStore) DeleteLoginThrottle(ctx context.Context, scope, subject string) (bool, error) {
	if f.failWrites.Load() {
		return false, errDown
	}
	return f.Store.DeleteLoginThrottle(ctx, scope, subject)
}

func (f *flakyStore) UpsertSetting(ctx context.Context, key, value string) error {
	if f.failWrites.Load() {
		return errDown
	}
	return f.Store.UpsertSetting(ctx, key, value)
}

func (f *flakyStore) PruneLoginThrottles(ctx context.Context, before time.Time) (int64, error) {
	f.prunes.Add(1)
	if f.failWrites.Load() {
		return 0, errDown
	}
	return f.Store.PruneLoginThrottles(ctx, before)
}

func newFlaky(t *testing.T) (*loginguard.Guard, *flakyStore, db.Store, *clock) {
	t.Helper()
	_, store := testutil.NewTestDB(t)
	fs := &flakyStore{Store: store}
	c := &clock{t: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	g := loginguard.New(fs)
	g.Now = c.now
	return g, fs, store, c
}

func tableRow(t *testing.T, store db.Store, scope, subject string) *model.LoginThrottle {
	t.Helper()
	row, err := store.GetLoginThrottle(context.Background(), scope, subject)
	require.NoError(t, err)
	return row
}

// The maintainer's rule: a write the table refuses does not change the decision. The
// lock falls and holds from memory; the table is caught up once it answers.
func TestALockHoldsWhileTheTableRefusesEveryWrite(t *testing.T) {
	g, fs, store, c := newFlaky(t)
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	fs.failWrites.Store(true)

	a := web("ada@example.com", "203.0.113.1")
	for i := 1; i <= 4; i++ {
		require.False(t, g.Check(ctx, a).Blocked)
		require.Equal(t, 5-i, fail(g, a).Remaining, "the count goes on in memory")
	}
	o := fail(g, a)
	require.True(t, o.Locked, "the fifth wrong attempt locks, table or no table")
	v := g.Check(ctx, a)
	require.True(t, v.Blocked, "and the lock is applied: not failing open")
	require.Equal(t, time.Minute, v.RetryAfter)
	require.False(t, g.Check(ctx, web("someone-else@example.com", "198.51.100.9")).Blocked, "not failing closed either")
	require.Nil(t, tableRow(t, store, model.LoginThrottleAccount, "ada@example.com"), "the table really took none of it")

	// The table answers again: the next sweep writes what it is owed.
	fs.failWrites.Store(false)
	g.Sweep(ctx)
	row := tableRow(t, store, model.LoginThrottleAccount, "ada@example.com")
	require.NotNil(t, row, "caught up")
	require.True(t, row.Locked(c.t))
	require.Equal(t, 1, row.LockLevel)

	// So a restart comes back to the lock.
	g2 := loginguard.New(store)
	g2.Now = c.now
	require.True(t, g2.Check(ctx, a).Blocked)
}

// "Or at the next write": a write that succeeds brings the owed ones with it.
func TestAWriteThatSucceedsCatchesUpTheOwedOnes(t *testing.T) {
	g, fs, store, _ := newFlaky(t)
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	fs.failWrites.Store(true)
	for i := 0; i < 3; i++ {
		fail(g, web("ada@example.com", "203.0.113.1"))
	}
	fs.failWrites.Store(false)
	fail(g, web("bob@example.com", "198.51.100.2"))
	row := tableRow(t, store, model.LoginThrottleAccount, "ada@example.com")
	require.NotNil(t, row, "ada's counter rode along with bob's write")
	require.Equal(t, 3, row.Fails)

	// A reset the table refused is owed the same way: memory forgets at once,
	// the table when it can.
	fs.failWrites.Store(true)
	g.Succeeded(ctx, web("ada@example.com", "203.0.113.1"))
	o := fail(g, web("ada@example.com", "203.0.113.1"))
	require.Equal(t, 4, o.Remaining, "the success reset the account in memory")
	fs.failWrites.Store(false)
	g.Sweep(ctx)
	require.Equal(t, 1, tableRow(t, store, model.LoginThrottleAccount, "ada@example.com").Fails)
}

// The maintainer's rule: at start the table's locks are in memory — and from then on
// the table is not what an attempt is decided by.
func TestALockInTheTableIsInMemoryAfterLoad(t *testing.T) {
	g, fs, store, c := newFlaky(t)
	ctx := context.Background()
	until := c.t.Add(10 * time.Minute)
	require.NoError(t, store.SaveLoginThrottle(ctx, &model.LoginThrottle{
		Scope: model.LoginThrottleAccount, Subject: "ada@example.com", LockLevel: 2,
		LockedUntil: &until, WindowStart: c.t, LastFailAt: c.t,
	}))

	require.NoError(t, g.Load(ctx))
	fs.failReads.Store(true)
	v := g.Check(ctx, web("Ada@Example.com", "198.51.100.9"))
	require.True(t, v.Blocked, "the lock the table held is in force, read from memory")
	require.Equal(t, 10*time.Minute, v.RetryAfter)
	require.Equal(t, model.LoginThrottleAccount, v.Scope)
	locks := g.List(ctx, "", &c.t, 0)
	require.Len(t, locks, 1)
	require.Equal(t, 2, locks[0].LockLevel, "the escalation came along too")
	require.Zero(t, fs.gets.Load(), "no attempt ever read a counter from the table")
}

// The table is read once: not per attempt, and not every few seconds for the
// settings either.
func TestTheTableIsReadOnceNotPerAttempt(t *testing.T) {
	g, fs, _, c := newFlaky(t)
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	loads, settings := fs.loads.Load(), fs.settings.Load()
	require.EqualValues(t, 1, loads)
	for i := 0; i < 30; i++ {
		a := web("user@example.com", "203.0.113.1")
		g.Check(ctx, a)
		fail(g, a)
		c.advance(3 * time.Second)
	}
	require.Zero(t, fs.gets.Load())
	require.Equal(t, settings, fs.settings.Load(), "the settings are not read again by the attempts")
}

// A table that cannot be read at start: the guard starts on the defaults with
// an empty memory, and the sweep reads the table once it answers — without
// losing what it counted in the meantime.
func TestATableThatWasDownAtStartIsReadAtTheNextSweep(t *testing.T) {
	g, fs, store, c := newFlaky(t)
	ctx := context.Background()
	until := c.t.Add(10 * time.Minute)
	require.NoError(t, store.SaveLoginThrottle(ctx, &model.LoginThrottle{
		Scope: model.LoginThrottleAccount, Subject: "ada@example.com", LockLevel: 1,
		LockedUntil: &until, WindowStart: c.t, LastFailAt: c.t,
	}))
	require.NoError(t, store.UpsertSetting(ctx, loginguard.KeyIPMax, "3"))

	fs.failReads.Store(true)
	require.Error(t, g.Load(ctx))
	require.Equal(t, 10, g.Config(ctx).IPMax, "no settings could be read: the defaults")
	require.False(t, g.Check(ctx, web("ada@example.com", "198.51.100.9")).Blocked, "the table's lock is not known yet")
	fail(g, web("bob@example.com", "198.51.100.9"))

	fs.failReads.Store(false)
	g.Tick(ctx)
	require.True(t, g.Check(ctx, web("ada@example.com", "198.51.100.9")).Blocked, "the counters are read at the next tick's sweep")
	require.Equal(t, 3, g.Config(ctx).IPMax, "and the settings")
	o := fail(g, web("bob@example.com", "198.51.100.9"))
	require.Equal(t, model.LoginThrottleIP, o.Scope)
	require.Equal(t, 1, o.Remaining, "the address's first miss, counted while the table was down, is kept: 3 - 2")
	bob := g.List(ctx, model.LoginThrottleAccount, nil, 0)
	require.Len(t, bob, 2)
	for _, r := range bob {
		if r.Subject == "bob@example.com" {
			require.Equal(t, 2, r.Fails, "memory's count wins over the table's older row")
		}
	}
}

// The maintainer's rule: a saved setting is in force at once (no cache), and the table
// it was saved to is the same as the memory.
func TestASavedSettingIsInForceAtOnce(t *testing.T) {
	g, fs, store, _ := newFlaky(t)
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	require.Equal(t, 5, g.Config(ctx).AccountMax)

	require.NoError(t, g.SaveSetting(ctx, loginguard.KeyAccountMax, "2"))
	require.Equal(t, 2, g.Config(ctx).AccountMax, "in force without a reload")
	v, err := store.GetSetting(ctx, loginguard.KeyAccountMax)
	require.NoError(t, err)
	require.Equal(t, "2", v, "and in the table")
	a := web("ada@example.com", "203.0.113.1")
	require.False(t, fail(g, a).Locked)
	require.True(t, fail(g, a).Locked, "the second miss locks, at once")

	// The memory is what decides: a write that went around the guard is not
	// in force until the guard reads the table again.
	require.NoError(t, store.UpsertSetting(ctx, loginguard.KeyAccountMax, "7"))
	require.Equal(t, 2, g.Config(ctx).AccountMax)
	g.Invalidate()
	require.Equal(t, 7, g.Config(ctx).AccountMax)

	// A table that cannot be read keeps the last value known...
	fs.failReads.Store(true)
	g.Invalidate()
	require.Equal(t, 7, g.Config(ctx).AccountMax)
	fs.failReads.Store(false)
	// ...and a write it refuses changes nothing, in memory either.
	fs.failWrites.Store(true)
	require.ErrorIs(t, g.SaveSetting(ctx, loginguard.KeyAccountMax, "9"), errDown)
	require.Equal(t, 7, g.Config(ctx).AccountMax)
	fs.failWrites.Store(false)

	require.Error(t, g.SaveSetting(ctx, "branding.name", "x"), "only its own keys")
}

// The trusted-proxy source reads the memory: a saved list is in force for the
// next request.
func TestTheTrustedProxySourceFollowsASaveAtOnce(t *testing.T) {
	g, _, _, _ := newFlaky(t)
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	src := g.TrustedProxySource("203.0.113.0/24")
	require.True(t, src().ContainsString("203.0.113.5"), "unset: the environment's list")
	require.NoError(t, g.SaveSetting(ctx, loginguard.KeyTrustedProxy, "loopback, 192.0.2.0/24"))
	require.True(t, src().ContainsString("192.0.2.5"))
	require.True(t, src().ContainsString("127.0.0.1"))
	require.False(t, src().ContainsString("203.0.113.5"))
	require.NoError(t, g.SaveSetting(ctx, loginguard.KeyTrustedProxy, ""))
	require.True(t, src().ContainsString("203.0.113.5"), "emptied: the environment's again")
	require.Nil(t, g.TrustedProxySource("")(), "neither: nil, which clientip reads as its default")
}

// The memory does not grow without bound: past its size the idle counters
// go, then the oldest — a counter with no lock in force before a lock.
func TestTheMemoryIsBoundedAndKeepsTheLocks(t *testing.T) {
	g, _, _, c := newFlaky(t)
	g.MaxCounters = 20
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	ada := web("ada@example.com", "203.0.113.1")
	for i := 0; i < 5; i++ {
		fail(g, ada)
	}
	require.True(t, g.Check(ctx, web("ada@example.com", "198.51.100.200")).Blocked)
	for i := 0; i < 200; i++ {
		c.advance(100 * time.Millisecond)
		fail(g, web("user"+string(rune('a'+i%26))+string(rune('a'+i/26))+"@example.com", "198.51.100."+itoa(i%250)))
	}
	require.LessOrEqual(t, len(g.List(ctx, "", nil, 1000)), 20, "never more than the memory's size")
	require.True(t, g.Check(ctx, web("ada@example.com", "198.51.100.200")).Blocked, "the lock outlives the flood")
}

// The minute's sweep also clears the memory: a released lock is released
// there, a counter idle for a day is dropped from it.
func TestTheSweepClearsTheMemory(t *testing.T) {
	g, _, store, c := newFlaky(t)
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	for i := 0; i < 5; i++ {
		fail(g, web("ada@example.com", "203.0.113.1"))
	}
	fail(g, web("bob@example.com", "198.51.100.2"))
	require.Len(t, g.List(ctx, "", &c.t, 0), 1)

	c.advance(2 * time.Minute)
	g.Sweep(ctx)
	require.Empty(t, g.List(ctx, "", &c.t, 0), "the lock that ran out is released in memory")
	require.Nil(t, tableRow(t, store, model.LoginThrottleAccount, "ada@example.com").LockedUntil, "and in the table")
	require.Len(t, g.List(ctx, "", nil, 0), 4, "the counters stay while they still count")

	c.advance(25 * time.Hour)
	g.Sweep(ctx)
	require.Empty(t, g.List(ctx, "", nil, 0), "a day idle: gone from memory")
	rows, err := store.ListLoginThrottles(ctx, "", nil, 0)
	require.NoError(t, err)
	require.Empty(t, rows, "and from the table")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
