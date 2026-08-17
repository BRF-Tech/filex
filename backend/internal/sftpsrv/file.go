package sftpsrv

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/pkg/sftp"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Reading and writing, written for the concurrency the library actually has.
//
// ⚠⚠ `sftp.NewRequestServer` hands one handle to EIGHT workers and holds no
// lock over it. With OpenSSH's defaults (64 outstanding requests of 32 KiB)
// both ReadAt and WriteAt are called concurrently at unordered offsets — for a
// plain sequential `put`. Every field below is either immutable after
// construction or guarded, and the failure mode of getting this wrong is the
// one that never shows up in testing: fine on a 1 MB file, corrupt at 2 GB.

// blockSize is the granularity of the read cache. Large enough that a
// sequential read of a 32 KiB window does not become one backend request per
// window; small enough that a random seek does not pull megabytes it will not
// use.
const blockSize = 4 << 20

// maxCachedBlocks bounds one handle's cache. Four blocks is enough to absorb
// the reordering the worker pool produces without becoming a copy of the file
// in memory.
const maxCachedBlocks = 4

// Fileread opens an object for reading.
func (f *fs) Fileread(r *sftp.Request) (io.ReaderAt, error) {
	t, err := f.resolve(r.Filepath)
	if err != nil {
		return nil, err
	}
	if t.isRoot() || t.Rel == "" {
		return nil, os.ErrInvalid
	}
	if !f.canRead(t) {
		// NoSuchFile, not PermissionDenied — see resolve.
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
	return &reader{fs: f, src: src, size: stat.Size, name: t.Rel}, nil
}

// reader serves ReadAt from a backend that may only be able to stream.
type reader struct {
	fs   *fs
	src  *filebody.Source
	size int64
	name string

	mu     sync.Mutex
	blocks map[int64][]byte
	order  []int64
	// seq is the whole-object fallback: a driver with no ranged read cannot
	// answer an arbitrary offset, so the object is pulled once into a temp file
	// and served from there.
	seq *os.File
}

func (r *reader) ReadAt(p []byte, off int64) (int, error) {
	if off >= r.size {
		return 0, io.EOF
	}
	if !r.src.CanRange() {
		return r.readViaSpill(p, off)
	}

	n := 0
	for n < len(p) && off+int64(n) < r.size {
		blk, err := r.block((off + int64(n)) / blockSize)
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		start := (off + int64(n)) % blockSize
		if start >= int64(len(blk)) {
			break
		}
		copied := copy(p[n:], blk[start:])
		n += copied
		if copied == 0 {
			break
		}
	}
	if n == 0 {
		return 0, io.EOF
	}
	if off+int64(n) >= r.size && n < len(p) {
		// The client asked past the end; give it what there was and the EOF in
		// the same answer rather than making it ask again.
		return n, io.EOF
	}
	return n, nil
}

// block returns one cached block, fetching it if necessary.
func (r *reader) block(idx int64) ([]byte, error) {
	r.mu.Lock()
	if b, ok := r.blocks[idx]; ok {
		r.mu.Unlock()
		return b, nil
	}
	r.mu.Unlock()

	off := idx * blockSize
	length := int64(blockSize)
	if off+length > r.size {
		length = r.size - off
	}
	if length <= 0 {
		return nil, io.EOF
	}
	rc, err := r.src.ReadRange(context.WithoutCancel(r.fs.ctx), off, length)
	if err != nil {
		return nil, mapStorageErr(err)
	}
	defer rc.Close()
	buf := make([]byte, length)
	if _, err := io.ReadFull(rc, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, mapStorageErr(err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.blocks == nil {
		r.blocks = map[int64][]byte{}
	}
	// Another worker may have fetched the same block while this one was in
	// flight; keeping the first keeps the map bounded and the answer identical.
	if b, ok := r.blocks[idx]; ok {
		return b, nil
	}
	r.blocks[idx] = buf
	r.order = append(r.order, idx)
	for len(r.order) > maxCachedBlocks {
		delete(r.blocks, r.order[0])
		r.order = r.order[1:]
	}
	return buf, nil
}

// readViaSpill serves a driver that cannot do ranged reads by pulling the
// object into a temp file once.
//
// ⚠ It is the last resort, not the default: on a 40 GB object it writes 40 GB
// to local disk. The cap is the spool cap, and exceeding it fails the read
// rather than filling the disk the storages live on.
func (r *reader) readViaSpill(p []byte, off int64) (int, error) {
	r.mu.Lock()
	if r.seq == nil {
		if r.size > r.fs.srv.cfg.MaxSpool {
			r.mu.Unlock()
			return 0, fmt.Errorf("sftp: %s is too large to serve from a backend without ranged reads", r.name)
		}
		tmp, err := os.CreateTemp(r.fs.srv.cfg.SpoolDir, "filex-sftp-read-*")
		if err != nil {
			r.mu.Unlock()
			return 0, err
		}
		body, err := r.src.Open(context.WithoutCancel(r.fs.ctx))
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			r.mu.Unlock()
			return 0, mapStorageErr(err)
		}
		_, cerr := io.Copy(tmp, body)
		body.Close()
		if cerr != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			r.mu.Unlock()
			return 0, cerr
		}
		// Unlinked immediately: the bytes stay reachable through the open
		// handle, and a crash cannot leave the file behind.
		os.Remove(tmp.Name())
		r.seq = tmp
	}
	seq := r.seq
	r.mu.Unlock()
	return seq.ReadAt(p, off)
}

func (r *reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blocks = nil
	r.order = nil
	if r.seq != nil {
		err := r.seq.Close()
		r.seq = nil
		return err
	}
	return nil
}

// ─────────────────────────── writing ───────────────────────────

// Filewrite opens an object for writing.
//
// The bytes go to a spool file first and land on the driver on Close. That is
// what /dav does, and it is not laziness: WriteAt arrives out of order, most
// drivers take a stream with a known length, and a partial upload committed to
// the real path would replace a good file with a torn one.
func (f *fs) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	t, err := f.resolve(r.Filepath)
	if err != nil {
		return nil, err
	}
	if t.isRoot() || t.Rel == "" {
		return nil, os.ErrInvalid
	}
	if !f.canWrite(t) {
		// PermissionDenied, unlike a read: a write that cannot happen must SAY
		// so, or a client retries forever against a path it can see.
		return nil, sftp.ErrSSHFxPermissionDenied
	}
	drv, err := f.driverFor(t)
	if err != nil {
		return nil, err
	}
	if _, ok := drv.(storage.Writer); !ok {
		return nil, sftp.ErrSSHFxOpUnsupported
	}
	spool, err := os.CreateTemp(f.srv.cfg.SpoolDir, "filex-sftp-*")
	if err != nil {
		return nil, err
	}
	os.Remove(spool.Name()) // unlinked: a crash leaves nothing behind
	return &writer{fs: f, target: t, drv: drv, spool: spool}, nil
}

