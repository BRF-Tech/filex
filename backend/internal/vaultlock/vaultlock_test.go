package vaultlock_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/vaultlock"
)

// The write lock of docs/E2E-VAULT-FORMAT.md → The write lock, walked with a
// clock the test moves: one session at a time, the lease, the person's idle
// time, a break, a release, the index write that keeps the lock from being
// free, and two sessions - or two processes - racing for it.

// memStore is vault_locks in memory, compare-and-set like VaultLockSQL.
// beforeUpdate runs once, inside the next update, before it is applied: the
// moment another writer can slip in.
type memStore struct {
	mu           sync.Mutex
	rows         map[string]model.VaultLock
	idle         map[int64]int
	beforeUpdate func()
	updates      int
}

func newMem() *memStore {
	return &memStore{rows: map[string]model.VaultLock{}, idle: map[int64]int{}}
}

func memKey(tenant int64, vault string) string { return fmt.Sprintf("%d/%s", tenant, vault) }

func (m *memStore) GetVaultLock(_ context.Context, tenant int64, vault string) (*model.VaultLock, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[memKey(tenant, vault)]
	if !ok {
		return nil, nil
	}
	return &r, nil
}

func (m *memStore) InsertVaultLock(_ context.Context, l *model.VaultLock) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := memKey(l.TenantID, l.VaultID)
	if _, ok := m.rows[k]; ok {
		return false, nil
	}
	l.Rev = 1
	m.rows[k] = *l
	return true, nil
}

func (m *memStore) UpdateVaultLock(_ context.Context, l *model.VaultLock, rev int64) (bool, error) {
	m.mu.Lock()
	hook := m.beforeUpdate
	m.beforeUpdate = nil
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates++
	k := memKey(l.TenantID, l.VaultID)
	cur, ok := m.rows[k]
	if !ok || cur.Rev != rev {
		return false, nil
	}
	l.Rev = rev + 1
	m.rows[k] = *l
	return true, nil
}

func (m *memStore) GetVaultIdleMinutes(_ context.Context, user int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.idle[user], nil
}

func (m *memStore) SetVaultIdleMinutes(_ context.Context, user int64, n int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idle[user] = n
	return nil
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	store *memStore
	clk   *clock
	svc   *vaultlock.Service
	ended []vaultlock.Ended
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{store: newMem(), clk: &clock{t: time.UnixMilli(1_791_000_000_000)}}
	r.svc = r.service()
	return r
}

// service is one more Service on the same store and clock - a second filex
// process on the same database.
func (r *rig) service() *vaultlock.Service {
	s := vaultlock.New(r.store)
	s.SetClock(r.clk.now)
	s.OnEnded = func(_ context.Context, e vaultlock.Ended) { r.ended = append(r.ended, e) }
	return s
}

var (
	kasa  = vaultlock.Key{Tenant: 1, Vault: "c29535e79852477843d6ef1b4a71afef"}
	place = vaultlock.Place{StorageID: 3, Path: "Kasa"}
	ayse  = vaultlock.Holder{UserID: 7, Name: "Ayşe", Client: "web", Label: "Firefox, ofis"}
	can   = vaultlock.Holder{UserID: 8, Name: "Can", Client: "desktop", Label: "Dizüstü"}
	admin = vaultlock.Holder{UserID: 1, Name: "Yönetici"}
)

func lostReason(t *testing.T, err error) string {
	t.Helper()
	var lost *vaultlock.LostError
	require.True(t, errors.As(err, &lost), "want a lost lock, got %v", err)
	require.ErrorIs(t, err, vaultlock.ErrLost)
	return lost.Reason
}

func lostHolder(t *testing.T, err error) string {
	t.Helper()
	var lost *vaultlock.LostError
	require.True(t, errors.As(err, &lost), "want a lost lock, got %v", err)
	if lost.Holder == nil {
		return ""
	}
	return lost.Holder.Name
}

func lockedBy(t *testing.T, err error) *vaultlock.LockedError {
	t.Helper()
	var locked *vaultlock.LockedError
	require.True(t, errors.As(err, &locked), "want the vault locked, got %v", err)
	require.ErrorIs(t, err, vaultlock.ErrLocked)
	return locked
}

