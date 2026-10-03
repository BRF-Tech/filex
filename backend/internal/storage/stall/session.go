package stall

import (
	"context"
	"sync"
)

// Session is what One holds: a driver's session to its server, which knows
// when it has been cut.
type Session interface {
	comparable
	Dead() bool
}

// One is a driver's one shared session to a server that can go silent (the
// SFTP and SMB drivers). Current hands out the live session and dials a new
// one when there is none; Forget retires a session that stopped answering, so
// the next call dials again; Take empties it for shutdown.
//
// ⚠ One caller dials at a time and the others wait for it - or leave as soon
// as their own context ends, so a dial that hangs on a dead server holds
// nobody but the caller who started it.
type One[S Session] struct {
	mu       sync.Mutex
	sess     S
	dialOnce sync.Once
	dialing  chan struct{}
}

// Current returns the live session, dialing one with dial when there is none.
func (o *One[S]) Current(ctx context.Context, dial func(context.Context) (S, error)) (S, error) {
	var none S
	if s, ok := o.Live(); ok {
		return s, nil
	}
	o.dialOnce.Do(func() { o.dialing = make(chan struct{}, 1) })
	select {
	case o.dialing <- struct{}{}:
	case <-ctx.Done():
		return none, ctx.Err()
	}
	defer func() { <-o.dialing }()
	if s, ok := o.Live(); ok {
		return s, nil
	}
	s, err := dial(ctx)
	if err != nil {
		return none, err
	}
	o.mu.Lock()
	o.sess = s
	o.mu.Unlock()
	return s, nil
}

// Live returns the session held, when there is one and it is not dead.
func (o *One[S]) Live() (S, bool) {
	var none S
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.sess != none && !o.sess.Dead() {
		return o.sess, true
	}
	return none, false
}

// Forget lets go of s if it is the session held; the caller closes s.
func (o *One[S]) Forget(s S) {
	var none S
	o.mu.Lock()
	if o.sess == s {
		o.sess = none
	}
	o.mu.Unlock()
}

// Take empties the holder and returns what it held (shutdown).
func (o *One[S]) Take() (S, bool) {
	var none S
	o.mu.Lock()
	s := o.sess
	o.sess = none
	o.mu.Unlock()
	return s, s != none
}
