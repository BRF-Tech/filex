package queue

import (
	"fmt"
	"sort"
	"sync"
)

// Factory constructs an empty Driver. Each call returns a fresh value so
// every queue instance has its own state. Drivers register one of these
// from their package init() block.
type Factory func() Driver

var (
	regMu  sync.RWMutex
	regMap = map[string]Factory{}
)

// Register installs factory under name (lower-cased). Subsequent
// Register calls with the same name overwrite — useful for tests but
// usually a sign of a bug.
func Register(name string, f Factory) {
	if name == "" || f == nil {
		return
	}
	regMu.Lock()
	defer regMu.Unlock()
	regMap[name] = f
}

// Get returns a fresh Driver for the named registry entry, or an error if
// no driver was registered.
func Get(name string) (Driver, error) {
	regMu.RLock()
	f, ok := regMap[name]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("queue: unknown driver %q (registered: %v)", name, Names())
	}
	return f(), nil
}

// MustGet panics if name is unknown — handy in test setup, never in
// server bootstrap (server.go uses Get + return-the-error).
func MustGet(name string) Driver {
	d, err := Get(name)
	if err != nil {
		panic(err)
	}
	return d
}

// Names returns the sorted list of registered drivers.
func Names() []string {
	regMu.RLock()
	out := make([]string, 0, len(regMap))
	for n := range regMap {
		out = append(out, n)
	}
	regMu.RUnlock()
	sort.Strings(out)
	return out
}
