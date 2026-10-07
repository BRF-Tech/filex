// Package memcache is filex's ONE in-process cache for small state and
// settings that live in the database: a bounded, expiring map (Cache), and the
// same map kept in front of a durable store, written through it in both
// directions (Through).
//
// The owner's ruling (2026-10-06, #184): keep it in the database, and where
// the database is not fast enough keep a simple in-memory cache in front of it
// - not Redis, nothing outside the process - for this kind of state and
// setting, both ways.
//
// # Why one package
//
// The tree grew its process-local caches one at a time, each a map, a mutex
// and a hand-written expiry: the storage usage card's (api/handlers
// quota_storages.go, now a Cache), the S3 etag memo, the protocol sessions'
// grant sets, the document server's record of who opened a session. Every one
// of them answers the same three questions - how big may it get, how long is
// an entry true, how does it forget one - and every hand-written answer is a
// place to get one wrong (a map with no ceiling is a memory incident). New
// state goes here; the older ones move here when they are next touched.
//
// # The rule that keeps it honest (Through)
//
// ⚠⚠ The cache is a READ ACCELERATOR, never the authority. Behind several
// filex instances sharing one database, another instance's write is not in
// this instance's cache, so an entry can be stale for up to its TTL:
//
//   - a write goes to the store FIRST and to the cache only when the store
//     took it (Set, Add, Delete): the cache never says something the store
//     does not;
//   - a read for speed may be answered from the cache (Get);
//   - a read that DECIDES something another instance may have changed - may
//     this save go over the file? - is read from the store (Fresh), which
//     also refreshes the cache. When the store cannot answer, Fresh says so
//     (err) and the caller chooses; it does not quietly fall back to a
//     possibly stale entry.
//
// A nil Backing is a cache with nothing behind it: every call works on memory
// alone (a unit test, a Service built without a database).
package memcache

import (
	"container/list"
	"context"
	"sync"
	"time"
)

// DefaultMaxEntries bounds a Cache whose Options leave MaxEntries at 0.
const DefaultMaxEntries = 4096

// Options shape a Cache.
type Options struct {
	// MaxEntries is the most entries held; when full, the least recently
	// used goes. 0 = DefaultMaxEntries.
	MaxEntries int
	// TTL is how long an entry is served after it was written; 0 = until it
	// is evicted or deleted.
	TTL time.Duration
	// Now is the clock; nil = time.Now. Tests move it.
	Now func() time.Time
}

type entry[K comparable, V any] struct {
	key K
	val V
	at  time.Time
}

// Cache is a bounded map with an expiry and a least-recently-used eviction.
// Safe for concurrent use; the zero value is not usable (New). Every method
// is nil-safe: a nil *Cache holds nothing and remembers nothing.
type Cache[K comparable, V any] struct {
	mu    sync.Mutex
	order *list.List // front = most recently used
	items map[K]*list.Element
	limit int
	ttl   time.Duration
	now   func() time.Time
}

// New builds a Cache.
func New[K comparable, V any](o Options) *Cache[K, V] {
	limit := o.MaxEntries
	if limit <= 0 {
		limit = DefaultMaxEntries
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return &Cache[K, V]{order: list.New(), items: map[K]*list.Element{}, limit: limit, ttl: o.TTL, now: now}
}

// Get returns the entry for k while it is fresh, and marks it used.
func (c *Cache[K, V]) Get(k K) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return zero, false
	}
	e := el.Value.(*entry[K, V])
	if c.expired(e) {
		c.removeLocked(el)
		return zero, false
	}
	c.order.MoveToFront(el)
	return e.val, true
}

// Put stores v for k (fresh from now), evicting the least recently used
// entries beyond the ceiling.
func (c *Cache[K, V]) Put(k K, v V) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		e := el.Value.(*entry[K, V])
		e.val, e.at = v, c.now()
		c.order.MoveToFront(el)
		return
	}
	c.items[k] = c.order.PushFront(&entry[K, V]{key: k, val: v, at: c.now()})
	for c.order.Len() > c.limit {
		c.removeLocked(c.order.Back())
	}
}

// Delete forgets k.
func (c *Cache[K, V]) Delete(k K) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		c.removeLocked(el)
	}
}

// DeleteFunc forgets every entry match says yes to (an invalidation by
// content: every entry of one node, say); n = entries forgotten.
func (c *Cache[K, V]) DeleteFunc(match func(K, V) bool) int {
	if c == nil || match == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for el := c.order.Front(); el != nil; {
		next := el.Next()
		e := el.Value.(*entry[K, V])
		if match(e.key, e.val) {
			c.removeLocked(el)
			n++
		}
		el = next
	}
	return n
}

