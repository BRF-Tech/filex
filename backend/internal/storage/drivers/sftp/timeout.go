package sftp

// How long the driver waits for a server that does not answer (issue #75, the
// rest of #73).
//
// Before, only the TCP connect and the SSH handshake were bounded (10 s).
// Once connected nothing was: a server that stopped answering left every
// operation waiting until the operating system gave up on the connection -
// and the driver kept handing out that dead session for good, because nothing
// ever dropped it. A server that accepted the connection and never sent its
// banner waited out the 10 s; an upload or a download the server stopped
// moving waited forever.
//
// ⚠ A read deadline on the raw connection cannot do it (stall.Conn): SSH's
// multiplexer keeps one Read waiting for as long as the session is open, so on
// a session that sits idle between two operations the deadline kills a healthy
// session. The limit is armed per CALL instead (stall.Activity):
//
//   - attempt_timeout_s bounds every wait for the server while a call runs:
//     connecting, the SSH handshake and login, the answer to each request,
//     and each next piece of a download (per Read). The clock restarts on
//     every byte the server sends, so a transfer that keeps moving is never
//     cut. An upload counts the bytes the server takes too, and may go a
//     minute without moving (stall.SendStall). When the limit runs out the
//     SSH connection is closed, which ends the call, and the next call dials
//     a new session.
//   - max_attempts and total_timeout_s: a connection that could not be made
//     (refused, silent, dropped) is tried again within the budget, and so is
//     an operation that changes nothing (a listing, a stat, opening a
//     download, a mkdir) whose session broke. An upload, a rename, a copy or a
//     delete that reached the server is not sent again. A refusal (a wrong
//     password, a host key that does not match) is never retried.
//   - Every call honours its context: a caller whose context ends leaves at
//     once. The call it started is abandoned to its watch, which cuts a silent
//     server; an upload stops reading the caller's stream first.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// defaults of the three settings: the FTP driver's (15 s is what the SMB
// driver's connect was allowed before; SFTP's handshake had 10).
var defaults = stall.Defaults{AttemptTimeoutS: 15, MaxAttempts: 3, TotalTimeoutS: 15}

func (d *Driver) addr() string { return net.JoinHostPort(d.host, fmt.Sprintf("%d", d.port)) }

// session is one SSH connection and the SFTP client on it.
type session struct {
	ssh    *ssh.Client
	client *sftp.Client
	act    *stall.Activity
	dead   atomic.Bool
}

// close cuts the connection first: a client closed on a silent server waits
// for its reader, and the reader waits on the connection.
func (s *session) close() {
	if s.dead.Swap(true) {
		return
	}
	s.act.Cut()
	go func() {
		_ = s.client.Close()
		_ = s.ssh.Close()
	}()
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

// dial connects, shakes hands, logs in and starts the SFTP subsystem, all
// under one silence limit.
func (d *Driver) dial(ctx context.Context) (*session, error) {
	hostKeyCB, err := d.hostKeyCallback()
	if err != nil {
		return nil, &refusal{fmt.Errorf("sftp: host key: %w", err)}
	}
	cfg := &ssh.ClientConfig{User: d.user, HostKeyCallback: hostKeyCB}
	if d.password != "" {
		cfg.Auth = append(cfg.Auth, ssh.Password(d.password))
	}
	if d.keyPEM != "" {
		signer, err := ssh.ParsePrivateKey([]byte(d.keyPEM))
		if err != nil {
			return nil, &refusal{fmt.Errorf("sftp: parse key: %w", err)}
		}
		cfg.Auth = append(cfg.Auth, ssh.PublicKeys(signer))
	}
	t := d.policy.AttemptTimeout
	nd := net.Dialer{Timeout: t}
	raw, err := nd.DialContext(ctx, "tcp", d.addr())
	if err != nil {
		return nil, fmt.Errorf("sftp: dial: %w", err)
	}
	act, conn := stall.Track(raw)
	watch := act.Watch(t, false)
	stop := context.AfterFunc(ctx, act.Cut)
	var (
		client *ssh.Client
		sc     *sftp.Client
	)
	c, chans, reqs, err := ssh.NewClientConn(conn, d.addr(), cfg)
	if err == nil {
		client = ssh.NewClient(c, chans, reqs)
		sc, err = sftp.NewClient(client)
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
		case errors.Is(err, io.EOF):
			return nil, stall.Closed(fmt.Errorf("sftp: handshake: %w", err))
		case refusedBySSH(err):
			return nil, &refusal{fmt.Errorf("sftp: %w", err)}
		}
		return nil, fmt.Errorf("sftp: handshake: %w", err)
	}
	return &session{ssh: client, client: sc, act: act}, nil
}

