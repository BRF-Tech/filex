package cliclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Ranged reads and single-file stats — what a MOUNT needs and a copy does not.
//
// `filex sync` and `filex client get` move whole files, so Download was enough.
// A mounted filesystem is the opposite: an application opens a 4 GB video and
// reads a few hundred kilobytes from the middle, and pulling the whole object
// to answer that would make the mount unusable. The manager endpoint already
// speaks Range (it serves through http.ServeContent) — this is the client side
// of that.

// ErrNoRange means the server answered a ranged request with the whole object.
//
// ⚠ It is a real error, not a fallback: a caller that asked for bytes
// [1000,2000) and silently received [0,N) would write the WRONG BYTES into a
// file at the offset it asked about. Wrong data is worse than a refusal.
var ErrNoRange = errors.New("cliclient: the server ignored the requested range")

// Stat describes one remote entry.
type Stat struct {
	Name    string
	Size    int64
	IsDir   bool
	ModTime time.Time
	Mime    string
}

// StatPath returns metadata for one path.
//
// ⚠ It answers from the PARENT's listing rather than a per-file endpoint,
// because the manager API has no stat verb: `action=index` on a file 404s. The
// listing is what the server already computes, so this costs one request and
// stays correct when a driver reports a size the database has not caught up
// with.
func (c *Client) StatPath(ctx context.Context, remote string) (*Stat, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return nil, err
	}
	if rp.IsRoot() {
		return &Stat{Name: rp.Adapter, IsDir: true}, nil
	}
	parent := rp.Dir()
	list, err := c.List(ctx, parent.String())
	if err != nil {
		return nil, err
	}
	want := rp.Base()
	for _, f := range list.Files {
		if f.Basename != want {
			continue
		}
		return entryStat(f), nil
	}
	return nil, &APIError{Status: http.StatusNotFound, Message: "no such path"}
}

func entryStat(f ListEntry) *Stat {
	st := &Stat{Name: f.Basename, Size: f.Size, IsDir: f.Type == "dir", Mime: f.MimeType}
	if f.LastModified > 0 {
		st.ModTime = time.UnixMilli(f.LastModified)
	}
	return st
}

// ListStats returns one directory's entries as stats.
func (c *Client) ListStats(ctx context.Context, remote string) ([]*Stat, error) {
	list, err := c.List(ctx, remote)
	if err != nil {
		return nil, err
	}
	out := make([]*Stat, 0, len(list.Files))
	for _, f := range list.Files {
		out = append(out, entryStat(f))
	}
	return out, nil
}

// ReadRange fetches [off, off+length) of a remote file.
//
// It returns the bytes actually read, which may be fewer at the end of the
// object. A server that answers 200 instead of 206 gets ErrNoRange rather than
// having its whole-object body treated as the window that was asked for.
func (c *Client) ReadRange(ctx context.Context, remote string, off, length int64) ([]byte, error) {
	if off < 0 || length <= 0 {
		return nil, fmt.Errorf("cliclient: bad range %d+%d", off, length)
	}
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("action", "download")
	q.Set("path", rp.String())
	req, err := c.newRequest(ctx, http.MethodGet, managerPath, q, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+length-1))

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
		// The window we asked for.
	case http.StatusOK:
		// ⚠ 200 means the server ignored the Range and is sending everything.
		// Reading it as the window would be silent corruption at an offset.
		if off == 0 {
			// From the start it is at least the right bytes; take what fits.
			return io.ReadAll(io.LimitReader(resp.Body, length))
		}
		return nil, ErrNoRange
	case http.StatusRequestedRangeNotSatisfiable:
		// Past the end of the object: no bytes, not an error, exactly as a
		// local read at EOF behaves.
		return nil, io.EOF
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, apiErrorFrom(resp.StatusCode, b)
	}
	return io.ReadAll(io.LimitReader(resp.Body, length))
}

// SizeOf reads an object's length from a HEAD-shaped request.
//
// It uses a one-byte ranged GET rather than HEAD: the manager endpoint answers
// HEAD, but a Content-Range header is the only place the TOTAL size appears in
// a partial response, and this is the same request the read path makes.
func (c *Client) SizeOf(ctx context.Context, remote string) (int64, error) {
	rp, err := ParseRemotePath(remote)
	if err != nil {
		return 0, err
	}
	q := url.Values{}
	q.Set("action", "download")
	q.Set("path", rp.String())
	req, err := c.newRequest(ctx, http.MethodGet, managerPath, q, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusPartialContent {
		if cr := resp.Header.Get("Content-Range"); cr != "" {
			if i := strings.LastIndex(cr, "/"); i >= 0 {
				if n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64); err == nil {
					return n, nil
				}
			}
		}
	}
	if resp.StatusCode == http.StatusOK && resp.ContentLength >= 0 {
		return resp.ContentLength, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return 0, apiErrorFrom(resp.StatusCode, nil)
	}
	return 0, errors.New("cliclient: the server did not report a size")
}
