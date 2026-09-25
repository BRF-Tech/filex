package webdav

// How long the driver waits for a server that does not answer (issue #73).
//
// It used to be one number: http.Client{Timeout: 60 * time.Second}, which
// bounds the WHOLE request, body included. Every upload and download that
// took longer than a minute was cut — a big file, a slow line — however well
// it was moving, and a server that was down took up to that minute to say so,
// in words that were not storage.ErrUnavailable.
//
// Now the S3 driver's pattern (issue #44), from the shared internal/storage/
// stall package, with the three settings in the descriptor:
//
//   - attempt_timeout_s bounds each wait for a sign of life: connecting (the
//     name lookup included), the TLS handshake, the answer once the request is
//     sent (stall.AnswerWatch), and each next piece of the answer
//     (stall.Reader). A transfer that keeps moving is never cut. An upload the
//     server stops taking is cut after a minute (stall.SendStallFloor).
//   - max_attempts and total_timeout_s: a request that failed in a way that
//     can pass is tried again within the budget (stall.Policy.Do).
//
// ⚠ Server-side work is not silence. A WebDAV server answers COPY, MOVE (every
// rename) and DELETE only when it has copied, moved or deleted the whole tree,
// which for a large folder on Nextcloud takes minutes. Those wait up to
// serverWorkTimeout for the answer.
//
// ⚠ What is sent again. A request that never reached the server (the
// connection was refused, the connect or the TLS handshake timed out) always
// is. An upload is never sent again once any of it was read: its body is the
// caller's stream, and it cannot be read twice. A request that reached the
// server and got no answer, or a broken one, is sent again only when it
// changes nothing (PROPFIND, GET, HEAD) — a MOVE that timed out may have
// happened. A 502, 503 or 504 is the server saying it did not take the request,
// so it is sent again (an upload only if nothing of it was read).
//
// ⚠ HTTP/1.1 only. A silent answer is cut by closing its connection, which on
// HTTP/2 would also cut every other request multiplexed on it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage/stall"
)

// defaults of the three settings. The attempt timeout is longer than S3's 10
// s: a PROPFIND of a large folder on Nextcloud can take well over 10 s to start
// answering, and the old client allowed 60 s for everything.
var defaults = stall.Defaults{AttemptTimeoutS: 30, MaxAttempts: 3, TotalTimeoutS: 15}

// serverWorkTimeout: a variable so a test can lower it.
var serverWorkTimeout = 10 * time.Minute

// serverWork are the methods the server answers only when the work is done.
var serverWork = map[string]bool{"COPY": true, "MOVE": true, http.MethodDelete: true}

// changesNothing are the methods that may be sent again after they reached
// the server.
var changesNothing = map[string]bool{"PROPFIND": true, http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true}

// transport: connecting and the TLS handshake bounded by the attempt timeout,
// a send that stopped moving by SendStall; the answer is watched per attempt.
func (d *Driver) transport() *http.Transport {
	dialer := &net.Dialer{Timeout: d.policy.AttemptTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           stall.GuardWrites(dialer.DialContext, d.policy.SendStall()),
		TLSHandshakeTimeout:   d.policy.AttemptTimeout,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// answerLimit is how long a request may wait for the answer once it is sent,
// and for each next piece of it.
func (d *Driver) answerLimit(method string) time.Duration {
	if serverWork[method] {
		return max(serverWorkTimeout, d.policy.AttemptTimeout)
	}
	return d.policy.AttemptTimeout
}

// call is one WebDAV request.
type call struct {
	method string
	path   string
	header http.Header
	// xml is a small body, sent again on every attempt (PROPFIND).
	xml string
	// stream is an upload body: the caller's reader, which can be read once.
	// size is its declared length, 0 or less when unknown.
	stream io.Reader
	size   int64
}

func propfind(p, depth string) call {
	return call{method: "PROPFIND", path: p, xml: propfindBody, header: http.Header{
		"Depth":        {depth},
		"Content-Type": {`application/xml; charset="utf-8"`},
	}}
}

func (d *Driver) destination(dst string) http.Header {
	return http.Header{"Destination": {d.urlFor(dst)}, "Overwrite": {"T"}}
}

// sendSize is the body length the answer wait allows a send tail for. An
// upload of unknown length gets the longest tail.
func (c call) sendSize() int64 {
	switch {
	case c.stream != nil && c.size > 0:
		return c.size
	case c.stream != nil:
		return math.MaxInt64
	}
	return int64(len(c.xml))
}

// exchange sends c under the policy. With slurp the answer body is read
// within the attempt (a listing), so a body that stalls is retried like an
// answer that never started; without it the open body is returned (a
// download), still bounded for silence.
//
// A server that is down is reported as a stall.UnavailableError, which
// matches storage.ErrUnavailable. A caller that gave up gets its own error.
func (d *Driver) exchange(ctx context.Context, c call, slurp bool) (*http.Response, error) {
	sent := &countingReader{r: c.stream}
	var resp *http.Response
	clock, err := d.policy.Do(ctx, func(err error) bool { return d.retryable(c, sent, err) }, func(ctx context.Context) error {
		r, err := d.attempt(ctx, c, sent, slurp)
		resp = r
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, err
		}
		return nil, d.policy.Unavailable(d.what, clock, err)
	}
	return resp, nil
}

func (d *Driver) attempt(ctx context.Context, c call, sent *countingReader, slurp bool) (*http.Response, error) {
	var body io.Reader
	switch {
	case c.xml != "":
		body = strings.NewReader(c.xml)
	case c.stream != nil:
		body = sent
	}
	req, err := http.NewRequestWithContext(ctx, c.method, d.urlFor(c.path), body)
	if err != nil {
		return nil, err
	}
	for k, v := range c.header {
		req.Header[k] = v
	}
	req.SetBasicAuth(d.user, d.pass)

	limit := d.answerLimit(c.method)
	w := stall.NewAnswerWatch(limit + stall.SendTail(c.sendSize()))
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), w.Trace()))
	resp, err := d.client.Do(req)
	w.Stop() // the answer has started: from here on its body is watched
	if err != nil {
		if w.Fired() {
			err = &stall.Error{Kind: stall.Answer, Limit: w.Limit(), Err: err}
		}
		return nil, err
	}
	resp.Body = stall.NewReader(resp.Body, limit)
	switch resp.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		return nil, &statusError{method: c.method, code: resp.StatusCode}
	}
	if slurp {
		data, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		resp.Body = io.NopCloser(strings.NewReader(string(data)))
	}
	return resp, nil
}

// retryable says whether a failed attempt may be sent again (the file
// comment says why each case is what it is).
func (d *Driver) retryable(c call, sent *countingReader, err error) bool {
	if stall.RefusedByTLS(err) {
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) && dns.IsNotFound {
		return false
	}
	if c.stream != nil {
		// ⚠ Only before any byte was read: after an early answer the
		// transport may still be reading the body in the background, and a
		// second attempt would read the same stream at the same time.
		return notSent(err) && sent.n.Load() == 0
	}
	var status *statusError
	if notSent(err) || errors.As(err, &status) {
		return true
	}
	return changesNothing[c.method]
}

// notSent: the request never reached the server — no connection, no TLS.
func notSent(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	return strings.Contains(err.Error(), "TLS handshake timeout")
}

// statusError is a server that answered it could not take the request now
// (502, 503, 504). stall.Policy.Reason reads its code.
type statusError struct {
	method string
	code   int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("webdav: %s answered %d %s", strings.ToLower(e.method), e.code, http.StatusText(e.code))
}
func (e *statusError) HTTPStatusCode() int { return e.code }

// countingReader counts what the transport read of an upload.
type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}
