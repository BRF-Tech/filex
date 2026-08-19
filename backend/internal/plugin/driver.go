package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/metrics"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Handle is what the adapters hold instead of a *Client: the client behind a
// launched plugin changes every time the process restarts (new socket, new
// port), and a storage opened before the restart must keep working after it.
type Handle interface {
	// Client returns the live client, or an error naming why the plugin is
	// unavailable right now (stopped, crashed and backing off, disabled).
	Client() (*Client, error)
	// DriverName is DriverPrefix+name.
	DriverName() string
}

// limitedHandle is a Handle that also carries the plugin's concurrency
// ceiling and its name for metrics. The manager's entry implements it; a
// bare Handle (tests, the conformance run) does not, and then nothing is
// bounded or counted — which is right: a probe must not be refused because
// the plugin is busy serving users.
type limitedHandle interface {
	limits() *limiter
	metricName() string
}

// conn is one storage row's view of a plugin: the config it was created
// with and the instance id the plugin gave back for it.
type conn struct {
	h    Handle
	caps Capabilities
	lim  *limiter
	// metric is the plugin's name for Prometheus labels; empty means the
	// operations are not counted (a conformance run, a test).
	metric string

	mu       sync.Mutex
	cfg      map[string]any
	instance string
	client   *Client // the client the instance was created on
}

// call runs op against a live instance, creating it on first use and ONCE
// more if the plugin says it has never heard of it — which is what a plugin
// restart looks like from here. Every adapter method goes through this, so a
// restart costs one retried request rather than a broken storage.
func (c *conn) call(ctx context.Context, op func(cl *Client, id string) error) error {
	return c.callNamed(ctx, "op", op)
}

// callNamed is call with a label for the metrics. Every path through a plugin
// goes through here, so a plugin cannot be slow, failing or saturated without
// it being visible from outside.
func (c *conn) callNamed(ctx context.Context, name string, op func(cl *Client, id string) error) error {
	release, err := c.acquire(ctx, name)
	if err != nil {
		return err
	}
	defer release()

	started := time.Now()
	err = c.callLocked(ctx, op)
	c.observe(name, started, err)
	return err
}

func (c *conn) acquire(ctx context.Context, name string) (func(), error) {
	if c.lim == nil {
		return func() {}, nil
	}
	release, err := c.lim.acquire(ctx)
	if err != nil {
		if c.metric != "" {
			metrics.PluginOps.WithLabelValues(c.metric, name, "busy").Inc()
		}
		return nil, err
	}
	if c.metric != "" {
		metrics.PluginInFlight.WithLabelValues(c.metric).Inc()
	}
	return func() {
		release()
		if c.metric != "" {
			metrics.PluginInFlight.WithLabelValues(c.metric).Dec()
		}
	}, nil
}

func (c *conn) observe(name string, started time.Time, err error) {
	if c.metric == "" {
		return
	}
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	metrics.PluginOps.WithLabelValues(c.metric, name, outcome).Inc()
	metrics.PluginOpDuration.WithLabelValues(c.metric, name).Observe(time.Since(started).Seconds())
}

func (c *conn) callLocked(ctx context.Context, op func(cl *Client, id string) error) error {
	cl, id, err := c.ensure(ctx, false)
	if err != nil {
		return err
	}
	err = op(cl, id)
	if err == nil {
		return nil
	}
	if !isNoInstance(err) {
		return mapErr(err)
	}
	cl, id, err = c.ensure(ctx, true)
	if err != nil {
		return err
	}
	return mapErr(op(cl, id))
}

// callFast is callNamed with a deadline. Used by the METADATA operations,
// where slowness means trouble; reads and writes deliberately have none,
// because a 20 GB upload is legitimately slow and a timeout there would turn
// a working transfer into a failed one.
func (c *conn) callFast(ctx context.Context, name string, op func(cl *Client, id string) error) error {
	tctx, cancel := context.WithTimeout(ctx, DefaultOpTimeout)
	defer cancel()
	return c.callNamed(tctx, name, op)
}

// ensure returns the current client and instance id, (re)creating the
// instance when the plugin restarted (client changed) or force is set.
func (c *conn) ensure(ctx context.Context, force bool) (*Client, string, error) {
	cl, err := c.h.Client()
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", c.h.DriverName(), err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !force && c.instance != "" && c.client == cl {
		return cl, c.instance, nil
	}
	id, err := cl.CreateInstance(ctx, c.cfg)
	if err != nil {
		return nil, "", fmt.Errorf("%s: init: %w", c.h.DriverName(), mapErr(err))
	}
	c.instance, c.client = id, cl
	return cl, id, nil
}

// ── read half — every profile ───────────────────────────────────────────────

type readOps struct{ c *conn }

