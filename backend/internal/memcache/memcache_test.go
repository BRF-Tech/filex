package memcache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func (c *clock) move(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Unix(1_790_000_000, 0)} }

func cacheOf(o Options) *Cache[string, int] { return New[string, int](o) }

func TestCache_AnEntryIsServedUntilItsTTL(t *testing.T) {
	c := newClock()
	m := cacheOf(Options{TTL: time.Minute, Now: c.now})
	m.Put("a", 1)
	v, ok := m.Get("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)
	c.move(59 * time.Second)
	_, ok = m.Get("a")
	assert.True(t, ok, "still fresh")
	c.move(2 * time.Second)
	_, ok = m.Get("a")
	assert.False(t, ok, "past its TTL")
	assert.Equal(t, 0, m.Len(), "an expired entry met by Get is forgotten")
}

func TestCache_AFullCacheLetsTheLeastRecentlyUsedGo(t *testing.T) {
	m := cacheOf(Options{MaxEntries: 3})
	m.Put("a", 1)
	m.Put("b", 2)
	m.Put("c", 3)
	_, _ = m.Get("a") // a is used: b is now the oldest
	m.Put("d", 4)
	assert.Equal(t, 3, m.Len(), "never more than the ceiling")
	_, ok := m.Get("b")
	assert.False(t, ok, "the least recently used went")
	for _, k := range []string{"a", "c", "d"} {
		_, ok := m.Get(k)
		assert.True(t, ok, k)
	}
}

func TestCache_DeleteDeleteFuncExpirePurge(t *testing.T) {
	c := newClock()
	m := cacheOf(Options{TTL: time.Minute, Now: c.now})
	m.Put("a", 1)
	m.Put("b", 2)
	m.Put("c", 3)
	m.Delete("a")
	_, ok := m.Get("a")
	assert.False(t, ok)
	assert.Equal(t, 1, m.DeleteFunc(func(_ string, v int) bool { return v == 2 }))
	c.move(2 * time.Minute)
	m.Put("d", 4)
	assert.Equal(t, 1, m.Expire(), "c expired; d is fresh")
	assert.Equal(t, 1, m.Len())
	m.Purge()
	assert.Equal(t, 0, m.Len())
}

func TestCache_ANilCacheHoldsNothingAndDoesNotPanic(t *testing.T) {
	var m *Cache[string, int]
	m.Put("a", 1)
	_, ok := m.Get("a")
	assert.False(t, ok)
	m.Delete("a")
	assert.Equal(t, 0, m.Len())
	assert.Equal(t, 0, m.Expire())
	m.Purge()
}

func TestCache_ConcurrentUseIsSafe(t *testing.T) {
	m := cacheOf(Options{MaxEntries: 64, TTL: time.Minute})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				k := fmt.Sprintf("%d-%d", g, i%100)
				m.Put(k, i)
				_, _ = m.Get(k)
				if i%50 == 0 {
					m.Delete(k)
				}
			}
		}(g)
	}
	wg.Wait()
	assert.LessOrEqual(t, m.Len(), 64)
}

// table is a Backing that counts its calls and can be told to fail.
type table struct {
	mu    sync.Mutex
	rows  map[string]int
	loads int
	fail  error
}

func newTable() *table { return &table{rows: map[string]int{}} }

func (b *table) Load(_ context.Context, k string) (int, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.loads++
	if b.fail != nil {
		return 0, false, b.fail
	}
	v, ok := b.rows[k]
	return v, ok, nil
}

func (b *table) Store(_ context.Context, k string, v int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail != nil {
		return b.fail
	}
	b.rows[k] = v
	return nil
}

func (b *table) Add(_ context.Context, k string, v int) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail != nil {
		return 0, b.fail
	}
	if cur, ok := b.rows[k]; ok {
		return cur, nil
	}
	b.rows[k] = v
	return v, nil
}

func (b *table) Remove(_ context.Context, k string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.fail != nil {
		return b.fail
	}
	delete(b.rows, k)
	return nil
}

