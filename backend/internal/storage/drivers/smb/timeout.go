package smb

// How long the driver waits for a server that does not answer (issue #75, the
// rest of #73).
//
// Before, only the TCP connect was bounded (dial_timeout_s, 15 s). After it
// nothing was: go-smb2 honours a request's context, and the requests here ran
// on the caller's - which for a sync pass or a queued job never ends - so a
// server that stopped answering held the operation for good. A server that
// accepted the connection and never answered the negotiation did the same, and
// a dead session was handed out again forever: nothing dropped it.
//
// ⚠ A read deadline on the raw connection cannot do it (stall.Conn): go-smb2's
// receiver keeps one Read waiting for as long as the session is open, so on an
// idle session the deadline kills a healthy one. The limit is armed per CALL
// (stall.Activity), as in the SFTP driver:
//
//   - attempt_timeout_s bounds every wait for the server while a call runs:
//     connecting, the negotiation and login, the share's tree connect, the
//     answer to each request and each next piece of a download (per Read).
//     The clock restarts on every byte the server sends, so a transfer that
//     keeps moving is never cut; an upload counts the bytes the server takes
//     too, and may go a minute without moving (stall.SendStall). When the
//     limit runs out the connection is closed - every request waiting on it
//     fails - and the next call dials a new session. `dial_timeout_s`, its
//     old name, is still read.
//   - max_attempts and total_timeout_s: a connection that could not be made
//     is tried again within the budget, and so is an operation that changes
//     nothing (a listing, a stat, opening a download, a mkdir) whose session
//     broke. An upload, a rename, a copy or a delete that reached the server
//     is not sent again. A refusal (a wrong password, a share that does not
//     exist) is never retried.
//   - Every request carries the caller's context, so a caller whose context
//     ends leaves at once.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	smb2 "github.com/hirochachacha/go-smb2"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// defaults of the three settings: 15 s is what the connect alone
// (dial_timeout_s) was allowed before.
var defaults = stall.Defaults{AttemptTimeoutS: 15, MaxAttempts: 3, TotalTimeoutS: 15}

func (d *Driver) addr() string { return net.JoinHostPort(d.host, fmt.Sprint(d.port)) }

// session is one TCP connection, the SMB session on it and the mounted share.
// go-smb2 does not support two sessions on one connection, so a session IS
// its connection.
type session struct {
	act  *stall.Activity
	sess *smb2.Session
	fs   *smb2.Share
	dead atomic.Bool
}

// close cuts the connection: a logoff on a silent server would wait for it.
func (s *session) close() {
	if s.dead.Swap(true) {
		return
	}
	s.act.Cut()
}

// current returns the live session, dialing one when there is none
// (stall.One: one caller dials at a time; the others wait for it, or leave
// when their context ends).
func (d *Driver) current(ctx context.Context) (*session, error) {
	return d.sessions.Current(ctx, d.dial)
}

// drop retires s, so the next call dials a new session.
func (d *Driver) drop(s *session) {
	d.sessions.Forget(s)
	s.close()
}

// Dead reports whether the session was cut (stall.Session).
func (s *session) Dead() bool { return s.dead.Load() }

// dial connects, negotiates, logs in and mounts the share, under one silence
// limit.
func (d *Driver) dial(ctx context.Context) (*session, error) {
	t := d.policy.AttemptTimeout
	nd := net.Dialer{Timeout: t}
	raw, err := nd.DialContext(ctx, "tcp", d.addr())
	if err != nil {
		return nil, fmt.Errorf("smb: dial %s: %w", d.host, err)
	}
	act, conn := stall.Track(raw)
	watch := act.Watch(t, false)
	stop := context.AfterFunc(ctx, act.Cut)
	dd := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:     d.user,
			Password: d.password,
			Domain:   d.domain,
		},
	}
	var (
		sess    *smb2.Session
		fs      *smb2.Share
		mounted bool
	)
	sess, err = dd.DialContext(ctx, conn)
	if err == nil {
		mounted = true
		fs, err = sess.WithContext(ctx).Mount(d.share)
	}
	stop()
	fired := watch.Stop()
	if err != nil {
		act.Cut()
		switch {
		case fired:
			return nil, &stall.Error{Kind: stall.Silent, Limit: t, Err: err}
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case isTransport(err):
			return nil, fmt.Errorf("smb: %s: %w", d.host, err)
		case mounted:
			// ⚠ The share name is the commonest thing to get wrong and the
			// server's error for it is STATUS_BAD_NETWORK_NAME, which means
			// nothing to anybody. Named here.
			return nil, &refusal{fmt.Errorf("smb: mount share %q: %w", d.share, err)}
		}
		return nil, &refusal{fmt.Errorf("smb: authenticate as %s: %w", d.user, err)}
	}
	return &session{act: act, sess: sess, fs: fs}, nil
}

