package loginguard_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/loginguard"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The housekeeping runs on a clock of its own (Guard.Run, started with the
// server), not on the traffic: a quiet instance still catches the table up,
// releases the locks that ran out, and — every instance, every minute — reads
// the settings again, so a change made on one replica reaches the others
// within a minute. The counters stay each instance's own.

// With no sign-in attempt at all, the loop writes what the table is owed and
// releases the lock that ran out; it stops with its context.
func TestTheLoopSweepsWithoutTraffic(t *testing.T) {
	g, fs, store, c := newFlaky(t)
	g.Every = 10 * time.Millisecond
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	fs.failWrites.Store(true)
	for i := 0; i < 5; i++ {
		fail(g, web("ada@example.com", "203.0.113.1"))
	}
	fs.failWrites.Store(false)
	require.Nil(t, tableRow(t, store, model.LoginThrottleAccount, "ada@example.com"))
	c.advance(2 * time.Minute) // the lock (one minute) has run out; nobody is trying to sign in

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { g.Run(runCtx); close(done) }()
	require.Eventually(t, func() bool {
		row, err := store.GetLoginThrottle(ctx, model.LoginThrottleAccount, "ada@example.com")
		return err == nil && row != nil && row.LockLevel == 1 && row.LockedUntil == nil
	}, 3*time.Second, 10*time.Millisecond, "the loop wrote the owed counter and released the lock, with no attempt")
	// The audit entry is written after the row: wait for it as well, then
	// hold it to exactly one (GitHub's -race run of v0.53.0 read it between
	// the two and found none).
	require.Eventually(t, func() bool {
		rows, err := store.ListAuditRecent(ctx, 500)
		if err != nil {
			return false
		}
		for _, r := range rows {
			if strings.HasPrefix(r.Action, loginguard.ActionUnlocked) {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond, "and audited the release")
	require.Len(t, audits(t, store, loginguard.ActionUnlocked), 1, "audited once")

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop when its context ended")
	}
}

// Attempts do not run the housekeeping: that is the loop's job alone.
func TestAttemptsDoNotSweep(t *testing.T) {
	g, fs, _, c := newFlaky(t)
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	for i := 0; i < 5; i++ {
		c.advance(2 * time.Minute)
		g.Check(ctx, web("ada@example.com", "203.0.113.1"))
		fail(g, web("ada@example.com", "203.0.113.1"))
	}
	require.Zero(t, fs.prunes.Load(), "ten minutes of attempts pruned nothing")
	g.Tick(ctx)
	require.EqualValues(t, 1, fs.prunes.Load(), "one tick, one sweep")
}

// The maintainer's rule for replicas: a settings change saved on one instance is in
// force on another after that one's next tick — the numbers, the allow-list,
// the switch and the trusted proxies alike.
func TestASecondInstanceSeesASettingChangeAtItsNextTick(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	a, b := loginguard.New(store), loginguard.New(store)
	require.NoError(t, a.Load(ctx))
	require.NoError(t, b.Load(ctx))

	require.NoError(t, a.SaveSetting(ctx, loginguard.KeyAccountMax, "2"))
	require.NoError(t, a.SaveSetting(ctx, loginguard.KeyIPAllowlist, "192.0.2.0/24"))
	require.NoError(t, a.SaveSetting(ctx, loginguard.KeyTrustedProxy, "loopback"))
	require.NoError(t, a.SaveSetting(ctx, loginguard.KeyEnabled, "false"))
	require.Equal(t, 5, b.Config(ctx).AccountMax, "until its tick the other instance decides with what it holds")

	b.Tick(ctx)
	cfg := b.Config(ctx)
	require.Equal(t, 2, cfg.AccountMax)
	require.True(t, cfg.Allow.ContainsString("192.0.2.9"))
	require.False(t, cfg.Enabled)
	src := b.TrustedProxySource("")
	require.True(t, src().ContainsString("::1"))
	require.False(t, src().ContainsString("10.1.2.3"), "the saved list replaced the default on this instance too")
}

// A tick whose read the table refuses keeps the last value known.
func TestATickKeepsTheLastSettingsWhenTheTableCannotBeRead(t *testing.T) {
	g, fs, store, _ := newFlaky(t)
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	require.NoError(t, store.UpsertSetting(ctx, loginguard.KeyAccountMax, "3"), "another instance saves")
	g.Tick(ctx)
	require.Equal(t, 3, g.Config(ctx).AccountMax)

	require.NoError(t, store.UpsertSetting(ctx, loginguard.KeyAccountMax, "4"))
	fs.failReads.Store(true)
	g.Tick(ctx)
	require.Equal(t, 3, g.Config(ctx).AccountMax, "the table did not answer: the last value known stays")
	fs.failReads.Store(false)
	g.Tick(ctx)
	require.Equal(t, 4, g.Config(ctx).AccountMax)
}

// The counters are each instance's own: the tick does not read the table's
// counters again (only Load does, once), so a lock another instance placed is
// not seen here — which is why N instances give an account up to N times the
// limit (docs/CONFIGURATION.md).
func TestTheTickDoesNotReadTheCountersAgain(t *testing.T) {
	g, fs, store, c := newFlaky(t)
	ctx := context.Background()
	require.NoError(t, g.Load(ctx))
	until := c.t.Add(10 * time.Minute)
	require.NoError(t, store.SaveLoginThrottle(ctx, &model.LoginThrottle{
		Scope: model.LoginThrottleAccount, Subject: "ada@example.com", LockLevel: 1,
		LockedUntil: &until, WindowStart: c.t, LastFailAt: c.t,
	}), "another instance locks ada")
	g.Tick(ctx)
	g.Tick(ctx)
	require.EqualValues(t, 1, fs.loads.Load(), "the counters were read once, at Load")
	require.False(t, g.Check(ctx, web("ada@example.com", "198.51.100.9")).Blocked)
}

// Every tick ends with OnTick: the server hangs the re-resolution of the
// automatic trusted-proxy set on it (a container can join a network at run
// time), so it runs on the loop's clock - every minute, traffic or not.
func TestEveryTickRunsOnTick(t *testing.T) {
	g, _, _, _ := newFlaky(t)
	g.Every = 10 * time.Millisecond
	var ticks atomic.Int32
	g.OnTick = func(context.Context) { ticks.Add(1) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go g.Run(ctx)
	require.Eventually(t, func() bool { return ticks.Load() >= 2 }, 3*time.Second, 5*time.Millisecond)

	g2, _, _, _ := newFlaky(t)
	g2.Tick(context.Background()) // no OnTick: nothing to call, nothing breaks
}
