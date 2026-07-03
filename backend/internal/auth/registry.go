package auth

import (
	"fmt"
	"sort"
	"sync"
)

// Factory builds a fresh Driver instance — invoked once per server start.
type Factory func() Driver

var (
	regMu       sync.RWMutex
	registry    = map[string]Factory{}
	enabledList []Driver
)

// Register adds a driver factory keyed by name. Called from drivers' init().
func Register(name string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, dup := registry[name]; dup {
		panic("auth: duplicate driver registration: " + name)
	}
	registry[name] = f
}

// Get returns a freshly-constructed driver instance, error if unknown.
func Get(name string) (Driver, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	f, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("auth: unknown driver %q", name)
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

// SetEnabled stores the active driver chain (ordered) for the middleware.
func SetEnabled(drivers []Driver) {
	regMu.Lock()
	defer regMu.Unlock()
	enabledList = drivers
}

// Enabled returns the configured driver chain.
func Enabled() []Driver {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]Driver, len(enabledList))
	copy(out, enabledList)
	return out
}
