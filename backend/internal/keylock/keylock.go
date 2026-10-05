// Package keylock serialises work per key: one holder of a key at a time,
// any number of keys at once. A key's lock lives while it is held or waited
// for, and is dropped afterwards, so a map of names does not grow with every
// name ever locked.
package keylock

import "sync"

// Map is a set of locks by key. The zero value is ready to use.
type Map struct {
	mu    sync.Mutex
	locks map[string]*entry
}

type entry struct {
	mu   sync.Mutex
	refs int
}

// Lock waits until key is free, takes it, and answers the function that
// gives it back (call it exactly once).
func (m *Map) Lock(key string) (unlock func()) {
	m.mu.Lock()
	if m.locks == nil {
		m.locks = map[string]*entry{}
	}
	e := m.locks[key]
	if e == nil {
		e = &entry{}
		m.locks[key] = e
	}
	e.refs++
	m.mu.Unlock()
	e.mu.Lock()
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Unlock()
			m.mu.Lock()
			e.refs--
			if e.refs == 0 {
				delete(m.locks, key)
			}
			m.mu.Unlock()
		})
	}
}

// Len is the number of keys held or waited for (tests).
func (m *Map) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.locks)
}
