package plugin

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// What a plugin is allowed to cost filex.
//
// A plugin is somebody else's program in filex's request path. Without a
// ceiling, one slow plugin is one slow filex: every request that touches its
// storage holds a goroutine, a connection and (on the HTTP surface) a
// client, and a backend that accepts connections and then says nothing is
// indistinguishable from one that is merely busy. So each plugin gets a fixed
// number of in-flight operations and a deadline, and a caller that cannot get
// a slot is told so immediately instead of joining a queue nobody drains.

// Defaults chosen to be generous for a healthy plugin and firm for a sick
// one. Both are per PLUGIN, not per storage: the process is the resource.
const (
	// DefaultMaxInFlight is how many operations may be inside one plugin at
	// once. Ten is comfortably above what a browsing user generates and low
	// enough that a stuck plugin cannot consume the server.
	DefaultMaxInFlight = 10
	// DefaultOpTimeout bounds one operation. Streaming reads and writes are
	// exempt — a 20 GB upload is legitimately slow — so this applies to the
	// metadata calls, where slowness means trouble rather than size.
	DefaultOpTimeout = 60 * time.Second
	// slotWait is how long a caller waits for a slot before giving up. Short
	// on purpose: if every slot is busy, the honest answer is "the storage is
	// overloaded", not a request that hangs for a minute and then fails.
	slotWait = 5 * time.Second
)

// ErrBusy is returned when a plugin has no free slot. It is deliberately
// distinct from a timeout: this one says the plugin is saturated, which is an
// operator's problem to size, not a bug to chase.
var ErrBusy = errors.New("plugin is at its concurrency limit")

// limiter bounds concurrent operations against one plugin and keeps the
// counters the metrics surface reads.
type limiter struct {
	slots chan struct{}
	name  string

	inFlight atomic.Int64
	waited   atomic.Int64 // calls that had to wait for a slot
	rejected atomic.Int64 // calls that gave up waiting
}

func newLimiter(name string, max int) *limiter {
	if max <= 0 {
		max = DefaultMaxInFlight
	}
	return &limiter{slots: make(chan struct{}, max), name: name}
}

// acquire takes a slot, or fails fast. The returned func releases it.
func (l *limiter) acquire(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	select {
	case l.slots <- struct{}{}:
		l.inFlight.Add(1)
		return l.release, nil
	default:
	}
	// Busy: wait briefly, then say so.
	l.waited.Add(1)
	t := time.NewTimer(slotWait)
	defer t.Stop()
	select {
	case l.slots <- struct{}{}:
		l.inFlight.Add(1)
		return l.release, nil
	case <-ctx.Done():
		l.rejected.Add(1)
		return nil, ctx.Err()
	case <-t.C:
		l.rejected.Add(1)
		return nil, fmt.Errorf("%s: %w (%d operations already in flight)", l.name, ErrBusy, cap(l.slots))
	}
}

func (l *limiter) release() {
	l.inFlight.Add(-1)
	<-l.slots
}

// Stats is what the admin surface and Prometheus read.
type Stats struct {
	InFlight int64 `json:"in_flight"`
	Waited   int64 `json:"waited"`
	Rejected int64 `json:"rejected"`
	Max      int   `json:"max_in_flight"`
}

func (l *limiter) stats() Stats {
	if l == nil {
		return Stats{}
	}
	return Stats{
		InFlight: l.inFlight.Load(),
		Waited:   l.waited.Load(),
		Rejected: l.rejected.Load(),
		Max:      cap(l.slots),
	}
}