func TestTakeIsOneSession(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	got, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	require.Len(t, got.Token, 43, "32 bytes, base64url without padding")
	require.Equal(t, 60, got.LeaseSeconds)
	require.Equal(t, 180, got.IdleSeconds, "3 minutes until the person sets their own")
	require.True(t, r.clk.now().Add(time.Minute).Equal(got.ExpiresAt), "the lease is a minute")

	// The token is kept only as its hash.
	row, err := r.store.GetVaultLock(ctx, kasa.Tenant, kasa.Vault)
	require.NoError(t, err)
	want, ok := vaultlock.HashToken(got.Token)
	require.True(t, ok)
	require.Equal(t, want, row.TokenHash)
	require.NotContains(t, fmt.Sprintf("%+v", *row), got.Token)

	// Somebody else, and the same person in a second tab, are second sessions.
	for _, h := range []vaultlock.Holder{can, ayse} {
		r.clk.add(10 * time.Second)
		_, err = r.svc.Take(ctx, kasa, place, h)
		locked := lockedBy(t, err)
		require.Equal(t, "Ayşe", locked.Holder.Name)
		require.Equal(t, "web", locked.Holder.Client)
		require.Equal(t, "Firefox, ofis", locked.Holder.Label)
		require.Positive(t, locked.RetryAfter)
		require.LessOrEqual(t, locked.RetryAfter, time.Minute)
	}

	// The same vault id in another tenant is another lock.
	_, err = r.svc.Take(ctx, vaultlock.Key{Tenant: 2, Vault: kasa.Vault}, place, can)
	require.NoError(t, err)

	st, err := r.svc.State(ctx, kasa)
	require.NoError(t, err)
	require.NotNil(t, st)
	require.True(t, st.Mine(got.Token))
	require.False(t, st.Mine("not-a-token"))
	require.Equal(t, "Ayşe", st.Holder.Name)
}

func TestLeaseRunsOutWithoutHeartbeat(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	got, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)

	r.clk.add(15 * time.Second)
	ren, err := r.svc.Renew(ctx, kasa, got.Token, true)
	require.NoError(t, err)
	require.True(t, r.clk.now().Add(time.Minute).Equal(ren.ExpiresAt), "a renewal starts the lease again")

	// 60 seconds after the last renewal the lease is over.
	r.clk.add(60 * time.Second)
	_, err = r.svc.Renew(ctx, kasa, got.Token, true)
	require.Equal(t, vaultlock.ReasonExpired, lostReason(t, err))
	require.Len(t, r.ended, 1, "the ending is recorded once")
	require.Equal(t, vaultlock.ReasonExpired, r.ended[0].Reason)
	require.Equal(t, "Ayşe", r.ended[0].Holder.Name)
	require.Equal(t, place, r.ended[0].Place)

	// Asked again: the same answer, nothing recorded twice.
	_, err = r.svc.Renew(ctx, kasa, got.Token, true)
	require.Equal(t, vaultlock.ReasonExpired, lostReason(t, err))
	require.Len(t, r.ended, 1)

	// And the vault is free.
	_, err = r.svc.Take(ctx, kasa, place, can)
	require.NoError(t, err)
}

func TestIdleTimeEndsTheLock(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	require.ErrorIs(t, r.svc.SetIdleMinutes(ctx, ayse.UserID, 0), vaultlock.ErrIdleRange)
	require.ErrorIs(t, r.svc.SetIdleMinutes(ctx, ayse.UserID, 11), vaultlock.ErrIdleRange)
	require.NoError(t, r.svc.SetIdleMinutes(ctx, ayse.UserID, 1))
	n, err := r.svc.IdleMinutes(ctx, ayse.UserID)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	got, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	require.Equal(t, 60, got.IdleSeconds)

	// Heartbeats that say nothing is being done keep the lease, not the lock.
	for i := 0; i < 3; i++ {
		r.clk.add(15 * time.Second)
		_, err = r.svc.Renew(ctx, kasa, got.Token, false)
		require.NoError(t, err, "beat %d", i)
	}
	r.clk.add(15 * time.Second)
	_, err = r.svc.Renew(ctx, kasa, got.Token, false)
	require.Equal(t, vaultlock.ReasonIdle, lostReason(t, err))
	require.Equal(t, vaultlock.ReasonIdle, r.ended[len(r.ended)-1].Reason)

	// A writer that keeps working keeps it: active renewals and vault writes.
	got, err = r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		r.clk.add(15 * time.Second)
		if i%2 == 0 {
			_, err = r.svc.Renew(ctx, kasa, got.Token, true)
		} else {
			err = r.svc.Touch(ctx, kasa, got.Token)
		}
		require.NoError(t, err, "step %d", i)
	}

	// The idle time is the one read when the lock was taken.
	require.NoError(t, r.svc.SetIdleMinutes(ctx, ayse.UserID, 10))
	r.clk.add(59 * time.Second)
	_, err = r.svc.Renew(ctx, kasa, got.Token, false)
	require.NoError(t, err)
	r.clk.add(2 * time.Second)
	_, err = r.svc.Renew(ctx, kasa, got.Token, false)
	require.Equal(t, vaultlock.ReasonIdle, lostReason(t, err))
}