func TestThrough_AWriteGoesToTheStoreAndTheCache(t *testing.T) {
	ctx := context.Background()
	back := newTable()
	th := NewThrough[string, int](back, Options{TTL: time.Minute})
	require.NoError(t, th.Set(ctx, "a", 1))
	assert.Equal(t, 1, back.rows["a"], "the store has it")
	v, ok := th.Cache().Get("a")
	require.True(t, ok, "and the cache")
	assert.Equal(t, 1, v)
	got, ok, err := th.Get(ctx, "a")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 1, got)
	assert.Equal(t, 0, back.loads, "a cached read does not ask the store")
}

func TestThrough_AWriteTheStoreRefusesChangesNeither(t *testing.T) {
	ctx := context.Background()
	back := newTable()
	th := NewThrough[string, int](back, Options{})
	require.NoError(t, th.Set(ctx, "a", 1))
	back.fail = errors.New("database down")
	require.Error(t, th.Set(ctx, "a", 2))
	v, _ := th.Cache().Get("a")
	assert.Equal(t, 1, v, "the cache never says what the store does not")
}

func TestThrough_AMissFillsTheCacheFromTheStore(t *testing.T) {
	ctx := context.Background()
	back := newTable()
	back.rows["a"] = 7 // written by another instance
	th := NewThrough[string, int](back, Options{TTL: time.Minute})
	v, ok, err := th.Get(ctx, "a")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 7, v)
	_, _, _ = th.Get(ctx, "a")
	assert.Equal(t, 1, back.loads, "filled once, served from memory after")
}

func TestThrough_FreshReadsTheStoreEvenWhenTheCacheHasIt(t *testing.T) {
	ctx := context.Background()
	back := newTable()
	th := NewThrough[string, int](back, Options{TTL: time.Hour})
	require.NoError(t, th.Set(ctx, "a", 1))
	back.rows["a"] = 2 // another instance's write: not in this cache
	v, _, _ := th.Get(ctx, "a")
	assert.Equal(t, 1, v, "Get may be stale - that is what it is for")
	v, ok, err := th.Fresh(ctx, "a")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 2, v, "Fresh is the store's answer")
	v, _ = th.Cache().Get("a")
	assert.Equal(t, 2, v, "and the cache now holds it")

	delete(back.rows, "a") // removed elsewhere
	_, ok, _ = th.Fresh(ctx, "a")
	assert.False(t, ok)
	_, ok = th.Cache().Get("a")
	assert.False(t, ok, "a row gone from the store goes from the cache too")

	back.fail = errors.New("database down")
	_, _, err = th.Fresh(ctx, "a")
	assert.Error(t, err, "a store that cannot answer says so; Fresh does not guess from memory")
}

func TestThrough_AddKeepsTheFirstWriter(t *testing.T) {
	ctx := context.Background()
	back := newTable()
	th := NewThrough[string, int](back, Options{})
	v, err := th.Add(ctx, "a", 1)
	require.NoError(t, err)
	assert.Equal(t, 1, v)
	v, err = th.Add(ctx, "a", 2)
	require.NoError(t, err)
	assert.Equal(t, 1, v, "the first writer's value stands")
	assert.Equal(t, 1, back.rows["a"])
}

func TestThrough_DeleteForgetsBothEvenWhenTheStoreFails(t *testing.T) {
	ctx := context.Background()
	back := newTable()
	th := NewThrough[string, int](back, Options{})
	require.NoError(t, th.Set(ctx, "a", 1))
	back.fail = errors.New("database down")
	require.Error(t, th.Delete(ctx, "a"))
	_, ok := th.Cache().Get("a")
	assert.False(t, ok, "the next read asks the store")
}

func TestThrough_WithoutAStoreItIsTheCacheAlone(t *testing.T) {
	ctx := context.Background()
	th := NewThrough[string, int](nil, Options{})
	require.NoError(t, th.Set(ctx, "a", 1))
	v, ok, err := th.Fresh(ctx, "a")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 1, v)
	v, err = th.Add(ctx, "a", 2)
	require.NoError(t, err)
	assert.Equal(t, 1, v)
	require.NoError(t, th.Delete(ctx, "a"))
	_, ok, _ = th.Get(ctx, "a")
	assert.False(t, ok)
}
