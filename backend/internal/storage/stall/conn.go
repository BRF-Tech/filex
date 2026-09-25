package stall

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"time"
)

// Conn bounds silence on one connection: a Read may wait at most read for the
// peer to send something, a Write at most write for the peer to take it.
//
// The deadline is set when the call starts, so it bounds a stall, never the
// length of a transfer, and a caller that is slow to call Read again (a
// browser reading a download slowly, a paused player) is never cut: the clock
// runs only while a call waits on the network.
//
// A zero limit leaves that direction unbounded.
type Conn struct {
	net.Conn
	read, write time.Duration
	extra       atomic.Int64 // AllowOnce, in nanoseconds
}

// Guard wraps c. read bounds each Read, write each Write.
func Guard(c net.Conn, read, write time.Duration) *Conn {
	return &Conn{Conn: c, read: read, write: write}
}

// AllowOnce lets the reads wait extra longer until one of them gets
// something. The answer to an upload may come only once the peer has read
// what was still in the sockets' buffers when the upload counted as sent
// (SendTail): FTP's "226" after a STOR against a server that reads slowly.
func (c *Conn) AllowOnce(extra time.Duration) { c.extra.Store(int64(extra)) }

func (c *Conn) Read(p []byte) (int, error) {
	limit := c.read + time.Duration(c.extra.Load())
	if c.read > 0 {
		if err := c.Conn.SetReadDeadline(time.Now().Add(limit)); err != nil {
			return 0, err
		}
	}
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.extra.Store(0)
	}
	if err != nil && c.read > 0 && errors.Is(err, os.ErrDeadlineExceeded) {
		err = &Error{Kind: Silent, Limit: limit, Err: err}
	}
	return n, err
}

func (c *Conn) Write(p []byte) (int, error) {
	if c.write > 0 {
		if err := c.Conn.SetWriteDeadline(time.Now().Add(c.write)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Write(p)
}

// DialFunc is net.Dialer.DialContext's shape.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// GuardWrites makes a send that stopped moving fail. http.Transport has no
// write timeout: a store that accepted the connection and then stopped
// reading left the upload blocked in Write for as long as TCP kept the peer
// alive.
//
// Only writes: an HTTP client's reads are watched per request (AnswerWatch
// and Reader), because a pooled connection sits in a read between requests.
func GuardWrites(dial DialFunc, limit time.Duration) DialFunc {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		c, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return Guard(c, 0, limit), nil
	}
}

// CloseRaw closes a connection under TLS without a close_notify, which would
// wait on a peer that is not answering.
func CloseRaw(c net.Conn) {
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	if c != nil {
		_ = c.Close()
	}
}

// RefusedByTLS: a certificate that does not verify, or a TLS endpoint that
// does not speak TLS, is a misconfiguration, not a network hiccup. Asking
// again cannot change the answer, so it is never retried.
func RefusedByTLS(err error) bool {
	var verify *tls.CertificateVerificationError
	var header tls.RecordHeaderError
	var authority x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &verify) || errors.As(err, &header) || errors.As(err, &authority) ||
		errors.As(err, &host) || errors.As(err, &invalid)
}
