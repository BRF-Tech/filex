package cliclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

// Client is a thin REST client for one filex server. Token may be either
// a session token minted by /api/auth/login or a durable API token — the
// server accepts both on the Authorization: Bearer header.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client

	// StagedThreshold is the file size at or above which uploads use the
	// resumable staged protocol instead of one multipart POST. 0 means the
	// default (DefaultStagedThreshold); a negative value disables the staged
	// path, which is how a caller pins the old behaviour.
	StagedThreshold int64
	// ChunkSize is the part size asked for at `begin`. 0 lets the server
	// choose — and the server's answer is binding either way.
	ChunkSize int64
	// ResumeDir holds the bookmarks that let an interrupted upload continue
	// across process restarts. Empty disables persistence: uploads still
	// resume within a run, but a restart begins the file again.
	ResumeDir string
}

// newHTTPClient is an http.Client that cannot hang forever on a half-dead
// connection. Everything here exists because of one measured failure: four
// parallel downloads sharing one HTTP/2 connection through a CDN proxy froze
// mid-first-sync when the connection died silently — Go's http2 sends no
// health pings by default, so every stream blocked until someone killed the
// process. ReadIdleTimeout is that ping. The other limits bound the steps of
// a request that may only legitimately take long in its BODY (a big
// transfer), never in dialing or waiting for headers. There is still no
// whole-request timeout: bodies stream arbitrarily large files.
func newHTTPClient() *http.Client {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	if h2, err := http2.ConfigureTransports(tr); err == nil {
		h2.ReadIdleTimeout = 30 * time.Second
		h2.PingTimeout = 15 * time.Second
	}
	return &http.Client{Transport: tr}
}

// New builds a Client from a resolved Conn. No global timeout is set —
// uploads/downloads stream arbitrarily large bodies; cancellation is the
// caller's context (Ctrl-C in the CLI).
func New(conn Conn) *Client {
	c := &Client{
		BaseURL: strings.TrimRight(conn.URL, "/"),
		Token:   conn.Token,
		HTTP:    newHTTPClient(),
	}
	// A missing home directory is not a reason to refuse to upload; it only
	// costs cross-restart resume, and the error would be reported at a point
	// that has nothing to do with what the user asked for.
	if dir, err := DefaultResumeDir(); err == nil {
		c.ResumeDir = dir
	}
	return c
}

// APIError is a non-2xx response mapped to an error. Body keeps the raw
// payload so callers can inspect extra fields (e.g. totp_required).
type APIError struct {
	Status  int
	Message string
	Body    []byte
}

// Error renders "HTTP <code>: <server message>".
func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// IsUnauthorized reports whether err is an APIError with status 401 —
// the CLI uses it to append the "run `filex client login`" hint.
func IsUnauthorized(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Status == http.StatusUnauthorized
}

// newRequest builds an authenticated request against BaseURL+p.
func (c *Client) newRequest(ctx context.Context, method, p string, q url.Values, body io.Reader) (*http.Request, error) {
	if c.BaseURL == "" {
		return nil, errors.New("no server URL configured (use --url, FILEX_URL, or run `filex client login`)")
	}
	u := c.BaseURL + p
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return req, nil
}

// doJSON executes the request and returns the raw response body. Non-2xx
// responses map to *APIError carrying the server's {"error": …} message.
func (c *Client) doJSON(req *http.Request) ([]byte, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, apiErrorFrom(resp.StatusCode, b)
	}
	return b, nil
}

// apiErrorFrom extracts the JSON error message when present, otherwise
// keeps a short plain-text excerpt of the body.
func apiErrorFrom(status int, body []byte) *APIError {
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &e)
	msg := e.Error
	if msg == "" {
		msg = strings.TrimSpace(string(body))
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
	}
	return &APIError{Status: status, Message: msg, Body: body}
}
