package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/storage"
)

// Client speaks the protocol to ONE plugin endpoint. It is safe for
// concurrent use; the storage adapters share one per plugin.
type Client struct {
	// base is "http://plugin" for unix sockets (the host is a placeholder —
	// the dialer ignores it) or the real http(s)://host:port for TCP/remote.
	base  string
	token string
	http  *http.Client
}

// Address is a parsed plugin address: "unix:/path" or "tcp:host:port" from
// the handshake line, or a full http(s):// URL for a remote plugin.
type Address struct {
	Network string // "unix" | "tcp"
	Target  string // socket path, or host:port, or full URL for remote
	URL     string // base URL to put in front of /v1
}

// ParseAddress accepts the three spellings the host meets.
func ParseAddress(s string) (Address, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "unix:"):
		p := strings.TrimPrefix(s, "unix:")
		if p == "" {
			return Address{}, errors.New("plugin: empty unix socket path")
		}
		return Address{Network: "unix", Target: p, URL: "http://plugin"}, nil
	case strings.HasPrefix(s, "tcp:"):
		hp := strings.TrimPrefix(s, "tcp:")
		host, _, err := net.SplitHostPort(hp)
		if err != nil {
			return Address{}, fmt.Errorf("plugin: bad tcp address %q: %w", hp, err)
		}
		// A launched plugin must stay on loopback: the token is the only
		// thing between the world and the storage credentials filex sends
		// it, and a plugin that binds 0.0.0.0 by accident is a plugin that
		// leaks them to the LAN.
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return Address{}, fmt.Errorf("plugin: launched plugins must listen on loopback, got %q", hp)
		}
		return Address{Network: "tcp", Target: hp, URL: "http://" + hp}, nil
	case strings.HasPrefix(s, "http://"), strings.HasPrefix(s, "https://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return Address{}, fmt.Errorf("plugin: bad url %q", s)
		}
		u.Path = strings.TrimRight(u.Path, "/")
		u.RawQuery, u.Fragment = "", ""
		return Address{Network: "tcp", Target: u.Host, URL: u.String()}, nil
	}
	return Address{}, fmt.Errorf("plugin: unrecognised address %q (want unix:/path, tcp:127.0.0.1:port or http(s)://…)", s)
}

// NewClient builds a client for addr authenticating with token.
func NewClient(addr Address, token string) *Client {
	tr := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
		// Reads and writes stream whole objects; only the connect is bounded.
		ResponseHeaderTimeout: 60 * time.Second,
	}
	if addr.Network == "unix" {
		path := addr.Target
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
	} else {
		d := &net.Dialer{Timeout: 10 * time.Second}
		tr.DialContext = d.DialContext
	}
	return &Client{base: addr.URL, token: token, http: &http.Client{Transport: tr}}
}