func (r *readOps) Init(ctx context.Context, cfg map[string]any) error {
	if cfg == nil {
		cfg = map[string]any{}
	}
	r.c.mu.Lock()
	r.c.cfg = cfg
	r.c.instance, r.c.client = "", nil
	r.c.mu.Unlock()
	// Eager: the admin's "test connection" and the server's pre-warm both
	// expect Init to fail when the config is wrong, not the first List.
	_, _, err := r.c.ensure(ctx, true)
	return err
}

func (r *readOps) Name() string { return r.c.h.DriverName() }

func (r *readOps) Capabilities() storage.Capabilities {
	caps := r.c.caps
	return storage.Capabilities{
		Read: true, Range: true, // range is always offered (emulated when the plugin lacks it)
		Write: caps.Writable(), Delete: caps.Writable(), Move: caps.Writable(),
		Copy: caps.Writable(), Mkdir: caps.Writable(), Watch: caps.Watch,
		Presign: caps.Presign,
	}
}

func (r *readOps) List(ctx context.Context, path string) ([]storage.Object, error) {
	var out []storage.Object
	err := r.c.callFast(ctx, "list", func(cl *Client, id string) error {
		objs, err := cl.List(ctx, id, path)
		if err != nil {
			return err
		}
		out = make([]storage.Object, 0, len(objs))
		for _, o := range objs {
			out = append(out, o.toStorage())
		}
		return nil
	})
	return out, err
}

func (r *readOps) Stat(ctx context.Context, path string) (storage.Object, error) {
	var out storage.Object
	err := r.c.callFast(ctx, "stat", func(cl *Client, id string) error {
		o, err := cl.Stat(ctx, id, path)
		if err != nil {
			return err
		}
		out = o.toStorage()
		return nil
	})
	return out, err
}

func (r *readOps) Read(ctx context.Context, path string) (io.ReadCloser, error) {
	var rc io.ReadCloser
	err := r.c.callNamed(ctx, "read", func(cl *Client, id string) error {
		var err error
		rc, err = cl.Read(ctx, id, path, 0, -1)
		return err
	})
	return rc, err
}

// ReadRange honours storage.RangeReader's contract. When the plugin cannot
// start at an offset the prefix is read and discarded: the bytes are right,
// the cost is the plugin's problem to fix by declaring range.
func (r *readOps) ReadRange(ctx context.Context, path string, off, length int64) (io.ReadCloser, error) {
	if off < 0 {
		return nil, errors.New("plugin: negative offset")
	}
	if length == 0 {
		return storage.EmptyReadCloser(), nil
	}
	if r.c.caps.Range {
		var rc io.ReadCloser
		err := r.c.callNamed(ctx, "read_range", func(cl *Client, id string) error {
			var err error
			rc, err = cl.Read(ctx, id, path, off, length)
			return err
		})
		return rc, err
	}
	rc, err := r.Read(ctx, path)
	if err != nil {
		return nil, err
	}
	if off > 0 {
		if _, err := io.CopyN(io.Discard, rc, off); err != nil {
			rc.Close()
			if errors.Is(err, io.EOF) {
				// At or past EOF is not an error — the contract says so.
				return storage.EmptyReadCloser(), nil
			}
			return nil, err
		}
	}
	return storage.LimitReadCloser(rc, length), nil
}

// ── write half — writable profiles ─────────────────────────────────────────

type writeOps struct{ c *conn }

func (w *writeOps) Write(ctx context.Context, path string, r io.Reader, size int64) error {
	// A body can be sent once. If the first attempt dies with no_instance the
	// reader is already consumed, so the retry only helps when the failure
	// happens before any byte moved. Buffer small bodies to keep the retry
	// honest; stream large ones and accept that a restart mid-upload fails
	// the upload (the caller retries the whole thing, as it would for S3).
	const bufferUpTo = 8 << 20
	if size >= 0 && size <= bufferUpTo {
		b, err := io.ReadAll(io.LimitReader(r, size+1))
		if err != nil {
			return err
		}
		if int64(len(b)) != size {
			return fmt.Errorf("plugin: write %s: got %d bytes, size says %d", path, len(b), size)
		}
		return w.c.callNamed(ctx, "write", func(cl *Client, id string) error {
			return cl.Write(ctx, id, path, bytes.NewReader(b), size)
		})
	}
	return w.c.callNamed(ctx, "write", func(cl *Client, id string) error {
		return cl.Write(ctx, id, path, r, size)
	})
}

func (w *writeOps) Delete(ctx context.Context, path string) error {
	return w.c.callFast(ctx, "delete", func(cl *Client, id string) error { return cl.Delete(ctx, id, path) })
}

