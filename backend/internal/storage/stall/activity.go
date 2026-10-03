package stall

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Activity bounds silence on a connection a library reads from in the
// BACKGROUND - SSH's multiplexer under the SFTP driver, the receiver goroutine
// under the SMB driver (issue #75).
//
// ⚠ Conn cannot do it. Conn sets a deadline on every Read, and those libraries
// keep one Read waiting for as long as the session is open: on a session that
// sits idle between two operations, that Read is waiting for nothing, and a
// deadline on it kills a healthy session. So the limit is armed per CALL
// instead (Watch): while an operation runs, the peer must show a sign of life
// - a byte arrives, or, for a send, a byte leaves - at least once every limit;
// when it does not, the connection is cut (Cut), which fails the operation and
// everything else waiting on that connection, and the driver opens a new one.
//
// The clock runs only while a watched call is under way, so an idle session
// is never cut, and it restarts on every byte, so a transfer that keeps moving
// is never cut either.
type Activity struct {
	conn net.Conn
	recv atomic.Int64 // UnixNano of the last byte read from the peer
	sent atomic.Int64 // UnixNano of the last byte the peer took
	cut  atomic.Bool
}

// Track wraps c: hand the returned connection to the library and keep the
// Activity to watch its calls with.
func Track(c net.Conn) (*Activity, net.Conn) {
	a := &Activity{conn: c}
	return a, &tracked{Conn: c, a: a}
}

type tracked struct {
	net.Conn
	a *Activity
}

func (t *tracked) Read(p []byte) (int, error) {
	n, err := t.Conn.Read(p)
	if n > 0 {
		t.a.recv.Store(time.Now().UnixNano())
	}
	return n, err
}

func (t *tracked) Write(p []byte) (int, error) {
	n, err := t.Conn.Write(p)
	if n > 0 {
		t.a.sent.Store(time.Now().UnixNano())
	}
	return n, err
}

// Cut closes the connection: every call waiting on it fails at once.
func (a *Activity) Cut() {
	a.cut.Store(true)
	CloseRaw(a.conn)
}

// WasCut reports whether the connection was cut.
func (a *Activity) WasCut() bool { return a.cut.Load() }

// Watch bounds the silence of one call: if, while it runs, nothing arrives
// from the peer for limit - nor, when sends, does the peer take anything we
// send - the connection is cut. Stop the watch when the call returns.
func (a *Activity) Watch(limit time.Duration, sends bool) *CallWatch {
	w := &CallWatch{a: a, limit: limit, sends: sends, armed: time.Now().UnixNano()}
	if limit > 0 {
		w.mu.Lock()
		w.timer = time.AfterFunc(limit, w.check)
		w.mu.Unlock()
	}
	return w
}

// CallWatch is one call's silence limit (Activity.Watch).
type CallWatch struct {
	a     *Activity
	limit time.Duration
	sends bool
	armed int64
	fired atomic.Bool

	mu    sync.Mutex
	timer *time.Timer
	done  bool
}

// check runs when the limit may have run out: the time since the last sign of
// life decides whether it has, or how much longer to wait.
func (w *CallWatch) check() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	last := max(w.armed, w.a.recv.Load())
	if w.sends {
		last = max(last, w.a.sent.Load())
	}
	quiet := time.Since(time.Unix(0, last))
	if quiet >= w.limit {
		w.fired.Store(true)
		w.a.Cut()
		return
	}
	w.timer = time.AfterFunc(w.limit-quiet, w.check)
}

// Limit is the silence the watch allows.
func (w *CallWatch) Limit() time.Duration { return w.limit }

// Stop ends the watch and reports whether it cut the connection.
func (w *CallWatch) Stop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.done = true
	if w.timer != nil {
		w.timer.Stop()
	}
	return w.fired.Load()
}
