package ftp

// How long the driver waits for a server that does not answer (issue #73).
//
// Before, only the TCP connect was bounded (15 s). Once connected nothing
// was: a server that stopped answering left the liveness probe (NOOP) in
// connectLocked waiting forever while it held the driver's lock, so every
// other operation on the storage queued behind it, forever too; a server that
// accepted the connection and never greeted did the same, and so did an
// upload or a download the server stopped moving.
//
// Now the S3 driver's pattern (issue #44) from internal/storage/stall, with
// the three settings in the descriptor:
//
//   - attempt_timeout_s bounds every wait for the server: connecting, each
//     read on the control connection (the greeting, the answer to each
//     command, the probe) and each read of a download. Every connection the
//     session opens, control and data, is a stall.Conn, so the limit renews
//     on every byte: a transfer that keeps moving is never cut. A send that
//     stops moving is cut after a minute (stall.SendStallFloor).
//   - max_attempts and total_timeout_s: a connection that could not be made
//     (refused, silent, dropped, the server busy with 421) is tried again
//     within the budget, and so is an operation that changes nothing (a
//     listing, a stat, a download, a mkdir) whose session broke. An upload,
//     a rename or a delete that reached the server is not sent again.
//   - The lock is a channel, not a mutex, so a caller whose context ends
//     while it waits for the storage leaves at once; while an operation runs,
//     its context ending closes the control connection, which ends it.
//
// ⚠ The "226" that answers an upload may come only when the server has read
// what was still in the sockets' buffers when the upload counted as sent —
// seconds, against a server that reads slowly. stor gives that read the
// send tail (stall.SendTail), as the S3 driver gives its answer wait.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"

	goftp "github.com/jlaffaye/ftp"

	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// defaults of the three settings: 15 s is what the connect alone was
// allowed before.
var defaults = stall.Defaults{AttemptTimeoutS: 15, MaxAttempts: 3, TotalTimeoutS: 15}

func (d *Driver) addr() string { return net.JoinHostPort(d.host, strconv.Itoa(d.port)) }