// writer spools an upload and commits it on Close.
type writer struct {
	fs     *fs
	target target
	drv    storage.Driver
	spool  *os.File

	mu      sync.Mutex
	high    int64 // the highest offset written, i.e. the object's size
	aborted error
	closed  bool
}

func (w *writer) WriteAt(p []byte, off int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.aborted != nil {
		return 0, w.aborted
	}
	if off+int64(len(p)) > w.fs.srv.cfg.MaxSpool {
		w.aborted = fmt.Errorf("sftp: upload exceeds the %d byte limit", w.fs.srv.cfg.MaxSpool)
		return 0, w.aborted
	}
	n, err := w.spool.WriteAt(p, off)
	if end := off + int64(n); end > w.high {
		w.high = end
	}
	return n, err
}

// TransferError is called when the session dies with the handle still open.
//
// ⚠ This is what stops a client that vanished mid-upload from getting its torn
// file committed: Close() still runs afterwards, and without this it would
// happily push half an object over a good one.
func (w *writer) TransferError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.aborted == nil {
		w.aborted = err
	}
}

func (w *writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	defer w.spool.Close()

	if w.aborted != nil {
		slog.Debug("sftp: upload discarded",
			slog.String("path", w.target.Rel), slog.Any("err", w.aborted))
		return w.aborted
	}

	ctx := context.WithoutCancel(w.fs.ctx)
	// The per-user ceiling, before the bytes reach the storage rather than
	// after — checking afterwards means the disk already holds what the quota
	// was meant to prevent.
	if u := auth.UserFrom(ctx); u != nil && w.fs.srv.cfg.Quota != nil {
		if err := w.fs.srv.cfg.Quota.CheckCanWrite(ctx, u.ID, w.high); err != nil {
			return err
		}
	}
	if _, err := w.spool.Seek(0, io.SeekStart); err != nil {
		return err
	}

	digest := md5.New()
	body := io.TeeReader(io.LimitReader(w.spool, w.high), digest)
	writer, _ := w.drv.(storage.Writer)
	if err := writer.Write(ctx, w.target.Rel, body, w.high); err != nil {
		return mapStorageErr(err)
	}

	// The same bookkeeping every other protocol does — node row, parent chain,
	// search index, thumbnail, write hook — through the shared syncer rather
	// than a copy of it.
	w.fs.srv.syncer.Write(ctx, w.target.Storage, w.target.Rel, w.high, mimeOf(w.target.Rel))
	_ = hex.EncodeToString(digest.Sum(nil))
	return nil
}

// mimeOf guesses a content type from the name.
//
// SFTP carries none — there is no Content-Type in the protocol — so the
// extension is all there is. The same guess the WebDAV surface makes, for the
// same reason: the node row wants one, and "application/octet-stream" for every
// file uploaded over SFTP would make the explorer show every one of them as an
// unknown blob.
func mimeOf(rel string) string {
	if ext := path.Ext(rel); ext != "" {
		if ct := mime.TypeByExtension(strings.ToLower(ext)); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}
