package assistant

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProviderError is a provider's refusal or failure, said without its key.
type ProviderError struct {
	Provider Kind
	// Status is the HTTP status the provider answered; 0 when it never
	// answered (the address did not answer, the stream broke).
	Status int
	// Message is the provider's own words (English, as it sent them).
	Message string
}

func (e *ProviderError) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("%s answered %d: %s", e.Provider, e.Status, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Provider, e.Message)
}

// Unauthorized reports whether the provider refused the key.
func (e *ProviderError) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// transport is the HTTP every driver shares: the key, the retries, the
// redaction.
type transport struct {
	client  *http.Client
	key     string
	retries int
	// sleep waits between retries (time.Sleep that ends with ctx; replaced
	// in tests).
	sleep func(ctx context.Context, d time.Duration) error
}

// redact takes the key out of s. A provider that echoes a request back in
// its error (some gateways do) must not put the key into a log line or an
// answer through us.
func (t *transport) redact(s string) string {
	if k := strings.TrimSpace(t.key); len(k) >= 4 {
		s = strings.ReplaceAll(s, k, "***")
	}
	return s
}

func (t *transport) wait(ctx context.Context, d time.Duration) error {
	if t.sleep != nil {
		return t.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// retryable is a status worth asking again: rate limited, overloaded, or a
// gateway that lost the upstream for a moment. 529 is Anthropic's
// "overloaded".
func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	}
	return false
}

// retryAfter is how long the provider asked us to wait (Retry-After in
// seconds or as a date), at most 30 s; the backoff when it said nothing.
func retryAfter(h http.Header, attempt int) time.Duration {
	const most = 30 * time.Second
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			if d := time.Duration(n) * time.Second; d < most {
				return d
			}
			return most
		}
		if at, err := http.ParseTime(v); err == nil {
			if d := time.Until(at); d > 0 && d < most {
				return d
			}
			return most
		}
	}
	return time.Duration(1<<attempt) * 500 * time.Millisecond
}

// do sends one request built by build, asking again on a retryable status or
// a connection that failed - but only before any of the answer was read, so a
// retry never doubles text the person already saw. kind names the provider in
// the errors. A non-2xx answer that is not retried, or still fails after the
// retries, is a *ProviderError.
func (t *transport) do(ctx context.Context, kind Kind, build func() (*http.Request, error)) (*http.Response, error) {
	var last error
	for attempt := 0; attempt <= t.retries; attempt++ {
		req, err := build()
		if err != nil {
			return nil, err
		}
		resp, err := t.client.Do(req.WithContext(ctx))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = &ProviderError{Provider: kind, Message: t.redact(err.Error())}
			if attempt < t.retries {
				if werr := t.wait(ctx, retryAfter(nil, attempt)); werr != nil {
					return nil, werr
				}
			}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		perr := t.answerError(kind, resp)
		if !retryable(resp.StatusCode) || attempt == t.retries {
			return nil, perr
		}
		last = perr
		if werr := t.wait(ctx, retryAfter(resp.Header, attempt)); werr != nil {
			return nil, werr
		}
	}
	return nil, last
}

// answerError reads a refusal's words: `{"error": {"message": …}}` (OpenAI,
// Anthropic), `{"error": "…"}` (Ollama), or the first 300 bytes as they came.
func (t *transport) answerError(kind Kind, resp *http.Response) *ProviderError {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	return &ProviderError{Provider: kind, Status: resp.StatusCode, Message: t.redact(errorWords(raw))}
}

// errorWords is the message inside a provider's error body.
func errorWords(raw []byte) string {
	var obj struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		var s string
		if json.Unmarshal(obj.Error, &s) == nil && s != "" {
			return s
		}
		var inner struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		}
		if json.Unmarshal(obj.Error, &inner) == nil && inner.Message != "" {
			return inner.Message
		}
		if obj.Message != "" {
			return obj.Message
		}
	}
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300]
	}
	if s == "" {
		s = "no message"
	}
	return s
}

// postJSON builds a POST of body to url with headers.
func postJSON(url string, headers map[string]string, body any) func() (*http.Request, error) {
	b, err := json.Marshal(body)
	return func() (*http.Request, error) {
		if err != nil {
			return nil, err
		}
		req, rerr := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
		if rerr != nil {
			return nil, rerr
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}
}

// getURL builds a GET of url with headers.
func getURL(url string, headers map[string]string) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		return req, nil
	}
}

// getJSON GETs url and decodes the answer into out.
func (t *transport) getJSON(ctx context.Context, kind Kind, url string, headers map[string]string, out any) error {
	resp, err := t.do(ctx, kind, getURL(url, headers))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out); err != nil {
		return &ProviderError{Provider: kind, Message: "the answer is not the provider's JSON: " + t.redact(err.Error())}
	}
	return nil
}

// maxLine is the longest line a stream may send: a tool call's arguments
// can be a whole file the model writes.
const maxLine = 16 << 20

// errLineTooLong is a stream line past maxLine.
var errLineTooLong = errors.New("a line of the stream is longer than 16 MiB")

// readLines calls fn with every line of r, without its line end.
func readLines(r io.Reader, fn func(line string) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	var buf []byte
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(chunk) > 0 || (err == nil && !isPrefix) {
			buf = append(buf, chunk...)
			if len(buf) > maxLine {
				return errLineTooLong
			}
		}
		if err != nil {
			if err == io.EOF {
				if len(buf) > 0 {
					return fn(string(buf))
				}
				return nil
			}
			return err
		}
		if isPrefix {
			continue
		}
		if ferr := fn(string(buf)); ferr != nil {
			return ferr
		}
		buf = buf[:0]
	}
}

// errStop ends a stream read early without it being a failure.
var errStop = errors.New("stop")

// readSSE calls fn with every server-sent event of r: its `event:` name
// (empty when it has none) and its `data:` lines joined with newlines.
func readSSE(r io.Reader, fn func(event, data string) error) error {
	var event string
	var data []string
	flush := func() error {
		if len(data) == 0 {
			event = ""
			return nil
		}
		err := fn(event, strings.Join(data, "\n"))
		event, data = "", nil
		return err
	}
	err := readLines(r, func(line string) error {
		switch {
		case line == "":
			return flush()
		case strings.HasPrefix(line, ":"):
			return nil
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		return nil
	})
	if err == nil {
		err = flush()
	}
	if errors.Is(err, errStop) {
		return nil
	}
	return err
}

// streamError is a stream that broke after it started: said without the key.
func (t *transport) streamError(kind Kind, err error) error {
	if err == nil {
		return nil
	}
	var perr *ProviderError
	if errors.As(err, &perr) {
		return perr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &ProviderError{Provider: kind, Message: "the stream broke: " + t.redact(err.Error())}
}