// work is what one operation is to its watch.
type work struct {
	// repeatable: the operation may be sent again after its session broke.
	repeatable bool
	// sends: an upload; the server taking its bytes is a sign of life.
	sends bool
}

var (
	readWork   = work{repeatable: true}
	changeWork = work{}
	sendWork   = work{sends: true}
)

// run does op on the share, under the policy.
func (d *Driver) run(ctx context.Context, w work, op func(fs *smb2.Share) error) error {
	return d.runOn(ctx, w, func(fs *smb2.Share, _ *session) error { return op(fs) })
}

// runOn is run for an op that keeps the session (a download watches its
// reads on it).
func (d *Driver) runOn(ctx context.Context, w work, op func(fs *smb2.Share, s *session) error) error {
	clock, err := d.policy.Do(ctx, d.retryable(w.repeatable), func(ctx context.Context) error {
		s, err := d.current(ctx)
		if err != nil {
			return &connectError{err}
		}
		return d.do(ctx, s, w, op)
	})
	if err == nil || ctx.Err() != nil {
		if err != nil {
			return ctx.Err()
		}
		return nil
	}
	var rf *refusal
	if errors.As(err, &rf) {
		return rf.err // the server's own words
	}
	if isTransport(err) {
		return d.policy.Unavailable(d.what, clock, err)
	}
	return mapErr(err)
}

// do runs op under one watch, every request on the caller's context.
func (d *Driver) do(ctx context.Context, s *session, w work, op func(fs *smb2.Share, s *session) error) error {
	limit, kind := d.policy.AttemptTimeout, stall.Silent
	if w.sends {
		limit, kind = d.policy.SendStall(), stall.Send
	}
	watch := s.act.Watch(limit, w.sends)
	err := op(s.fs.WithContext(ctx), s)
	fired := watch.Stop()
	if fired || (err != nil && isTransport(err)) {
		d.drop(s)
	}
	if fired {
		return &stall.Error{Kind: kind, Limit: limit, Err: err}
	}
	return err
}

// retryable says whether a failed attempt may be tried again (see run).
func (d *Driver) retryable(repeatable bool) func(error) bool {
	return func(err error) bool {
		var rf *refusal
		if errors.As(err, &rf) {
			return false
		}
		var dns *net.DNSError
		if errors.As(err, &dns) && dns.IsNotFound {
			return false
		}
		var ce *connectError
		if errors.As(err, &ce) || repeatable {
			return isTransport(err)
		}
		return false
	}
}

// isTransport: the error is the connection's, not the server's answer.
func isTransport(err error) bool {
	if err == nil {
		return false
	}
	var te *smb2.TransportError
	if errors.As(err, &te) {
		return true
	}
	var se *stall.Error
	if errors.As(err, &se) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "use of closed") || strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe")
}

// connectError is a failure to connect, negotiate, log in or mount - nothing
// of the operation had started, so it may be tried again.
type connectError struct{ err error }

func (e *connectError) Error() string { return e.err.Error() }
func (e *connectError) Unwrap() error { return e.err }

// refusal is the server saying no (a wrong password, a share that does not
// exist): never retried, never "unavailable".
type refusal struct{ err error }

func (e *refusal) Error() string { return e.err.Error() }
func (e *refusal) Unwrap() error { return e.err }

// fileReader is a download: every Read is watched, and only while it waits on
// the network, so a caller that reads slowly is never cut.
type fileReader struct {
	d     *Driver
	s     *session
	f     *smb2.File
	limit time.Duration
}

func (r *fileReader) Read(p []byte) (int, error) {
	w := r.s.act.Watch(r.limit, false)
	n, err := r.f.Read(p)
	if w.Stop() {
		r.d.drop(r.s)
		return n, &stall.Error{Kind: stall.Body, Limit: r.limit, Err: err}
	}
	if err != nil && err != io.EOF && isTransport(err) {
		r.d.drop(r.s)
	}
	return n, mapReadErr(err)
}

// Close closes the remote file under a watch too: a silent server never
// answers the CLOSE.
func (r *fileReader) Close() error {
	w := r.s.act.Watch(r.limit, false)
	err := r.f.Close()
	if w.Stop() {
		r.d.drop(r.s)
	}
	return err
}

// mapReadErr keeps io.EOF as it is (the end of the file, not an error).
func mapReadErr(err error) error {
	if err == nil || err == io.EOF {
		return err
	}
	if os.IsNotExist(err) {
		return storage.ErrNotFound
	}
	return err
}
