package ftpsrv

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

	ftpserver "github.com/fclairamb/ftpserverlib"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// The transfer handles.
//
// FTP's handle is a Reader+Writer+Seeker+Closer, which is a friendlier shape
// than SFTP's ReaderAt/WriterAt: transfers are sequential, and one connection
// carries one transfer at a time. The Seeker exists for REST — resuming an
// interrupted transfer at an offset — and that is the only thing that seeks.

// ─────────────────────────── reading ───────────────────────────

func (f *fs) openRead(name string, offset int64) (ftpserver.FileTransfer, error) {
	t, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.isRoot() || t.Rel == "" {
		return nil, os.ErrInvalid
	}
	if !f.canRead(t) {
		return nil, os.ErrNotExist
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return nil, err
	}
	src, err := f.srv.cfg.Body.Resolve(f.ctx, drv, t.Storage.ID, t.Rel, nil)
	if err != nil {
		return nil, mapStorageErr(err)
	}
	stat, err := src.Stat(f.ctx)
	if err != nil {
		return nil, mapStorageErr(err)
	}
	if stat.Kind == storage.KindDirectory {
		return nil, os.ErrInvalid
	}
	r := &download{fs: f, size: stat.Size}
	r.open = func(at int64) (io.ReadCloser, error) {
		if at <= 0 {
			return src.Open(context.WithoutCancel(f.ctx))
		}
		if !src.CanRange() {
			// ⚠ Refused rather than silently starting at zero. A REST that
			// resumes from the beginning appends the whole file onto a partial
			// one — the client believes it resumed, and the result is a
			// corrupt file nobody notices until it is opened.
			return nil, fmt.Errorf("ftps: this storage cannot resume a transfer")
		}
		return src.ReadRange(context.WithoutCancel(f.ctx), at, stat.Size-at)
	}
	if offset > 0 {
		if _, err := r.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// download streams an object, reopening at an offset when the client seeks.
type download struct {
	fs   *fs
	size int64
	open func(at int64) (io.ReadCloser, error)

	mu  sync.Mutex
	rc  io.ReadCloser
	pos int64
}

func (d *download) Read(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rc == nil {
		rc, err := d.open(d.pos)
		if err != nil {
			return 0, err
		}
		d.rc = rc
	}
	n, err := d.rc.Read(p)
	d.pos += int64(n)
	return n, err
}

func (d *download) Write([]byte) (int, error) { return 0, os.ErrPermission }

// Seek serves REST. Only the forms a client actually sends are honoured;
// anything else is refused rather than approximated.
func (d *download) Seek(offset int64, whence int) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = d.pos + offset
	case io.SeekEnd:
		abs = d.size + offset
	default:
		return 0, os.ErrInvalid
	}
	if abs < 0 || abs > d.size {
		return 0, os.ErrInvalid
	}
	if abs == d.pos {
		return abs, nil
	}
	// A new position means a new stream: the underlying reader is sequential.
	if d.rc != nil {
		_ = d.rc.Close()
		d.rc = nil
	}
	d.pos = abs
	return abs, nil
}

func (d *download) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rc != nil {
		err := d.rc.Close()
		d.rc = nil
		return err
	}
	return nil
}

// ─────────────────────────── writing ───────────────────────────

func (f *fs) openWrite(name string, flags int, offset int64) (ftpserver.FileTransfer, error) {
	t, err := f.resolve(name)
	if err != nil {
		return nil, err
	}
	if t.isRoot() || t.Rel == "" {
		return nil, os.ErrInvalid
	}
	if !f.canWrite(t) {
		// ⚠ A write that cannot happen must SAY so, unlike a read: answering
		// "no such file" would make a client retry against a path it can see.
		return nil, os.ErrPermission
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return nil, err
	}
	if _, ok := drv.(storage.Writer); !ok {
		return nil, storage.ErrUnsupported
	}
	spool, err := os.CreateTemp(f.srv.cfg.SpoolDir, "filex-ftp-*")
	if err != nil {
		return nil, err
	}
	os.Remove(spool.Name()) // unlinked: a crash leaves nothing behind

	up := &upload{fs: f, target: t, drv: drv, spool: spool}

	// APPE, or STOR after REST: the existing bytes have to be in the spool
	// first, or committing it would truncate the file to whatever arrived now.
	if offset > 0 || flags&os.O_APPEND != 0 {
		if err := up.seedFromExisting(offset); err != nil {
			spool.Close()
			return nil, err
		}
	}
	return up, nil
}

