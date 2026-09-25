package stall

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// Reason decides whether err means the store is down — as opposed to a
// refusal (403, a wrong password, a missing folder, a bad certificate), which
// is the store answering — and says what happened in words a person can act
// on, naming the limit that bounded it.
func (p Policy) Reason(err error) (string, bool) {
	t := p.AttemptTimeout
	var dns *net.DNSError
	if errors.As(err, &dns) {
		switch {
		case dns.IsNotFound:
			return fmt.Sprintf("the host name %s does not resolve", dns.Name), true
		case dns.IsTimeout:
			return fmt.Sprintf("the name lookup got no answer within %s", t), true
		}
		return "the name lookup failed", true
	}
	// ⚠ Before the stall errors: an FTP upload the server stopped taking fails
	// twice — the send times out, then the "226" that never comes — and the
	// send is what happened.
	var nop *net.OpError
	if errors.As(err, &nop) && nop.Op == "write" && nop.Timeout() {
		return fmt.Sprintf("the store stopped taking the upload for %s", p.SendStall()), true
	}
	var stall *Error
	if errors.As(err, &stall) {
		return stall.What(), true
	}
	var closed *closedError
	if errors.As(err, &closed) {
		return "the server closed the connection", true
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "TLS handshake timeout"):
		return fmt.Sprintf("the TLS handshake did not finish within %s", t), true
	// ⚠ Both wordings: Unix says "connection refused", Windows (a filex
	// server on Windows, measured in the admin form's "Test connection") says
	// "…the target machine actively refused it", and the syscall package has no
	// WSAECONNREFUSED to compare with.
	case strings.Contains(msg, "connection refused"), strings.Contains(msg, "actively refused"):
		return "the connection was refused", true
	case strings.Contains(msg, "connection reset"), strings.Contains(msg, "forcibly closed"):
		return "the connection was reset", true
	case strings.Contains(msg, "no route to host"), strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "unreachable host"), strings.Contains(msg, "unreachable network"):
		return "there is no route to the host", true
	}
	if errors.As(err, &nop) {
		switch {
		case nop.Op == "dial" && nop.Timeout():
			return fmt.Sprintf("no answer to the connection attempt within %s", t), true
		case nop.Op == "dial":
			return "the connection could not be made", true
		}
		return "the connection failed", true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "the connection closed in the middle of the answer", true
	}
	var status interface{ HTTPStatusCode() int }
	if errors.As(err, &status) && status.HTTPStatusCode() >= 500 {
		code := status.HTTPStatusCode()
		return fmt.Sprintf("the store answered %d %s", code, http.StatusText(code)), true
	}
	var timeout interface{ Timeout() bool }
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "the request timed out", true
	}
	return "", false
}

// Closed marks err (an io.EOF on a command connection) as the server having
// closed the connection. Not every EOF means that — a body that ends early is
// read as EOF too, which is why Reason does not take a bare EOF for it — so
// the driver that knows says so.
func Closed(err error) error {
	if err == nil {
		return nil
	}
	return &closedError{err: err}
}

type closedError struct{ err error }

func (e *closedError) Error() string { return e.err.Error() }
func (e *closedError) Unwrap() error { return e.err }

// Unavailable wraps err as an UnavailableError when it means the store is
// down, and returns it unchanged otherwise. what names the store ("webdav
// server https://dav.example.com"); c is the operation's clock.
func (p Policy) Unavailable(what string, c *Clock, err error) error {
	if err == nil {
		return nil
	}
	reason, down := p.Reason(err)
	if !down {
		return err
	}
	return &UnavailableError{What: what, Reason: reason, Attempts: max(c.Attempts, 1), Took: time.Since(c.Began), Err: err}
}

// UnavailableError is an operation that failed because the store could not be
// reached or would not answer. It says so first, then the underlying text:
//
//	webdav server https://dav.example.com is unavailable: the connection was refused (3 attempts in 14.8s): …
//
// It matches storage.ErrUnavailable, which the HTTP layer answers with 503.
type UnavailableError struct {
	What     string
	Reason   string
	Attempts int
	Took     time.Duration
	// Err is the failure, for errors.Is/As. Cause, when set, is what the
	// message prints after the colon instead (the S3 driver strips its own
	// wrapper there).
	Err   error
	Cause error
}

func (e *UnavailableError) Error() string {
	cause := e.Cause
	if cause == nil {
		cause = e.Err
	}
	if s, ok := cause.(*Error); ok && s.Err != nil {
		cause = s.Err // its words are already the reason
	}
	unit := "attempts"
	if e.Attempts == 1 {
		unit = "attempt"
	}
	return fmt.Sprintf("%s is unavailable: %s (%d %s in %.1fs): %v",
		e.What, e.Reason, e.Attempts, unit, e.Took.Seconds(), cause)
}
func (e *UnavailableError) Unwrap() error        { return e.Err }
func (e *UnavailableError) Is(target error) bool { return target == storage.ErrUnavailable }
