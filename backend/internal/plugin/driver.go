package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

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

// conn is one storage row's view of a plugin: the config it was created
// with and the instance id the plugin gave back for it.
type conn struct {
	h    Handle
	caps Capabilities

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
	}
}

func (r *readOps) List(ctx context.Context, path string) ([]storage.Object, error) {
	var out []storage.Object
	err := r.c.call(ctx, func(cl *Client, id string) error {
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
	err := r.c.call(ctx, func(cl *Client, id string) error {
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
	err := r.c.call(ctx, func(cl *Client, id string) error {
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
		err := r.c.call(ctx, func(cl *Client, id string) error {
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
		return w.c.call(ctx, func(cl *Client, id string) error {
			return cl.Write(ctx, id, path, bytes.NewReader(b), size)
		})
	}
	return w.c.call(ctx, func(cl *Client, id string) error {
		return cl.Write(ctx, id, path, r, size)
	})
}

func (w *writeOps) Delete(ctx context.Context, path string) error {
	return w.c.call(ctx, func(cl *Client, id string) error { return cl.Delete(ctx, id, path) })
}

// Mkdir is a no-op for plugins without directories — the same choice the
// object-store drivers make.
func (w *writeOps) Mkdir(ctx context.Context, path string) error {
	if !w.c.caps.Mkdir {
		return nil
	}
	return w.c.call(ctx, func(cl *Client, id string) error { return cl.Mkdir(ctx, id, path) })
}

// Copy uses the plugin's own copy when it has one, else read→write.
func (w *writeOps) Copy(ctx context.Context, src, dst string) error {
	if w.c.caps.Copy {
		return w.c.call(ctx, func(cl *Client, id string) error { return cl.Copy(ctx, id, src, dst) })
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
		return w.c.call(ctx, func(cl *Client, id string) error { return cl.Move(ctx, id, src, dst) })
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
	return t.c.call(ctx, func(cl *Client, id string) error { return cl.SetMtime(ctx, id, path, mtime) })
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

// ── profiles ────────────────────────────────────────────────────────────────
//
// filex discovers what a driver can do by TYPE-ASSERTING optional interfaces
// on it (storage.Writer, storage.Toucher, …) — at forty-odd call sites, not
// only in ComputeCapabilities. So a plugin that cannot write must be handed
// to filex as a value that does not HAVE a Write method, and one that cannot
// set mtimes as a value without SetMtime. Eight concrete shapes cover the
// {read-only, writable} × {mtime} × {watch} space; the factory picks one from
// the plugin's declared capabilities.

type (
	drvR  struct{ *readOps }
	drvRT struct {
		*readOps
		*touchOps
	}
	drvRW struct {
		*readOps
		*watchOps
	}
	drvRTW struct {
		*readOps
		*touchOps
		*watchOps
	}
	drvX struct {
		*readOps
		*writeOps
	}
	drvXT struct {
		*readOps
		*writeOps
		*touchOps
	}
	drvXW struct {
		*readOps
		*writeOps
		*watchOps
	}
	drvXTW struct {
		*readOps
		*writeOps
		*touchOps
		*watchOps
	}
)

// Compile-time proof that each shape implements what its name promises —
// and, as importantly, that the read-only ones do NOT satisfy storage.Writer
// (that assertion is in driver_test.go, since Go cannot state a negative
// here).
var (
	_ storage.Driver      = drvR{}
	_ storage.RangeReader = drvR{}
	_ storage.Toucher     = drvRT{}
	_ storage.Watcher     = drvRW{}
	_ storage.Writer      = drvX{}
	_ storage.Mover       = drvX{}
	_ storage.Copier      = drvX{}
	_ storage.Deleter     = drvX{}
	_ storage.Mkdirer     = drvX{}
	_ storage.Toucher     = drvXTW{}
	_ storage.Watcher     = drvXTW{}
)

// NewDriver builds a fresh storage.Driver for one storage row on plugin h.
// This is the storage.Factory the manager registers.
func NewDriver(h Handle, caps Capabilities) storage.Driver {
	c := &conn{h: h, caps: caps}
	r := &readOps{c: c}
	t := &touchOps{c: c}
	w := &watchOps{c: c}
	x := &writeOps{c: c}
	switch {
	case caps.Writable() && caps.SetMtime && caps.Watch:
		return drvXTW{r, x, t, w}
	case caps.Writable() && caps.SetMtime:
		return drvXT{r, x, t}
	case caps.Writable() && caps.Watch:
		return drvXW{r, x, w}
	case caps.Writable():
		return drvX{r, x}
	case caps.SetMtime && caps.Watch:
		return drvRTW{r, t, w}
	case caps.SetMtime:
		return drvRT{r, t}
	case caps.Watch:
		return drvRW{r, w}
	default:
		return drvR{r}
	}
}