// Mkdir is a no-op for plugins without directories — the same choice the
// object-store drivers make.
func (w *writeOps) Mkdir(ctx context.Context, path string) error {
	if !w.c.caps.Mkdir {
		return nil
	}
	return w.c.callFast(ctx, "mkdir", func(cl *Client, id string) error { return cl.Mkdir(ctx, id, path) })
}

// Copy uses the plugin's own copy when it has one, else read→write.
func (w *writeOps) Copy(ctx context.Context, src, dst string) error {
	if w.c.caps.Copy {
		return w.c.callFast(ctx, "copy", func(cl *Client, id string) error { return cl.Copy(ctx, id, src, dst) })
	}
	return w.emulateCopy(ctx, src, dst)
}

func (w *writeOps) emulateCopy(ctx context.Context, src, dst string) error {
	ro := &readOps{c: w.c}
	st, err := ro.Stat(ctx, src)
	if err != nil {
		return err
	}
	if st.Kind == storage.KindDirectory {
		// Recursive directory copy: mkdir, then each child.
		if err := w.Mkdir(ctx, dst); err != nil {
			return err
		}
		children, err := ro.List(ctx, src)
		if err != nil {
			return err
		}
		for _, ch := range children {
			if err := w.emulateCopy(ctx, joinPath(src, ch.Name), joinPath(dst, ch.Name)); err != nil {
				return err
			}
		}
		return nil
	}
	rc, err := ro.Read(ctx, src)
	if err != nil {
		return err
	}
	defer rc.Close()
	return w.Write(ctx, dst, rc, st.Size)
}

// Move uses the plugin's own move when it has one, else copy→delete.
func (w *writeOps) Move(ctx context.Context, src, dst string) error {
	if w.c.caps.Move {
		return w.c.callFast(ctx, "move", func(cl *Client, id string) error { return cl.Move(ctx, id, src, dst) })
	}
	if err := w.Copy(ctx, src, dst); err != nil {
		return err
	}
	return w.deleteTree(ctx, src)
}

func (w *writeOps) deleteTree(ctx context.Context, path string) error {
	ro := &readOps{c: w.c}
	st, err := ro.Stat(ctx, path)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		return err
	}
	if st.Kind == storage.KindDirectory {
		children, err := ro.List(ctx, path)
		if err != nil {
			return err
		}
		for _, ch := range children {
			if err := w.deleteTree(ctx, joinPath(path, ch.Name)); err != nil {
				return err
			}
		}
	}
	return w.Delete(ctx, path)
}

func joinPath(dir, name string) string {
	if dir == "" || dir == "/" {
		return name
	}
	if dir[len(dir)-1] == '/' {
		return dir + name
	}
	return dir + "/" + name
}

// ── optional halves ─────────────────────────────────────────────────────────

type touchOps struct{ c *conn }

func (t *touchOps) SetMtime(ctx context.Context, path string, mtime time.Time) error {
	return t.c.callFast(ctx, "set_mtime", func(cl *Client, id string) error { return cl.SetMtime(ctx, id, path, mtime) })
}

type watchOps struct{ c *conn }