func TestBreakReleaseAndTaken(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	a, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	broken, err := r.svc.Break(ctx, kasa, admin)
	require.NoError(t, err)
	require.NotNil(t, broken)
	require.Equal(t, vaultlock.ReasonBroken, broken.Reason)
	require.Equal(t, "Ayşe", broken.Holder.Name)
	_, err = r.svc.Renew(ctx, kasa, a.Token, true)
	require.Equal(t, vaultlock.ReasonBroken, lostReason(t, err))
	require.Equal(t, "Yönetici", lostHolder(t, err), "a broken lock names who broke it")
	require.Equal(t, "Yönetici", broken.By.Name)
	nothing, err := r.svc.Break(ctx, kasa, admin)
	require.NoError(t, err)
	require.Nil(t, nothing, "nothing held, nothing broken")

	// Can takes it; Ayşe's old token now hears that it was taken.
	c, err := r.svc.Take(ctx, kasa, place, can)
	require.NoError(t, err)
	_, err = r.svc.Renew(ctx, kasa, a.Token, true)
	require.Equal(t, vaultlock.ReasonTaken, lostReason(t, err))
	require.Equal(t, "Can", lostHolder(t, err), "a taken lock names who holds it now")

	// A release frees it at once; a release of a token that does not hold
	// it changes nothing.
	require.NoError(t, r.svc.Release(ctx, kasa, a.Token, ""))
	st, err := r.svc.State(ctx, kasa)
	require.NoError(t, err)
	require.NotNil(t, st, "a stale token released nothing")
	require.NoError(t, r.svc.Release(ctx, kasa, c.Token, ""))
	require.Equal(t, vaultlock.ReasonReleased, r.ended[len(r.ended)-1].Reason)
	st, err = r.svc.State(ctx, kasa)
	require.NoError(t, err)
	require.Nil(t, st)
	_, err = r.svc.Renew(ctx, kasa, c.Token, true)
	require.Equal(t, vaultlock.ReasonReleased, lostReason(t, err))
	require.Equal(t, "", lostHolder(t, err))

	// The client that locked the vault after its idle time says so.
	d, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	r.clk.add(5 * time.Minute)
	require.NoError(t, r.svc.Release(ctx, kasa, d.Token, vaultlock.ReasonLockedIdle))
	require.Equal(t, vaultlock.ReasonLockedIdle, r.ended[len(r.ended)-1].Reason)
	_, err = r.svc.Renew(ctx, kasa, d.Token, true)
	require.Equal(t, vaultlock.ReasonReleased, lostReason(t, err))

	// A lease that ran out with nobody asking is closed by the next take,
	// and its holder hears `taken` once somebody else holds it.
	e, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	r.clk.add(2 * time.Minute)
	before := len(r.ended)
	_, err = r.svc.Take(ctx, kasa, place, can)
	require.NoError(t, err)
	require.Len(t, r.ended, before+1)
	require.Equal(t, vaultlock.ReasonExpired, r.ended[before].Reason)
	_, err = r.svc.Renew(ctx, kasa, e.Token, true)
	require.Equal(t, vaultlock.ReasonTaken, lostReason(t, err))
}

func TestIndexWriteKeepsTheVaultBusy(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()

	a, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	require.NoError(t, r.svc.BeginIndex(ctx, kasa, a.Token))
	require.ErrorIs(t, r.svc.BeginIndex(ctx, kasa, a.Token), vaultlock.ErrIndexRunning)

	// The lock is broken mid-write: nobody takes it over until the write
	// ends, or 60 seconds after it began.
	_, err = r.svc.Break(ctx, kasa, admin)
	require.NoError(t, err)
	r.clk.add(10 * time.Second)
	_, err = r.svc.Take(ctx, kasa, place, can)
	locked := lockedBy(t, err)
	require.Equal(t, "Ayşe", locked.Holder.Name)
	require.LessOrEqual(t, locked.RetryAfter, 50*time.Second)
	st, err := r.svc.State(ctx, kasa)
	require.NoError(t, err)
	require.NotNil(t, st, "the vault shows as busy while the write runs")

	require.NoError(t, r.svc.EndIndex(ctx, kasa, a.Token, 4, true))
	c, err := r.svc.Take(ctx, kasa, place, can)
	require.NoError(t, err, "the write ended: free")

	// The write that outlives its 60 seconds no longer holds the vault.
	require.NoError(t, r.svc.BeginIndex(ctx, kasa, c.Token))
	require.NoError(t, r.svc.Release(ctx, kasa, c.Token, ""))
	r.clk.add(59 * time.Second)
	_, err = r.svc.Take(ctx, kasa, place, ayse)
	lockedBy(t, err)
	r.clk.add(2 * time.Second)
	d, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	// A late end of the abandoned write changes nothing of the new lock.
	require.NoError(t, r.svc.EndIndex(ctx, kasa, c.Token, 9, true))
	row, err := r.store.GetVaultLock(ctx, kasa.Tenant, kasa.Vault)
	require.NoError(t, err)
	require.Zero(t, row.LastGen)

	// The generations committed under a lock reach its ending.
	for _, g := range []int64{5, 6} {
		require.NoError(t, r.svc.BeginIndex(ctx, kasa, d.Token))
		r.clk.add(time.Second)
		require.NoError(t, r.svc.EndIndex(ctx, kasa, d.Token, g, true))
	}
	require.NoError(t, r.svc.BeginIndex(ctx, kasa, d.Token))
	require.NoError(t, r.svc.EndIndex(ctx, kasa, d.Token, 7, false))
	require.NoError(t, r.svc.Release(ctx, kasa, d.Token, ""))
	last := r.ended[len(r.ended)-1]
	require.Equal(t, int64(5), last.FirstGen)
	require.Equal(t, int64(6), last.LastGen, "an abandoned write commits nothing")

	// An index write needs the lock.
	require.Equal(t, vaultlock.ReasonReleased, lostReason(t, r.svc.BeginIndex(ctx, kasa, d.Token)))
}

