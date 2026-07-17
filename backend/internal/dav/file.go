package dav

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"time"

	"golang.org/x/net/webdav"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// ─────────────────────────────── fileInfo ─────────────────────────────────

// fileInfo adapts storage.Object to os.FileInfo and implements the webdav
// optional interfaces: ContentTyper (so PROPFIND never opens files just to
// sniff a mime type — critical on S3) and ETager (reuse the backend etag
// when the driver reports one).
type fileInfo struct {
	name  string
	size  int64
	mtime time.Time
	dir   bool
	mimeT string
	etag  string
}

func newFileInfo(o storage.Object) *fileInfo {
	return &fileInfo{
		name:  o.Name,
		size:  o.Size,
		mtime: o.Mtime,
		dir:   o.Kind == storage.KindDirectory,
		mimeT: o.Mime,
		etag:  o.Etag,
	}
}

func syntheticDirInfo(name string) *fileInfo {
	return &fileInfo{name: path.Base("/" + name), dir: true, mtime: time.Unix(0, 0)}
}

func (fi *fileInfo) Name() string { return fi.name }
func (fi *fileInfo) Size() int64  { return fi.size }
func (fi *fileInfo) Mode() os.FileMode {
	if fi.dir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (fi *fileInfo) ModTime() time.Time { return fi.mtime }
func (fi *fileInfo) IsDir() bool        { return fi.dir }
func (fi *fileInfo) Sys() any           { return nil }

// ContentType implements webdav.ContentTyper.
func (fi *fileInfo) ContentType(context.Context) (string, error) {
	if fi.dir {
		return "httpd/unix-directory", nil
	}
	if fi.mimeT != "" {
		return fi.mimeT, nil
	}
	if ct := mime.TypeByExtension(path.Ext(fi.name)); ct != "" {
		return ct, nil
	}
	return "application/octet-stream", nil
}

// ETag implements webdav.ETager — backend etag when present, else the
// library's Apache-style mtime+size fallback.
func (fi *fileInfo) ETag(context.Context) (string, error) {
	if fi.etag == "" {
		return "", webdav.ErrNotImplemented
	}
	return `"` + fi.etag + `"`, nil
}

// ─────────────────────────────── dirFile ──────────────────────────────────

// dirFile is a read-only directory handle with a pre-computed child list.
type dirFile struct {
	fi     os.FileInfo
	childs []os.FileInfo
	off    int
}

func newDirFile(fi os.FileInfo, childs []os.FileInfo) *dirFile {
	return &dirFile{fi: fi, childs: childs}
}

func (d *dirFile) Close() error              { return nil }
func (d *dirFile) Read([]byte) (int, error)  { return 0, fs.ErrInvalid }
func (d *dirFile) Write([]byte) (int, error) { return 0, os.ErrPermission }
func (d *dirFile) Seek(int64, int) (int64, error) {
	return 0, fs.ErrInvalid
}
func (d *dirFile) Stat() (os.FileInfo, error) { return d.fi, nil }

func (d *dirFile) Readdir(count int) ([]os.FileInfo, error) {
	if count <= 0 {
		out := d.childs[d.off:]
		d.off = len(d.childs)
		return out, nil
	}
	if d.off >= len(d.childs) {
		return nil, io.EOF
	}
	end := d.off + count
	if end > len(d.childs) {
		end = len(d.childs)
	}
	out := d.childs[d.off:end]
	d.off = end
	return out, nil
}

// ─────────────────────────────── readFile ─────────────────────────────────

// readFile is a lazy, seekable read handle over Driver.Read. The Driver
// interface exposes a plain ReadCloser, so Seek is emulated: forward gaps
// are discarded, a backward seek closes and re-opens the stream. That makes
// http.ServeContent's probe seeks (0,end then 0,start) free and range
// requests O(offset) — fine for the drives WebDAV clients mount.
type readFile struct {
	ctx  context.Context
	drv  storage.Driver
	rel  string
	fi   os.FileInfo
	size int64

	rc   io.ReadCloser
	pos  int64 // logical read position
	rpos int64 // underlying stream position
}

func newReadFile(ctx context.Context, drv storage.Driver, rel string, fi os.FileInfo) *readFile {
	return &readFile{ctx: ctx, drv: drv, rel: rel, fi: fi, size: fi.Size()}
}

func (r *readFile) Stat() (os.FileInfo, error)         { return r.fi, nil }
func (r *readFile) Readdir(int) ([]os.FileInfo, error) { return nil, fs.ErrInvalid }
func (r *readFile) Write([]byte) (int, error)          { return 0, os.ErrPermission }

func (r *readFile) Close() error {
	if r.rc != nil {
		err := r.rc.Close()
		r.rc = nil
		return err
	}
	return nil
}

func (r *readFile) Seek(offset int64, whence int) (int64, error) {
	var abs int64
	switch whence {
	case io.SeekStart:
		abs = offset
	case io.SeekCurrent:
		abs = r.pos + offset
	case io.SeekEnd:
		abs = r.size + offset
	default:
		return 0, fs.ErrInvalid
	}
	if abs < 0 {
		return 0, fs.ErrInvalid
	}
	r.pos = abs
	return abs, nil
}

func (r *readFile) Read(p []byte) (int, error) {
	if r.rc == nil || r.pos < r.rpos {
		if err := r.reopen(); err != nil {
			return 0, err
		}
	}
	if r.pos > r.rpos {
		if _, err := io.CopyN(io.Discard, r.rc, r.pos-r.rpos); err != nil {
			if errors.Is(err, io.EOF) {
				r.rpos = r.pos
				return 0, io.EOF
			}
			return 0, err
		}
		r.rpos = r.pos
	}
	n, err := r.rc.Read(p)
	r.pos += int64(n)
	r.rpos += int64(n)
	return n, err
}

func (r *readFile) reopen() error {
	if r.rc != nil {
		_ = r.rc.Close()
		r.rc = nil
	}
	rc, err := r.drv.Read(r.ctx, r.rel)
	if err != nil {
		return mapErr(err)
	}
	r.rc = rc
	r.rpos = 0
	return nil
}

// ─────────────────────────────── writeFile ────────────────────────────────

// writeFile spools an upload to a local temp file and hands the whole
// object to storage.Writer.Write on Close — Driver writes are whole-object
// (S3 PUT, SFTP create), so streaming into the backend mid-request isn't an
// option. Close also runs the best-effort DB/index/thumbnail sync.
type writeFile struct {
	ctx    context.Context
	h      *Handler
	st     *model.Storage
	drv    storage.Driver
	rel    string
	tmp    *os.File
	size   int64
	closed bool
}

func newWriteFile(ctx context.Context, h *Handler, st *model.Storage, drv storage.Driver, rel string) (webdav.File, error) {
	tmp, err := os.CreateTemp("", "filex-dav-*")
	if err != nil {
		return nil, err
	}
	return &writeFile{ctx: ctx, h: h, st: st, drv: drv, rel: rel, tmp: tmp}, nil
}

func (w *writeFile) Write(p []byte) (int, error) {
	n, err := w.tmp.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *writeFile) Read([]byte) (int, error)           { return 0, fs.ErrInvalid }
func (w *writeFile) Seek(int64, int) (int64, error)     { return 0, fs.ErrInvalid }
func (w *writeFile) Readdir(int) ([]os.FileInfo, error) { return nil, fs.ErrInvalid }

func (w *writeFile) Stat() (os.FileInfo, error) {
	return &fileInfo{name: path.Base(w.rel), size: w.size, mtime: time.Now()}, nil
}

func (w *writeFile) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	defer func() {
		name := w.tmp.Name()
		_ = w.tmp.Close()
		_ = os.Remove(name)
	}()

	if _, err := w.tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	wr, ok := w.drv.(storage.Writer)
	if !ok {
		return storage.ErrUnsupported
	}
	// WithoutCancel: a client that drops the connection right after the last
	// body byte must not abort the backend flush halfway through.
	ctx := context.WithoutCancel(w.ctx)
	if err := wr.Write(ctx, w.rel, w.tmp, w.size); err != nil {
		return mapErr(err)
	}
	w.h.syncWrite(ctx, w.st, w.rel, w.size, sniffTempMime(w.tmp, w.rel))
	return nil
}

// sniffTempMime detects the uploaded file's mime from its first bytes, with
// the storage package's office-container refinement, falling back to the
// extension. Best-effort: an empty result is fine (sync stores "").
func sniffTempMime(tmp *os.File, rel string) string {
	head := make([]byte, 512)
	n, _ := tmp.ReadAt(head, 0)
	if n <= 0 {
		return mime.TypeByExtension(path.Ext(rel))
	}
	return storage.RefineOfficeMime(http.DetectContentType(head[:n]), path.Base(rel))
}