func (w *watchOps) Subscribe(ctx context.Context) (<-chan storage.Event, error) {
	var in <-chan Event
	err := w.c.call(ctx, func(cl *Client, id string) error {
		var err error
		in, err = cl.Watch(ctx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make(chan storage.Event, 64)
	go func() {
		defer close(out)
		for ev := range in {
			select {
			case out <- storage.Event{Op: ev.Op, Path: ev.Path, From: ev.From}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

type presignOps struct{ c *conn }

// PresignUpload/PresignDownload hand the CALLER a URL that skips filex.
//
// ⚠ The URL must be reachable from wherever the caller is — a browser, not
// this server. filex cannot check that for the plugin (it may sit on a
// network the browser cannot see), so conformance only asserts that a URL
// comes back and parses; a plugin that returns a loopback URL will "work" in
// a probe and fail in a browser. Declare presign only when the backend really
// hands out public, signed URLs.
func (p *presignOps) PresignUpload(ctx context.Context, path string, size int64) (storage.PresignedUpload, error) {
	var out storage.PresignedUpload
	err := p.c.call(ctx, func(cl *Client, id string) error {
		r, err := cl.PresignUpload(ctx, id, path, size)
		if err != nil {
			return err
		}
		if r.URL == "" {
			return fmt.Errorf("%s: presign-upload returned no url", p.c.h.DriverName())
		}
		method := r.Method
		if method == "" {
			method = "PUT"
		}
		out = storage.PresignedUpload{
			URL: r.URL, Method: method, Headers: r.Headers, ExpiresAt: r.ExpiresAt,
		}
		return nil
	})
	return out, err
}

func (p *presignOps) PresignDownload(ctx context.Context, path string, ttl time.Duration) (string, error) {
	var out string
	err := p.c.call(ctx, func(cl *Client, id string) error {
		r, err := cl.PresignDownload(ctx, id, path, ttl)
		if err != nil {
			return err
		}
		if r.URL == "" {
			return fmt.Errorf("%s: presign-download returned no url", p.c.h.DriverName())
		}
		out = r.URL
		return nil
	})
	return out, err
}

type multipartOps struct{ c *conn }

func (m *multipartOps) InitMultipart(ctx context.Context, path string, totalSize int64, partCount int) (string, []string, error) {
	var id string
	var urls []string
	err := m.c.call(ctx, func(cl *Client, inst string) error {
		r, err := cl.InitMultipart(ctx, inst, path, totalSize, partCount)
		if err != nil {
			return err
		}
		if r.UploadID == "" {
			return fmt.Errorf("%s: multipart init returned no upload id", m.c.h.DriverName())
		}
		id, urls = r.UploadID, r.PartURLs
		return nil
	})
	return id, urls, err
}

// UploadPart pushes one part from the server side. ⚠ NOT retried on
// no_instance: a part body can only be read once, and a plugin that lost its
// instance has also lost the multipart upload the part belongs to, so the
// caller must start the upload again rather than have a part silently land
// in a different upload.
func (m *multipartOps) UploadPart(ctx context.Context, path, uploadID string, partNumber int, r io.Reader, size int64) (string, error) {
	cl, inst, err := m.c.ensure(ctx, false)
	if err != nil {
		return "", err
	}
	etag, err := cl.UploadPart(ctx, inst, path, uploadID, partNumber, r, size)
	return etag, mapErr(err)
}

func (m *multipartOps) CompleteMultipart(ctx context.Context, path, uploadID string, parts []storage.PartCompletion) error {
	wire := make([]MultipartPart, 0, len(parts))
	for _, p := range parts {
		wire = append(wire, MultipartPart{PartNumber: p.PartNumber, Etag: p.Etag})
	}
	return m.c.call(ctx, func(cl *Client, inst string) error {
		return cl.CompleteMultipart(ctx, inst, path, uploadID, wire)
	})
}

func (m *multipartOps) AbortMultipart(ctx context.Context, path, uploadID string) error {
	return m.c.call(ctx, func(cl *Client, inst string) error {
		return cl.AbortMultipart(ctx, inst, path, uploadID)
	})
}

// ── profiles ────────────────────────────────────────────────────────────────
//
// filex discovers what a driver can do by TYPE-ASSERTING optional interfaces
// on it (storage.Writer, storage.Toucher, …) — at forty-odd call sites, not
// only in ComputeCapabilities. So a plugin that cannot write must be handed
// to filex as a value that does not HAVE a Write method, and one that cannot
// set mtimes as a value without SetMtime.
//
// With five optional axes — write, mtime, watch, presign, multipart — that is
// twenty concrete shapes, which is why they are GENERATED (driver_shapes.go,
// from gen/main.go) rather than written out here. newShape picks the one
// matching the plugin's declared capabilities.

// newBoundDriver builds a driver already attached to an EXISTING instance,
// bypassing Init.
//
// The conformance run needs this: the instance it probes is the throwaway one
// the plugin opened for POST /v1/selftest, so there is no config to Init with
// and nothing may be created a second time underneath a probe.
func newBoundDriver(h Handle, caps Capabilities, cl *Client, instance string) storage.Driver {
	// No limiter and no metrics on purpose: a conformance probe must not be
	// refused because users are keeping the plugin busy, and its calls are
	// not user traffic.
	c := &conn{h: h, caps: caps, cfg: map[string]any{}, instance: instance, client: cl}
	return newShape(
		&readOps{c: c}, &writeOps{c: c}, &touchOps{c: c},
		&watchOps{c: c}, &presignOps{c: c}, &multipartOps{c: c},
		caps,
	)
}

// NewDriver builds a fresh storage.Driver for one storage row on plugin h.
// This is the storage.Factory the manager registers.
//
// The concrete type is chosen by newShape (driver_shapes.go, generated): a
// plugin gets a value whose METHOD SET is exactly what it declared, because
// filex reads capabilities by type assertion at forty-odd call sites.
func NewDriver(h Handle, caps Capabilities) storage.Driver {
	c := &conn{h: h, caps: caps}
	if lh, ok := h.(limitedHandle); ok {
		c.lim, c.metric = lh.limits(), lh.metricName()
	}
	return newShape(
		&readOps{c: c}, &writeOps{c: c}, &touchOps{c: c},
		&watchOps{c: c}, &presignOps{c: c}, &multipartOps{c: c},
		caps,
	)
}
