package storage

import (
	"fmt"
	"sort"
	"sync"
)

// Factory builds a fresh Driver instance — invoked once per Storage row.
type Factory func() Driver

var (
	regMu    sync.RWMutex
	registry = map[string]Factory{}
)

// Register adds a driver factory, called from drivers' init().
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("storage: duplicate driver registration: " + name)
	}
	registry[name] = f
}

// Get returns a freshly-constructed driver instance, error if unknown.
func Get(name string) (Driver, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("storage: unknown driver %q", name)
	}
	return f(), nil
}

// Names returns the registered driver names sorted alphabetically.
func Names() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