// work is what one operation is to its watch.
type work struct {
	// repeatable: the operation may be sent again after its session broke -
	// it changes nothing on the server, or doing it twice is harmless.
	repeatable bool
	// sends: an upload; the server taking its bytes is a sign of life, and
	// it may go stall.SendStall without one.
	sends bool
}

var (
	readWork   = work{repeatable: true}
	changeWork = work{}
	sendWork   = work{sends: true}
)

// run does op on the session, under the policy.
func (d *Driver) run(ctx context.Context, w work, op func(*sftp.Client) error) error {
	return d.runOn(ctx, w, func(s *session) error { return op(s.client) })
}

// runOn is run for an op that needs the session itself (a download keeps
// it, to watch its reads on).
func (d *Driver) runOn(ctx context.Context, w work, op func(*session) error) error {
	clock, err := d.policy.Do(ctx, d.retryable(w.repeatable), func(ctx context.Context) error {
		s, err := d.current(ctx)
		if err != nil {
			return &connectError{err}
		}
		return d.do(ctx, s, w, op)
	})
	if err == nil || ctx.Err() != nil {
		return err
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

// do runs op under one watch. The caller leaves when its context ends; op
// finishes on its own, and its watch cuts a server that stays silent.
func (d *Driver) do(ctx context.Context, s *session, w work, op func(*session) error) error {
	limit, kind := d.policy.AttemptTimeout, stall.Silent
	if w.sends {
		limit, kind = d.policy.SendStall(), stall.Send
	}
	watch := s.act.Watch(limit, w.sends)
	done := make(chan error, 1)
	go func() { done <- op(s) }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		go func() {
			if err := <-done; watch.Stop() || isTransport(err) {
				d.drop(s)
			}
		}()
		return ctx.Err()
	}
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
		if errors.As(err, &rf) || stall.RefusedByTLS(err) {
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
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, sftp.ErrSSHFxConnectionLost) {
		return true
	}
	var se *stall.Error
	if errors.As(err, &se) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "use of closed") || strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection lost")
}

// refusedBySSH: the server answered, and the answer was no - a password or a
// key it does not accept, a host key that does not match.
func refusedBySSH(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "unable to authenticate") || strings.Contains(msg, "knownhosts") ||
		strings.Contains(msg, "host key")
}

// mapErr turns an SFTP answer into filex's vocabulary.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
		return storage.ErrNotFound
	}
	return err
}

// connectError is a failure to connect, shake hands or log in - nothing of
// the operation had started, so it may be tried again.
type connectError struct{ err error }

func (e *connectError) Error() string { return e.err.Error() }
func (e *connectError) Unwrap() error { return e.err }

// refusal is the server saying no (a wrong password, a host key that does not
// match): never retried, never "unavailable".
type refusal struct{ err error }

func (e *refusal) Error() string { return e.err.Error() }
func (e *refusal) Unwrap() error { return e.err }

// errDetached is what an abandoned upload reads from the caller's stream
// after the caller left.
var errDetached = errors.New("sftp: the upload was abandoned by its caller")

// gateReader hands an upload the caller's stream until the caller leaves
// (shut); after that it never touches the stream again, so a call that
// returned early on its context cannot race the caller over its own reader.
type gateReader struct {
	mu     sync.Mutex
	r      io.Reader
	closed bool
}

func (g *gateReader) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return 0, errDetached
	}
	return g.r.Read(p)
}

// shut waits for a Read in progress, then detaches the stream.
func (g *gateReader) shut() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// fileReader is a download: every Read is watched (each next piece of the
// file must arrive within the attempt timeout), and only while it waits on the
// network, so a caller that reads slowly is never cut.
type fileReader struct {
	d     *Driver
	s     *session
	f     *sftp.File
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
	return n, err
}

// Close closes the remote file under a watch too: CLOSE is a request that a
// silent server never answers.
func (r *fileReader) Close() error {
	w := r.s.act.Watch(r.limit, false)
	err := r.f.Close()
	if w.Stop() {
		r.d.drop(r.s)
	}
	return err
}