// lock takes the storage's one session, or gives up when ctx ends first.
func (d *Driver) lock(ctx context.Context) error {
	d.semOnce.Do(func() { d.sem = make(chan struct{}, 1) })
	select {
	case d.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Driver) unlock() { <-d.sem }

// run does op on the session under the lock and the policy.
//
// repeatable says op may be sent again after its session broke: it changes
// nothing on the server (a listing), or doing it twice is harmless (a
// mkdir). A connection that could not be made is always tried again — op had
// not started.
func (d *Driver) run(ctx context.Context, repeatable bool, op func(*goftp.ServerConn) error) error {
	if err := d.lock(ctx); err != nil {
		return err
	}
	defer d.unlock()
	return d.runLocked(ctx, repeatable, op)
}

func (d *Driver) runLocked(ctx context.Context, repeatable bool, op func(*goftp.ServerConn) error) error {
	clock, err := d.policy.Do(ctx, d.retryable(repeatable), func(ctx context.Context) error {
		c, err := d.connectLocked(ctx)
		if err != nil {
			return err
		}
		if ctrl := d.ctrl; ctrl != nil {
			stop := context.AfterFunc(ctx, func() { stall.CloseRaw(ctrl) })
			defer stop()
		}
		if err := op(c); err != nil {
			if isTransport(err) {
				d.dropConnLocked()
			}
			return closedIfEOF(err)
		}
		return nil
	})
	if err == nil || ctx.Err() != nil {
		return err
	}
	if isTransport(err) {
		return d.policy.Unavailable(d.what, clock, err)
	}
	var ce *connectError
	if errors.As(err, &ce) {
		return err // a refused login keeps the server's words
	}
	return translateErr(err)
}

// connectLocked returns the session, dialing it when there is none or the
// cached one does not answer. Caller MUST hold the lock.
func (d *Driver) connectLocked(ctx context.Context) (*goftp.ServerConn, error) {
	if d.conn != nil {
		// Cheap liveness probe — one round trip. ⚠ It used to wait forever on
		// a server that stopped answering, holding the lock; the control
		// connection's read limit now ends it within the attempt timeout, and
		// a fresh connection is tried (an idle one a NAT dropped is common).
		if err := d.conn.NoOp(); err == nil {
			return d.conn, nil
		}
		d.dropConnLocked()
	}
	c, ctrl, err := d.dial(ctx)
	if err != nil {
		return nil, &connectError{err: closedIfEOF(err)}
	}
	d.conn, d.ctrl = c, ctrl
	return c, nil
}

// dial connects and logs in. Every connection of the session — the control
// connection and, later, each data connection — goes through dialConn, so
// each is a stall.Conn.
func (d *Driver) dial(ctx context.Context) (*goftp.ServerConn, *stall.Conn, error) {
	var ctrl *stall.Conn
	dialConn := func(network, addr string) (net.Conn, error) {
		dctx := context.Background() // a data connection, dialled later under another operation
		if ctrl == nil {
			dctx = ctx
		}
		nd := net.Dialer{Timeout: d.policy.AttemptTimeout}
		raw, err := nd.DialContext(dctx, network, addr)
		if err != nil {
			return nil, err
		}
		g := stall.Guard(raw, d.policy.AttemptTimeout, d.policy.SendStall())
		if ctrl == nil {
			ctrl = g // the library upgrades it with AUTH TLS itself
			return g, nil
		}
		// ⚠ With a dial function the library (v0.2.0) hands data
		// connections back as they come, TLS or not: an FTPS session would
		// send its files in the clear.
		if d.tls {
			return tls.Client(g, d.tlsConfig()), nil
		}
		return g, nil
	}
	opts := []goftp.DialOption{
		goftp.DialWithDialFunc(dialConn),
		goftp.DialWithDisabledEPSV(!d.passive),
	}
	if d.tls {
		opts = append(opts, goftp.DialWithExplicitTLS(d.tlsConfig()))
	}
	c, err := goftp.Dial(d.addr(), opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("ftp: dial: %w", err)
	}
	if err := c.Login(d.user, d.password); err != nil {
		_ = c.Quit()
		return nil, nil, fmt.Errorf("ftp: login: %w", err)
	}
	return c, ctrl, nil
}

func (d *Driver) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: d.host, RootCAs: testRootCAs}
}

// testRootCAs lets a test trust its own FTPS stand-in. nil in production: the
// system's roots.
var testRootCAs *x509.CertPool

// stor uploads r, and lets the "226" that answers it wait the send tail (see
// the file comment).
func (d *Driver) stor(c *goftp.ServerConn, abs string, r io.Reader) error {
	return c.Stor(abs, &tailReader{r: r, ctrl: d.ctrl})
}

// tailReader counts an upload and, when it ends, gives the control
// connection's next read the time the server needs for what is still
// buffered.
type tailReader struct {
	r    io.Reader
	ctrl *stall.Conn
	n    int64
}

func (t *tailReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	t.n += int64(n)
	if err == io.EOF && t.ctrl != nil {
		t.ctrl.AllowOnce(stall.SendTail(t.n))
	}
	return n, err
}

// retryable says whether a failed attempt may be tried again (see run).
func (d *Driver) retryable(repeatable bool) func(error) bool {
	return func(err error) bool {
		if stall.RefusedByTLS(err) {
			return false
		}
		var dns *net.DNSError
		if errors.As(err, &dns) && dns.IsNotFound {
			return false
		}
		var ce *connectError
		if errors.As(err, &ce) || repeatable {
			return isTransport(err) || busy(err)
		}
		return false
	}
}

// busy: "421 Service not available" — too many users, going down for a
// moment. Worth asking again; a 530 (wrong password) is not.
func busy(err error) bool {
	var pe *textproto.Error
	return errors.As(err, &pe) && pe.Code == 421
}

// closedIfEOF: an EOF on the control connection is the server hanging up.
func closedIfEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return stall.Closed(err)
	}
	return err
}

// connectError is a failure to connect or log in — nothing of the
// operation had started, so it may always be tried again.
type connectError struct{ err error }

func (e *connectError) Error() string { return e.err.Error() }
func (e *connectError) Unwrap() error { return e.err }