// Close releases idle connections.
func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) newRequest(ctx context.Context, method, path string, q url.Values, body io.Reader) (*http.Request, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json, */*")
	return req, nil
}

// pluginError is a non-2xx answer, decoded.
type pluginError struct {
	Status  int
	Code    string
	Message string
}

func (e *pluginError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("plugin: %s (%s, http %d)", e.Message, e.Code, e.Status)
	}
	return fmt.Sprintf("plugin: %s (http %d)", e.Code, e.Status)
}

// mapErr turns a plugin error into the storage error callers already handle.
func mapErr(err error) error {
	var pe *pluginError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case ErrCodeNotFound:
		return storage.ErrNotFound
	case ErrCodeReadOnly:
		return storage.ErrReadOnly
	case ErrCodeUnsupported:
		return storage.ErrUnsupported
	}
	if pe.Status == http.StatusNotFound && pe.Code == "" {
		return storage.ErrNotFound
	}
	return err
}

// isNoInstance is the one error the adapter retries after re-initialising.
func isNoInstance(err error) bool {
	var pe *pluginError
	return errors.As(err, &pe) && pe.Code == ErrCodeNoInstance
}

func readError(resp *http.Response) error {
	pe := &pluginError{Status: resp.StatusCode}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var er ErrorResponse
	if json.Unmarshal(b, &er) == nil && er.Error != "" {
		pe.Code, pe.Message = er.Error, er.Message
	} else if s := strings.TrimSpace(string(b)); s != "" {
		pe.Message = s
	}
	return pe
}

// doJSON sends an optional JSON body and decodes a JSON answer into out
// (nil out discards it).
func (c *Client) doJSON(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, path, q, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("plugin: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return readError(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Describe is GET /v1/describe with the answer validated.
func (c *Client) Describe(ctx context.Context) (*DescribeResponse, error) {
	var d DescribeResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/describe", nil, nil, &d); err != nil {
		return nil, err
	}
	if err := ValidateDescribe(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ValidateDescribe applies the rules a plugin must meet before the host
// registers it. Every message names the rule, because the person reading it
// is the plugin's author.
func ValidateDescribe(d *DescribeResponse) error {
	if d.Protocol != ProtocolVersion {
		return fmt.Errorf("plugin speaks protocol %d, this filex speaks %d", d.Protocol, ProtocolVersion)
	}
	if !validName(d.Name) {
		return fmt.Errorf("plugin name %q: want [a-z0-9][a-z0-9_-]{0,31}", d.Name)
	}
	if strings.TrimSpace(d.Label) == "" {
		return errors.New("plugin describes no label")
	}
	if d.Capabilities.Write != d.Capabilities.Delete {
		return errors.New("plugin declares write without delete (or the reverse): a writable driver must be able to remove what it creates")
	}
	seen := map[string]bool{}
	for _, f := range d.Fields {
		if f.Key == "" {
			return errors.New("plugin describes a field with an empty key")
		}
		if seen[f.Key] {
			return fmt.Errorf("plugin describes field %q twice", f.Key)
		}
		seen[f.Key] = true
		switch f.Type {
		case storage.FieldString, storage.FieldInt, storage.FieldBool, storage.FieldPassword, storage.FieldSelect, "":
		default:
			return fmt.Errorf("plugin field %q has unknown type %q", f.Key, f.Type)
		}
	}
	return nil
}

func validName(s string) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '_' || r == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}

// CreateInstance is POST /v1/instances.
func (c *Client) CreateInstance(ctx context.Context, cfg map[string]any) (string, error) {
	var out InstanceResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/instances", nil, InstanceRequest{Config: cfg}, &out); err != nil {
		return "", err
	}
	if out.Instance == "" {
		return "", errors.New("plugin: instance created without an id")
	}
	return out.Instance, nil
}

// DeleteInstance is DELETE /v1/instances/{id}. Best effort — the plugin may
// already be gone.
func (c *Client) DeleteInstance(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/v1/instances/"+url.PathEscape(id), nil, nil, nil)
}

func inst(id, op string) string { return "/v1/instances/" + url.PathEscape(id) + "/" + op }

func (c *Client) List(ctx context.Context, id, path string) ([]Object, error) {
	var out ListResponse
	err := c.doJSON(ctx, http.MethodGet, inst(id, "list"), url.Values{"path": {path}}, nil, &out)
	return out.Objects, err
}

func (c *Client) Stat(ctx context.Context, id, path string) (Object, error) {
	var out Object
	err := c.doJSON(ctx, http.MethodGet, inst(id, "stat"), url.Values{"path": {path}}, nil, &out)
	return out, err
}

// Read streams the object; off/length follow storage.RangeReader (length<0 =
// to end). off==0 && length<0 sends no Range header at all.
func (c *Client) Read(ctx context.Context, id, path string, off, length int64) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, http.MethodGet, inst(id, "read"), url.Values{"path": {path}}, nil)
	if err != nil {
		return nil, err
	}
	if off > 0 || length >= 0 {
		if length < 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-")
		} else {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(off, 10)+"-"+strconv.FormatInt(off+length-1, 10))
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin: read %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, readError(resp)
	}
	return resp.Body, nil
}

// Write streams r as the new content of path.
func (c *Client) Write(ctx context.Context, id, path string, r io.Reader, size int64) error {
	req, err := c.newRequest(ctx, http.MethodPut, inst(id, "write"), url.Values{"path": {path}}, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(SizeHeader, strconv.FormatInt(size, 10))
	if size >= 0 {
		req.ContentLength = size
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("plugin: write %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return readError(resp)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *Client) Move(ctx context.Context, id, src, dst string) error {
	return c.doJSON(ctx, http.MethodPost, inst(id, "move"), nil, MoveRequest{Src: src, Dst: dst}, nil)
}

func (c *Client) Copy(ctx context.Context, id, src, dst string) error {
	return c.doJSON(ctx, http.MethodPost, inst(id, "copy"), nil, MoveRequest{Src: src, Dst: dst}, nil)
}

func (c *Client) Delete(ctx context.Context, id, path string) error {
	return c.doJSON(ctx, http.MethodPost, inst(id, "delete"), nil, PathRequest{Path: path}, nil)
}

func (c *Client) Mkdir(ctx context.Context, id, path string) error {
	return c.doJSON(ctx, http.MethodPost, inst(id, "mkdir"), nil, PathRequest{Path: path}, nil)
}

func (c *Client) SetMtime(ctx context.Context, id, path string, t time.Time) error {
	return c.doJSON(ctx, http.MethodPost, inst(id, "set-mtime"), nil, MtimeRequest{Path: path, Mtime: t}, nil)
}

// Watch opens the SSE stream and forwards events until ctx ends or the
// stream closes. The channel is closed when the stream ends.
func (c *Client) Watch(ctx context.Context, id string) (<-chan Event, error) {
	req, err := c.newRequest(ctx, http.MethodGet, inst(id, "watch"), nil, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	// The stream lives as long as the watch; the transport's header timeout
	// still bounds the connect, but nothing bounds the body.
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("plugin: watch: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		return nil, readError(resp)
	}
	ch := make(chan Event, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var ev Event
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev) != nil {
				continue
			}
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}
