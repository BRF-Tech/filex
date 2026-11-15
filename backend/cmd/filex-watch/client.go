package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	mrand "math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/brf-tech/filex/backend/internal/watchproto"
)

// client sends reports to filex's door.
type client struct {
	url  string
	key  string
	http *http.Client
}

func newClient(cfg *Config) (*client, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.InsecureSkipTLSVerify {
		// The operator's explicit choice, for a self-signed certificate; the
		// agent says so in its log at start.
		tlsCfg.InsecureSkipVerify = true //nolint:gosec
	}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read ca_file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("ca_file %s holds no PEM certificate", cfg.CAFile)
		}
		tlsCfg.RootCAs = pool
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = tlsCfg
	return &client{
		url:  cfg.Server + watchproto.EventsPath(cfg.Storage),
		key:  cfg.key,
		http: &http.Client{Transport: tr, Timeout: 30 * time.Second},
	}, nil
}

// sendError is a report filex did not take, and what to do about it.
type sendError struct {
	// Status is the HTTP status (0: no answer at all).
	Status int
	// Code and Message are filex's (the apierr envelope); Err the transport's.
	Code, Message string
	Err           error
	// RetryAfter is what a 429 asked for.
	RetryAfter time.Duration
}

func (e *sendError) Error() string {
	switch {
	case e.Err != nil:
		return "filex could not be reached: " + e.Err.Error()
	case e.Message != "":
		return fmt.Sprintf("filex answered %d %s: %s", e.Status, e.Code, e.Message)
	default:
		return fmt.Sprintf("filex answered %d", e.Status)
	}
}

func (e *sendError) Unwrap() error { return e.Err }

// transport reports a failure in which filex may have stopped (no answer, a
// 5xx): whatever it held in memory may be gone, so the next report asks for a
// resync.
func (e *sendError) transport() bool { return e.Status == 0 || e.Status >= 500 }

// badReport reports a refusal of THIS report (400, 413): sending it again
// cannot help. The agent drops it and asks for a resync instead.
func (e *sendError) badReport() bool {
	return e.Status == http.StatusBadRequest || e.Status == http.StatusRequestEntityTooLarge
}

// send posts one report and reads filex's answer.
func (c *client) send(ctx context.Context, rep watchproto.Report) (watchproto.Answer, error) {
	var ans watchproto.Answer
	body, err := json.Marshal(rep)
	if err != nil {
		return ans, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return ans, &sendError{Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("User-Agent", "filex-watch/"+version)
	res, err := c.http.Do(req)
	if err != nil {
		return ans, &sendError{Err: err}
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	if res.StatusCode == http.StatusAccepted || res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &ans); err != nil {
			return ans, &sendError{Status: res.StatusCode, Err: fmt.Errorf("an answer that is not a report answer: %w", err)}
		}
		return ans, nil
	}
	se := &sendError{Status: res.StatusCode}
	var env struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &env) == nil {
		se.Code, se.Message = env.Error, env.Message
	}
	if s := res.Header.Get("Retry-After"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			se.RetryAfter = time.Duration(n) * time.Second
		}
	}
	return ans, se
}

// asSendError unwraps a send's error.
func asSendError(err error) *sendError {
	var se *sendError
	if errors.As(err, &se) {
		return se
	}
	return &sendError{Err: err}
}

// Backoff of a failed send: doubling from backoffMin to backoffMax, give or
// take a fifth so many agents of one filex do not come back in step.
const (
	backoffMin = time.Second
	backoffMax = 5 * time.Minute
)

// nextBackoff is the wait after one more failure, prev being the last wait
// (0 after a success).
func nextBackoff(prev time.Duration) time.Duration {
	if prev < backoffMin {
		return backoffMin
	}
	return min(prev*2, backoffMax)
}

// jitter spreads d by up to a fifth either way.
func jitter(d time.Duration) time.Duration {
	f := 0.8 + 0.4*mrand.Float64()
	return time.Duration(math.Round(float64(d) * f))
}

// agentInfo is what the agent says about itself in every report.
func agentInfo(cfg *Config) watchproto.Agent {
	host, _ := os.Hostname()
	return watchproto.Agent{
		ID:      agentID,
		Version: version,
		Host:    clip(host),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		Root:    clip(cfg.Root),
	}
}

// agentID is this process's own name: a new process is a new agent.
var agentID = func() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}()

// clip keeps a field within what a report may carry.
func clip(s string) string {
	if len(s) <= watchproto.MaxAgentField {
		return s
	}
	cut := watchproto.MaxAgentField
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