// upload spools the incoming bytes and commits them on Close.
//
// The spool is what makes an interrupted transfer safe. FTP sends no length
// header for STOR, so the server cannot tell a completed upload from a client
// that hung up — the only defence is to write nothing to the real path until
// the transfer has ended cleanly.
type upload struct {
	fs     *fs
	target target
	drv    storage.Driver
	spool  *os.File

	mu      sync.Mutex
	size    int64
	aborted error
	closed  bool
}

// seedFromExisting copies the current object into the spool up to `upTo`
// bytes (0 = all of it), so an append or a resumed transfer starts from what
// is already there.
func (u *upload) seedFromExisting(upTo int64) error {
	src, err := u.fs.srv.cfg.Body.Resolve(u.fs.ctx, u.drv, u.target.Storage.ID, u.target.Rel, nil)
	if err != nil {
		// Nothing there yet: an append to a missing file is just a create.
		return nil
	}
	stat, err := src.Stat(u.fs.ctx)
	if err != nil || stat.Kind == storage.KindDirectory {
		return nil
	}
	body, err := src.Open(context.WithoutCancel(u.fs.ctx))
	if err != nil {
		return mapStorageErr(err)
	}
	defer body.Close()

	var r io.Reader = body
	if upTo > 0 && upTo < stat.Size {
		r = io.LimitReader(body, upTo)
	}
	n, err := io.Copy(u.spool, r)
	if err != nil {
		return err
	}
	u.size = n
	return nil
}

func (u *upload) Read([]byte) (int, error) { return 0, os.ErrPermission }

func (u *upload) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.aborted != nil {
		return 0, u.aborted
	}
	if u.size+int64(len(p)) > u.fs.srv.cfg.MaxSpool {
		u.aborted = fmt.Errorf("ftps: upload exceeds the %d byte limit", u.fs.srv.cfg.MaxSpool)
		return 0, u.aborted
	}
	n, err := u.spool.WriteAt(p, u.size)
	u.size += int64(n)
	return n, err
}

func (u *upload) Seek(offset int64, whence int) (int64, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch whence {
	case io.SeekStart:
		if offset < 0 || offset > u.size {
			return 0, os.ErrInvalid
		}
		u.size = offset
		return offset, nil
	case io.SeekCurrent:
		if offset != 0 {
			return 0, os.ErrInvalid
		}
		return u.size, nil
	case io.SeekEnd:
		if offset != 0 {
			return 0, os.ErrInvalid
		}
		return u.size, nil
	default:
		return 0, os.ErrInvalid
	}
}

// TransferError is what stops a torn upload from being committed.
//
// ⚠⚠ Without it, a client that vanishes mid-transfer gets its half a file
// written over a good one — and FTP's own protocol cannot distinguish that
// from a clean finish, so this hook is the only signal there is.
func (u *upload) TransferError(err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.aborted == nil {
		u.aborted = err
	}
}

func (u *upload) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return nil
	}
	u.closed = true
	defer u.spool.Close()

	if u.aborted != nil {
		slog.Debug("ftps: upload discarded",
			slog.String("path", u.target.Rel), slog.Any("err", u.aborted))
		return u.aborted
	}

	ctx := context.WithoutCancel(u.fs.ctx)
	// The ceiling, before the bytes reach the storage.
	if usr := auth.UserFrom(ctx); usr != nil && u.fs.srv.cfg.Quota != nil {
		if err := u.fs.srv.cfg.Quota.CheckCanWrite(ctx, usr.ID, u.size); err != nil {
			return err
		}
	}
	if _, err := u.spool.Seek(0, io.SeekStart); err != nil {
		return err
	}
	writer, _ := u.drv.(storage.Writer)
	if err := writer.Write(ctx, u.target.Rel, io.LimitReader(u.spool, u.size), u.size); err != nil {
		return mapStorageErr(err)
	}
	// The same bookkeeping every other protocol does, through the shared
	// syncer rather than a copy of it.
	u.fs.srv.syncer.Write(ctx, u.target.Storage, u.target.Rel, u.size, mimeOf(u.target.Rel))
	return nil
}

// mimeOf guesses a content type from the name. FTP carries none, so the
// extension is all there is — the same guess /dav and SFTP make, so a file
// uploaded over any of them looks the same in the explorer.
func mimeOf(rel string) string {
	if ext := path.Ext(rel); ext != "" {
		if ct := mime.TypeByExtension(strings.ToLower(ext)); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}

// userFrom is a thin accessor so fs.go does not import auth for one call.
func userFrom(ctx context.Context) *model.User { return auth.UserFrom(ctx) }