// Expire forgets the entries past their TTL now (Get forgets them as it meets
// them; this is for a sweep); n = entries forgotten.
func (c *Cache[K, V]) Expire() int {
	if c == nil || c.ttl <= 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for el := c.order.Front(); el != nil; {
		next := el.Next()
		if c.expired(el.Value.(*entry[K, V])) {
			c.removeLocked(el)
			n++
		}
		el = next
	}
	return n
}

// Purge forgets everything.
func (c *Cache[K, V]) Purge() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order.Init()
	c.items = map[K]*list.Element{}
}

// Len is the number of entries held, fresh or not yet swept.
func (c *Cache[K, V]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

func (c *Cache[K, V]) expired(e *entry[K, V]) bool {
	return c.ttl > 0 && c.now().Sub(e.at) > c.ttl
}

// removeLocked: caller holds c.mu.
func (c *Cache[K, V]) removeLocked(el *list.Element) {
	e := el.Value.(*entry[K, V])
	delete(c.items, e.key)
	c.order.Remove(el)
}

// Backing is the durable store a Through keeps a Cache in front of.
type Backing[K comparable, V any] interface {
	// Load reads k; ok is false when there is nothing stored.
	Load(ctx context.Context, k K) (v V, ok bool, err error)
	// Store creates or overwrites k.
	Store(ctx context.Context, k K, v V) error
	// Add stores v for k unless something is stored already, and returns
	// what is stored now (the first writer's value).
	Add(ctx context.Context, k K, v V) (V, error)
	// Remove deletes k; nothing stored is not an error.
	Remove(ctx context.Context, k K) error
}

// Through is a Cache in front of a Backing, written through in both
// directions (see the package doc for the rule).
type Through[K comparable, V any] struct {
	cache *Cache[K, V]
	back  Backing[K, V]
}

// NewThrough keeps a Cache shaped by o in front of back (nil: memory only).
func NewThrough[K comparable, V any](back Backing[K, V], o Options) *Through[K, V] {
	return &Through[K, V]{cache: New[K, V](o), back: back}
}

// Cache is the memory half, for an invalidation the store does not drive.
func (t *Through[K, V]) Cache() *Cache[K, V] { return t.cache }

// Get answers from the cache, else from the store (and fills the cache). For
// a read whose answer may be a few seconds old; see Fresh.
func (t *Through[K, V]) Get(ctx context.Context, k K) (V, bool, error) {
	if v, ok := t.cache.Get(k); ok {
		return v, true, nil
	}
	return t.Fresh(ctx, k)
}

// Fresh answers from the store, and refreshes (or clears) the cache with what
// it said. The read for a decision another instance's write may change.
// Memory only (no Backing): the cache's answer.
func (t *Through[K, V]) Fresh(ctx context.Context, k K) (V, bool, error) {
	if t.back == nil {
		v, ok := t.cache.Get(k)
		return v, ok, nil
	}
	v, ok, err := t.back.Load(ctx, k)
	if err != nil {
		var zero V
		return zero, false, err
	}
	if ok {
		t.cache.Put(k, v)
	} else {
		t.cache.Delete(k)
	}
	return v, ok, nil
}

// Set writes v for k to the store and then to the cache. A write the store
// refuses changes neither.
func (t *Through[K, V]) Set(ctx context.Context, k K, v V) error {
	if t.back != nil {
		if err := t.back.Store(ctx, k, v); err != nil {
			return err
		}
	}
	t.cache.Put(k, v)
	return nil
}

// Add stores v for k unless something is stored already, and returns what is
// stored now; the cache holds that. Memory only: the cache's entry when it
// has a fresh one.
func (t *Through[K, V]) Add(ctx context.Context, k K, v V) (V, error) {
	if t.back == nil {
		if cur, ok := t.cache.Get(k); ok {
			return cur, nil
		}
		t.cache.Put(k, v)
		return v, nil
	}
	stored, err := t.back.Add(ctx, k, v)
	if err != nil {
		var zero V
		return zero, err
	}
	t.cache.Put(k, stored)
	return stored, nil
}

// Delete removes k from the store and then from the cache. A delete the store
// refuses still forgets the cache's entry: the next read asks the store.
func (t *Through[K, V]) Delete(ctx context.Context, k K) error {
	var err error
	if t.back != nil {
		err = t.back.Remove(ctx, k)
	}
	t.cache.Delete(k)
	return err
}
