package nfssrv

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path"
	"strings"
	"sync"

	billy "github.com/go-git/go-billy/v5"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Opening files.
//
// ⚠⚠ NFS is stateless in a way that decides this design: a client does not
// "open" a file and then stream it, it sends independent READ and WRITE
// requests at arbitrary offsets against a file HANDLE, in any order, from as
// many parallel threads as it likes. There is no close, and no length header on
// a write.
//
// So every handle here is backed by a local spool file, which is the only
// structure that answers random offsets in both directions. Reads fill it once
// from the driver; writes accumulate in it and land on the driver when the
// client is finished (a COMMIT, or the handle falling out of the library's
// cache). The cost is disk, bounded by MaxSpool; the alternative is answering
// an offset with the wrong bytes, which is corruption nobody would notice.

func (f *fs) Open(name string) (billy.File, error) {
	return f.OpenFile(name, os.O_RDONLY, 0)
}

func (f *fs) Create(name string) (billy.File, error) {
	return f.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
}

func (f *fs) OpenFile(name string, flag int, _ os.FileMode) (billy.File, error) {
	t, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.isRoot() || t.Rel == "" {
		return nil, os.ErrInvalid
	}
	write := flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0

	if write {
		if !f.canWrite(t) {
			// A write that cannot happen must say so — unlike a read, where
			// "no such file" is the answer that keeps the tree from leaking.
			return nil, os.ErrPermission
		}
	} else if !f.canRead(t) {
		return nil, os.ErrNotExist
	}

	drv, err := f.driverFor(t)
	if err != nil {
		return nil, err
	}
	if write {
		if _, ok := drv.(storage.Writer); !ok {
			return nil, billy.ErrNotSupported
		}
	}

	spool, err := os.CreateTemp(f.srv.cfg.SpoolDir, "filex-nfs-*")
	if err != nil {
		return nil, err
	}
	// Unlinked at once: the bytes stay reachable through the open handle, and a
	// crash cannot leave the file behind.
	os.Remove(spool.Name())

	h := &file{
		fs:     f,
		target: t,
		drv:    drv,
		spool:  spool,
		name:   path.Base(t.Rel),
		write:  write,
	}

	// O_TRUNC starts empty; anything else seeds the spool with what is there,
	// so a partial write does not lose the rest of the file and a read has
	// something to answer from.
	if flag&os.O_TRUNC == 0 {
		if err := h.seed(); err != nil {
			spool.Close()
			return nil, err
		}
	} else {
		h.dirty = true
	}
	return h, nil
}

// file is one open handle.
type file struct {
	fs     *fs
	target target
	drv    storage.Driver
	spool  *os.File
	name   string
	write  bool

	mu     sync.Mutex
	size   int64
	pos    int64
	dirty  bool
	closed bool
}

// seed copies the current object into the spool.
func (h *file) seed() error {
	src, err := h.fs.srv.cfg.Body.Resolve(h.fs.ctx, h.drv, h.target.Storage.ID, h.target.Rel, nil)
	if err != nil {
		// Nothing there yet: an open-for-write of a missing file is a create.
		if h.write {
			return nil
		}
		return mapErr(err)
	}
	stat, err := src.Stat(h.fs.ctx)
	if err != nil {
		if h.write {
			return nil
		}
		return mapErr(err)
	}
	if stat.Kind == storage.KindDirectory {
		return os.ErrInvalid
	}
	if stat.Size > h.fs.srv.cfg.MaxSpool {
		return fmt.Errorf("nfs: %s is larger than the %d byte spool limit", h.target.Rel, h.fs.srv.cfg.MaxSpool)
	}
	body, err := src.Open(context.WithoutCancel(h.fs.ctx))
	if err != nil {
		return mapErr(err)
	}
	defer body.Close()
	n, err := io.Copy(h.spool, body)
	if err != nil {
		return err
	}
	h.size = n
	return nil
}

func (h *file) Name() string { return h.name }

func (h *file) Read(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pos >= h.size {
		return 0, io.EOF
	}
	n, err := h.spool.ReadAt(p, h.pos)
	h.pos += int64(n)
	if err == io.EOF && n > 0 {
		return n, nil
	}
	return n, err
}

func (h *file) ReadAt(p []byte, off int64) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if off >= h.size {
		return 0, io.EOF
	}
	return h.spool.ReadAt(p, off)
}

func (h *file) Write(p []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.write {
		return 0, os.ErrPermission
	}
	if h.pos+int64(len(p)) > h.fs.srv.cfg.MaxSpool {
		return 0, fmt.Errorf("nfs: write exceeds the %d byte limit", h.fs.srv.cfg.MaxSpool)
	}
	n, err := h.spool.WriteAt(p, h.pos)
	h.pos += int64(n)
	if h.pos > h.size {
		h.size = h.pos
	}
	if n > 0 {
		h.dirty = true
	}
	return n, err
}

func (h *file) Seek(offset int64, whence int) (int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = h.pos + offset
	case io.SeekEnd:
		abs = h.size + offset
	default:
		return 0, os.ErrInvalid
	}
	if abs < 0 {
		return 0, os.ErrInvalid
	}
	h.pos = abs
	return abs, nil
}

func (h *file) Truncate(size int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.write {
		return os.ErrPermission
	}
	if err := h.spool.Truncate(size); err != nil {
		return err
	}
	h.size = size
	h.dirty = true
	return nil
}

// Lock/Unlock are billy's advisory locks. NFSv3's own locking is a separate
// protocol (NLM) that this server does not implement, so claiming to hold a
// lock here would be a promise nothing keeps.
func (h *file) Lock() error   { return nil }
func (h *file) Unlock() error { return nil }

// Close commits the spool when it changed.
func (h *file) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	defer h.spool.Close()

	if !h.write || !h.dirty {
		return nil
	}

	ctx := context.WithoutCancel(h.fs.ctx)
	if u := auth.UserFrom(ctx); u != nil && h.fs.srv.cfg.Quota != nil {
		if err := h.fs.srv.cfg.Quota.CheckCanWrite(ctx, u.ID, h.size); err != nil {
			return err
		}
	}
	if _, err := h.spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	writer, ok := h.drv.(storage.Writer)
	if !ok {
		return billy.ErrNotSupported
	}
	if err := writer.Write(ctx, h.target.Rel, io.LimitReader(h.spool, h.size), h.size); err != nil {
		slog.Warn("nfs: commit failed",
			slog.String("path", h.target.Rel), slog.String("err", err.Error()))
		return mapErr(err)
	}
	// The shared bookkeeping: node row, parent chain, index, thumbnail, hook.
	h.fs.srv.syncer.Write(ctx, h.target.Storage, h.target.Rel, h.size, mimeOf(h.target.Rel))
	return nil
}

// mimeOf guesses a content type from the name — NFS carries none, so the
// extension is all there is. The same guess /dav, SFTP and FTPS make.
func mimeOf(rel string) string {
	if ext := path.Ext(rel); ext != "" {
		if ct := mime.TypeByExtension(strings.ToLower(ext)); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}
