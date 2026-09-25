package stall

import (
	"fmt"
	"io"
	"net"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"
)

// Kind is what a silence limit was waiting for when it ran out.
type Kind int

const (
	// Answer: the request was sent and no answer started (AnswerWatch).
	Answer Kind = iota
	// Body: an answer started and then stopped arriving (Reader).
	Body
	// Silent: a connection read got nothing from the peer (Conn) — no answer
	// to a command, or a transfer that stopped.
	Silent
)

// Error is an attempt cut for giving no sign of life. It is a timeout, and a
// timeout is worth another attempt when the request can be sent again.
type Error struct {
	Kind  Kind
	Limit time.Duration
	Err   error
}

// What says what ran out, in words, with the limit.
func (e *Error) What() string {
	limit := e.Limit.Round(100 * time.Millisecond)
	switch e.Kind {
	case Answer:
		return fmt.Sprintf("no answer within %s of sending the request", limit)
	case Body:
		return fmt.Sprintf("the answer stopped arriving for %s", limit)
	}
	return fmt.Sprintf("the server sent nothing for %s", limit)
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.What() + ": " + e.Err.Error()
	}
	return e.What()
}
func (e *Error) Unwrap() error   { return e.Err }
func (e *Error) Timeout() bool   { return true }
func (e *Error) Temporary() bool { return true }

// RetryableError answers the AWS SDK's first question directly. Without it the
// SDK's connection check finds the url.Error inside, looks only at what that
// wraps — "use of closed network connection", from the watch closing it —
// and calls the attempt final.
func (e *Error) RetryableError() bool { return true }

// AnswerWatch closes the connection when the answer to a sent HTTP request
// does not start within limit. Closing, not cancelling: a cancelled context
// would also end the body of a download the caller has not read yet.
//
// Install Trace on the request's context, and call Stop when the transport
// hands over the answer — for a plain http.RoundTripper, when RoundTrip
// returns.
//
// ⚠ NOT at httptrace's GotFirstResponseByte. That hook fires once, on the
// first byte of ANY response, and an upload sent with "Expect: 100-continue"
// (the AWS SDK does it over 2 MB) gets an interim "100 Continue" before the
// body is even sent: a watch stopped there never runs, and a store that took
// the whole upload and then never answered was waited for forever.
type AnswerWatch struct {
	limit time.Duration
	fired atomic.Bool

	mu    sync.Mutex
	conn  net.Conn
	timer *time.Timer
	done  bool
}

// NewAnswerWatch watches one attempt.
func NewAnswerWatch(limit time.Duration) *AnswerWatch { return &AnswerWatch{limit: limit} }

// Limit is the wait the watch allows.
func (w *AnswerWatch) Limit() time.Duration { return w.limit }

// Fired reports whether the watch cut the attempt.
func (w *AnswerWatch) Fired() bool { return w.fired.Load() }

// Trace is the hooks that follow the request.
func (w *AnswerWatch) Trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		GotConn: func(i httptrace.GotConnInfo) {
			w.mu.Lock()
			w.conn = i.Conn
			w.mu.Unlock()
		},
		WroteRequest: func(httptrace.WroteRequestInfo) { w.arm() },
	}
}

// arm starts the wait when the request is out.
//
// ⚠ The transport may send the request again by itself: when a POOLED
// connection fails before any answer, an idempotent request (GET, HEAD) is
// replayed on a fresh connection, and the attempt goes on. That is right when
// the store had closed an idle connection — then the watch starts over — but
// not when the watch itself closed it: the replay would wait on the silent
// store unwatched. So after the watch has fired, a replay is cut at once.
func (w *AnswerWatch) arm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	if w.fired.Load() {
		CloseRaw(w.conn)
		return
	}
	w.timer = time.AfterFunc(w.limit, w.expire)
}

func (w *AnswerWatch) expire() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	w.fired.Store(true)
	CloseRaw(w.conn)
}

// Stop ends the watch: the answer has arrived (or the attempt is over).
func (w *AnswerWatch) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.done = true
	if w.timer != nil {
		w.timer.Stop()
	}
}

// Reader bounds a stalled answer body. Its timer runs only while a Read is
// waiting for the network, so a caller that reads slowly is never cut; a
// store that stops sending mid-listing or mid-download is.
type Reader struct {
	rc    io.ReadCloser
	limit time.Duration
	timer *time.Timer
	fired atomic.Bool
}

// NewReader wraps an answer body.
func NewReader(rc io.ReadCloser, limit time.Duration) *Reader {
	r := &Reader{rc: rc, limit: limit}
	r.timer = time.AfterFunc(time.Hour, func() {
		r.fired.Store(true)
		_ = r.rc.Close()
	})
	r.timer.Stop()
	return r
}

func (r *Reader) Read(p []byte) (int, error) {
	if r.fired.Load() {
		return 0, &Error{Kind: Body, Limit: r.limit}
	}
	r.timer.Reset(r.limit)
	n, err := r.rc.Read(p)
	if !r.timer.Stop() && r.fired.Load() {
		return n, &Error{Kind: Body, Limit: r.limit, Err: err}
	}
	return n, err
}

func (r *Reader) Close() error {
	r.timer.Stop()
	return r.rc.Close()
}