// TestTwoSessionsRace: two sessions - two processes on one database - read
// the free vault at the same moment. Both decide to take it; the row's
// revision lets exactly one write land, and the other reads again and finds
// the vault locked.
func TestTwoSessionsRace(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	// A row that exists, so both writes are compare-and-set updates.
	first, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	require.NoError(t, r.svc.Release(ctx, kasa, first.Token, ""))

	other := r.service()
	var (
		bTaken *vaultlock.Taken
		bErr   error
	)
	r.store.beforeUpdate = func() { bTaken, bErr = other.Take(ctx, kasa, place, can) }
	_, aErr := r.svc.Take(ctx, kasa, place, ayse)

	require.NoError(t, bErr, "the session that wrote first holds the lock")
	require.NotNil(t, bTaken)
	locked := lockedBy(t, aErr)
	require.Equal(t, "Can", locked.Holder.Name, "the loser read again and saw the winner")
	st, err := r.svc.State(ctx, kasa)
	require.NoError(t, err)
	require.True(t, st.Mine(bTaken.Token))
}

// TestTwoProcessesOneDatabase: the same race over the real store - two
// services on one SQLite database, as two filex processes behind one
// database are. The lock is in the database, not in either process.
func TestTwoProcessesOneDatabase(t *testing.T) {
	_, store := testutil.NewTestDB(t)
	ctx := context.Background()
	p1, p2 := vaultlock.New(store), vaultlock.New(store)

	got, err := p1.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	_, err = p2.Take(ctx, kasa, place, can)
	locked := lockedBy(t, err)
	require.Equal(t, "Ayşe", locked.Holder.Name)

	st, err := p2.State(ctx, kasa)
	require.NoError(t, err)
	require.NotNil(t, st)
	require.True(t, st.Mine(got.Token))

	_, err = p2.Renew(ctx, kasa, got.Token, true)
	require.NoError(t, err, "a renewal reaches whichever process serves it")
	broken, err := p2.Break(ctx, kasa, admin)
	require.NoError(t, err)
	require.NotNil(t, broken)
	_, err = p1.Renew(ctx, kasa, got.Token, true)
	require.Equal(t, vaultlock.ReasonBroken, lostReason(t, err))

	// Many sessions at once: exactly one wins.
	k := vaultlock.Key{Tenant: 0, Vault: "0123456789abcdef0123456789abcdef"}
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		wins int
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := vaultlock.New(store)
			_, err := svc.Take(ctx, k, place, vaultlock.Holder{UserID: int64(100 + i), Name: fmt.Sprint(i), Client: "cli"})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if !errors.Is(err, vaultlock.ErrLocked) && !errors.Is(err, vaultlock.ErrContention) {
				t.Errorf("session %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, 1, wins)
}

func TestTokens(t *testing.T) {
	_, ok := vaultlock.HashToken("")
	require.False(t, ok)
	_, ok = vaultlock.HashToken("c29535e79852477843d6ef1b4a71afef")
	require.False(t, ok, "not 32 bytes of base64url")
	r := newRig(t)
	ctx := context.Background()
	a, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	require.NoError(t, r.svc.Release(ctx, kasa, a.Token, ""))
	b, err := r.svc.Take(ctx, kasa, place, ayse)
	require.NoError(t, err)
	require.NotEqual(t, a.Token, b.Token, "every lock has a new token")
	// A malformed token holds nothing and releases nothing.
	_, err = r.svc.Renew(ctx, kasa, "garbage", true)
	lostReason(t, err)
	require.NoError(t, r.svc.Release(ctx, kasa, "garbage", ""))
	st, err := r.svc.State(ctx, kasa)
	require.NoError(t, err)
	require.True(t, st.Mine(b.Token))
}
