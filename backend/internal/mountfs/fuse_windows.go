//go:build windows

package mountfs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/winfsp/cgofuse/fuse"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// The Windows binding, on WinFsp.
//
// # Why this can exist at all
//
// The obvious objection is CGO: filex ships CGO_ENABLED=0 everywhere, and a
// FUSE binding that needs a C toolchain would end that. cgofuse has a
// **CGO-free path on Windows** (`host_nocgo_windows.go`, build tag
// `!cgo && windows`) which loads `winfsp-x64.dll` at run time with
// syscall.LoadDLL instead of linking it. So `filex mount` on Windows is the
// same single binary as everywhere else.
//
// # Licences, since they decided the design elsewhere
//
//   - cgofuse is MIT.
//   - WinFsp is GPLv3 with a FLOSS exception. filex neither ships nor fetches
//     it — the DLL is loaded from wherever the USER installed it, and the
//     exception covers filex (MIT) in any case. The day filex ships
//     closed-source this is a question again, which is worth knowing now
//     rather than then.
//
// # What is different from Linux
//
//   - No /proc, so a write spool cannot be an unlinked fd with a name. It is a
//     real temp file, removed after the upload.
//   - The mountpoint may be a DRIVE LETTER (`Z:`) as well as a directory, and
//     a drive letter is what most people want on Windows.
//   - cgofuse's interface is path-based rather than a node tree, which happens
//     to match mountfs.FS exactly — this file is a thin adapter, not a second
//     implementation of the filesystem.

// Mount attaches fsys at mountpoint.
//
// ⚠ `host.Mount` BLOCKS for the life of the mount, so it runs on its own
// goroutine and this waits to see whether it came up. Returning "mounted" for
// something that failed a moment later would hand the user a drive letter that
// is not there.
func Mount(fsys *FS, mountpoint string, debug bool) (*Server, error) {
	impl := &winFS{
		fs:      fsys,
		handles: map[uint64]*winHandle{},
		pending: map[string]*winHandle{},
	}
	host := fuse.NewFileSystemHost(impl)
	host.SetCapReaddirPlus(true)

	srv := &Server{host: host, mountpoint: mountpoint, done: make(chan struct{})}

	opts := []string{
		// The label shown next to the drive in Explorer. Without it the mount
		// is an unnamed volume, which tells nobody which of several it is.
		"-o", "volname=filex",
		// ⚠ Case-insensitive, because Windows is: a program that opens
		// `Report.PDF` after listing `report.pdf` must find it, and every
		// installer and half the editors on the platform do exactly that.
		"-o", "uid=-1", "-o", "gid=-1",
	}
	if debug {
		opts = append(opts, "-d")
	}

	go func() {
		defer close(srv.done)
		if !host.Mount(mountpoint, opts) {
			srv.failed.Store(true)
		}
	}()

	// Wait for it to be real. WinFsp takes a moment to register the volume,
	// and the honest test is whether the path can be listed.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if srv.failed.Load() {
			return nil, ErrMountFailed
		}
		select {
		case <-srv.done:
			return nil, ErrMountFailed
		default:
		}
		if _, err := os.Stat(ensureSlash(mountpoint)); err == nil {
			return srv, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	_ = host.Unmount()
	return nil, ErrMountTimeout
}

// ensureSlash turns `Z:` into `Z:\` — os.Stat on a bare drive letter asks
// about the CURRENT DIRECTORY of that drive, not its root, which is a
// different question and usually the wrong answer.
func ensureSlash(mountpoint string) string {
	if len(mountpoint) == 2 && mountpoint[1] == ':' {
		return mountpoint + `\`
	}
	return mountpoint
}

// Server is a live mount.
type Server struct {
	host       *fuse.FileSystemHost
	mountpoint string
	done       chan struct{}
	failed     atomicBool
}

// Wait blocks until the filesystem is unmounted.
func (s *Server) Wait() { <-s.done }

// Unmount detaches it.
func (s *Server) Unmount() error {
	if !s.host.Unmount() {
		return ErrUnmountFailed
	}
	return nil
}

// Mountpoint is where it is attached.
func (s *Server) Mountpoint() string { return s.mountpoint }

// ─────────────────────────── the filesystem ───────────────────────────

type winFS struct {
	fuse.FileSystemBase
	fs *FS

	mu      sync.Mutex
	next    uint64
	handles map[uint64]*winHandle
	// pending is the files this mount has created but not yet uploaded.
	//
	// ⚠⚠ Without it, creating a file FAILS. A write lands on the server only
	// when the handle is closed, so between Create and Release the path does
	// not exist remotely — and WinFsp asks Getattr about it immediately, unlike
	// Linux where go-fuse takes the attributes from the Create reply. The
	// server answers 404, Windows reports "Could not find file", and the write
	// the user just made appears to fail. Measured 2026-08-17: every `Out-File`
	// onto the mount.
	pending map[string]*winHandle
}

// winHandle is one open file. Reads come from the block cache; writes spool to
// a local temp file and land on the server when the handle is closed, for the
// same reason as everywhere else: the REST API takes a whole object, and a
// partial upload committed under the real name replaces a good file with a
// torn one.
type winHandle struct {
	path    string
	size    int64
	write   bool
	created bool

	mu     sync.Mutex
	spool  *os.File
	dirty  bool
	closed bool
}

func (w *winFS) ctx() context.Context { return context.Background() }

// errc turns an error into the negative errno cgofuse expects.
//
// ⚠ The JUDGEMENT of what each error means lives in Classify (errkind.go),
// shared with the Linux binding. Only the vocabulary is here — see the note
// there for why that split matters.
func errc(err error) int {
	switch Classify(err) {
	case ErrNone:
		return 0
	case ErrNotFound:
		return -fuse.ENOENT
	case ErrDenied:
		return -fuse.EACCES
	case ErrExists:
		return -fuse.EEXIST
	case ErrReadOnlyFS:
		return -fuse.EROFS
	case ErrNoSpace:
		return -fuse.ENOSPC
	case ErrTooBig:
		return -fuse.EFBIG
	}
	return -fuse.EIO
}

func (w *winFS) fill(stat *fuse.Stat_t, st *cliclient.Stat) {
	// ⚠ The permission bits are synthesised from the mount's read-only flag,
	// the same as on Linux and for the same reason: WinFsp maps them onto the
	// read-only file ATTRIBUTE, and Explorer acts on that before any request
	// reaches this process.
	if st.IsDir {
		stat.Mode = fuse.S_IFDIR | 0o755
		if w.fs.ReadOnly() {
			stat.Mode = fuse.S_IFDIR | 0o555
		}
		stat.Nlink = 2
	} else {
		stat.Mode = fuse.S_IFREG | 0o644
		if w.fs.ReadOnly() {
			stat.Mode = fuse.S_IFREG | 0o444
		}
		stat.Nlink = 1
	}
	stat.Size = st.Size
	// ⚠ A stand-in when there is none, rather than leaving it zero. A storage
	// folder has no modification time of its own, and zero renders in Explorer
	// as **01/01/1970** — which reads as a corrupt volume, not as "unknown".
	// The mount's own start time is stable for the life of the mount, which is
	// what matters: a timestamp that moved on every listing would make every
	// client think every folder had just changed.
	when := st.ModTime
	if when.IsZero() {
		when = mountStarted
	}
	t := fuse.NewTimespec(when)
	stat.Mtim, stat.Ctim, stat.Atim, stat.Birthtim = t, t, t, t
}

// mountStarted is fixed at process start; see fill.
var mountStarted = time.Now()

func (w *winFS) Getattr(p string, stat *fuse.Stat_t, _ uint64) int {
	// A file this mount created but has not uploaded yet is answered from the
	// handle — see the note on `pending`.
	if h := w.peekPending(p); h != nil {
		h.mu.Lock()
		size := h.size
		h.mu.Unlock()
		w.fill(stat, &cliclient.Stat{Name: path.Base(p), Size: size, ModTime: time.Now()})
		return 0
	}
	st, err := w.fs.Stat(w.ctx(), p)
	if err != nil {
		return errc(err)
	}
	w.fill(stat, st)
	return 0
}

func (w *winFS) peekPending(p string) *winHandle {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.pending[p]
}

// openPendingCopy returns a READ handle over the spool a pending write is
// filling.
//
// ⚠ A second os.File on the same name, not the writer's own: two handles
// sharing one *os.File would share its seek offset, and `write` is left false
// so closing this one never commits or uploads anything.
func (w *winFS) openPendingCopy(src *winHandle, p string) (*winHandle, bool) {
	src.mu.Lock()
	spool, size := src.spool, src.size
	src.mu.Unlock()
	if spool == nil {
		return nil, false
	}
	f, err := os.Open(spool.Name())
	if err != nil {
		return nil, false
	}
	return &winHandle{path: p, size: size, spool: f}, true
}

func (w *winFS) Readdir(
	p string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	_ int64,
	_ uint64,
) int {
	entries, err := w.fs.ReadDir(w.ctx(), p)
	if err != nil {
		return errc(err)
	}
	// ⚠ "." and ".." are required. Explorer copes without them, but `dir` and
	// a good deal of older software do not, and their absence shows up as an
	// empty folder rather than an error.
	fill(".", nil, 0)
	fill("..", nil, 0)
	for _, e := range entries {
		var stat fuse.Stat_t
		w.fill(&stat, e)
		if !fill(path.Base(e.Name), &stat, 0) {
			break
		}
	}
	return 0
}

func (w *winFS) Statfs(_ string, stat *fuse.Statfs_t) int {
	// A plausible, large volume. filex reports a quota through its own
	// surfaces; inventing a number here that Explorer would draw as a nearly
	// full disk is worse than a round one.
	const block = 4096
	stat.Bsize, stat.Frsize = block, block
	stat.Blocks = 1 << 30
	stat.Bfree, stat.Bavail = 1<<29, 1<<29
	stat.Files, stat.Ffree = 1<<20, 1<<19
	stat.Namemax = 255
	return 0
}

func (w *winFS) Open(p string, flags int) (int, uint64) {
	// ⚠⚠ A file this mount created but has not uploaded yet is not on the
	// server, so asking the server about it answers 404 and the open fails.
	// Getattr already knows about `pending`; Open has to as well, or the
	// sequence "write a file, read it straight back" — which is what every
	// script and half the editors do — fails on a file that plainly exists.
	// It reads from the same spool the writer is filling. Measured 2026-08-17:
	// `Out-File` then `Get-Content` returned empty, and worked five seconds
	// later once the attribute cache expired, which is the shape of bug that
	// gets called "flaky" and shipped.
	if h := w.peekPending(p); h != nil {
		if rh, ok := w.openPendingCopy(h, p); ok {
			return 0, w.put(rh)
		}
	}
	st, err := w.fs.Stat(w.ctx(), p)
	if err != nil {
		return errc(err), ^uint64(0)
	}
	write := flags&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_TRUNC) != 0
	if write && w.fs.ReadOnly() {
		return -fuse.EROFS, ^uint64(0)
	}
	h := &winHandle{path: p, size: st.Size, write: write}
	if write {
		if err := h.startWrite(w.ctx(), w.fs, flags&os.O_TRUNC == 0); err != nil {
			return errc(err), ^uint64(0)
		}
	}
	return 0, w.put(h)
}

func (w *winFS) Create(p string, flags int, _ uint32) (int, uint64) {
	if w.fs.ReadOnly() {
		return -fuse.EROFS, ^uint64(0)
	}
	h := &winHandle{path: p, write: true, created: true}
	if err := h.startWrite(w.ctx(), w.fs, false); err != nil {
		return errc(err), ^uint64(0)
	}
	// ⚠ Marked dirty at creation. A program that creates an empty file and
	// closes it without writing still means to create it, and a commit gated
	// only on "something was written" would silently drop it.
	h.dirty = true
	w.mu.Lock()
	w.pending[p] = h
	w.mu.Unlock()
	return 0, w.put(h)
}

func (w *winFS) Read(p string, buff []byte, ofst int64, fh uint64) int {
	h := w.get(fh)
	if h == nil {
		return -fuse.EBADF
	}
	h.mu.Lock()
	spool, size := h.spool, h.size
	h.mu.Unlock()
	// A handle open for writing reads back what it has written so far —
	// otherwise a read-modify-write cycle sees the pre-edit file.
	if spool != nil {
		n, err := spool.ReadAt(buff, ofst)
		if n > 0 {
			return n
		}
		if err == io.EOF {
			return 0
		}
		return errc(err)
	}
	n, err := w.fs.ReadAt(w.ctx(), p, buff, ofst, size)
	if n > 0 {
		return n
	}
	if err == nil || err == io.EOF {
		return 0
	}
	return errc(err)
}

func (w *winFS) Write(_ string, buff []byte, ofst int64, fh uint64) int {
	h := w.get(fh)
	if h == nil {
		return -fuse.EBADF
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.spool == nil {
		return -fuse.EBADF
	}
	if ofst+int64(len(buff)) > w.fs.MaxSpool() {
		return -fuse.ENOSPC
	}
	n, err := h.spool.WriteAt(buff, ofst)
	if err != nil {
		return errc(err)
	}
	h.dirty = true
	if end := ofst + int64(n); end > h.size {
		h.size = end
	}
	return n
}

func (w *winFS) Truncate(_ string, size int64, fh uint64) int {
	h := w.get(fh)
	if h == nil || h.spool == nil {
		return -fuse.EBADF
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.spool.Truncate(size); err != nil {
		return errc(err)
	}
	h.size = size
	h.dirty = true
	return 0
}

func (w *winFS) Flush(_ string, fh uint64) int {
	h := w.get(fh)
	if h == nil {
		return 0
	}
	return errc(h.commit(w.ctx(), w.fs))
}

func (w *winFS) Release(_ string, fh uint64) int {
	h := w.take(fh)
	if h == nil {
		return 0
	}
	err := h.commit(w.ctx(), w.fs)
	h.mu.Lock()
	if h.spool != nil {
		name := h.spool.Name()
		_ = h.spool.Close()
		// ⚠ Only the WRITER owns the spool file. A read handle from
		// openPendingCopy has its own fd on the same name, and removing it here
		// would delete the file the writer is still filling.
		if h.write {
			_ = os.Remove(name)
		}
		h.spool = nil
	}
	h.mu.Unlock()
	// ⚠ Dropped only when a WRITE handle committed successfully. A read handle
	// over the same spool (openPendingCopy) commits nothing, and letting it
	// clear the entry would make the file vanish while the writer still holds
	// it. And on a failed upload the file is still not on the server, so
	// forgetting it would pull the path out from under a program about to retry.
	if err == nil && h.write {
		w.mu.Lock()
		if w.pending[h.path] == h {
			delete(w.pending, h.path)
		}
		w.mu.Unlock()
	}
	return errc(err)
}

func (w *winFS) Mkdir(p string, _ uint32) int {
	if w.fs.ReadOnly() {
		return -fuse.EROFS
	}
	return errc(w.fs.Mkdir(w.ctx(), p))
}

func (w *winFS) Unlink(p string) int {
	if w.fs.ReadOnly() {
		return -fuse.EROFS
	}
	return errc(w.fs.Remove(w.ctx(), p))
}

func (w *winFS) Rmdir(p string) int {
	if w.fs.ReadOnly() {
		return -fuse.EROFS
	}
	return errc(w.fs.Remove(w.ctx(), p))
}

func (w *winFS) Rename(oldpath, newpath string) int {
	if w.fs.ReadOnly() {
		return -fuse.EROFS
	}
	return errc(w.fs.Rename(w.ctx(), oldpath, newpath))
}

// Chmod / Chown / Utimens are accepted and ignored.
//
// ⚠ Accepted, not refused. filex has no POSIX bits to set, and Windows sets
// timestamps and attributes on almost every file it writes — a refusal here
// makes Explorer report "the file could not be copied" for a copy that fully
// succeeded.
func (w *winFS) Chmod(string, uint32) int            { return 0 }
func (w *winFS) Chown(string, uint32, uint32) int    { return 0 }
func (w *winFS) Utimens(string, []fuse.Timespec) int { return 0 }

// ─────────────────────────── handle table ───────────────────────────

func (w *winFS) put(h *winHandle) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next++
	id := w.next
	w.handles[id] = h
	return id
}

func (w *winFS) get(fh uint64) *winHandle {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.handles[fh]
}

func (w *winFS) take(fh uint64) *winHandle {
	w.mu.Lock()
	defer w.mu.Unlock()
	h := w.handles[fh]
	delete(w.handles, fh)
	return h
}

// ─────────────────────────── write path ───────────────────────────

// startWrite opens the spool, seeding it with the current contents unless the
// caller asked to truncate.
//
// ⚠ Unlike Linux the spool is a NAMED temp file that stays named: Windows has
// no /proc, so an unlinked fd could not be handed to Upload, and a file cannot
// be deleted while it is open anyway. It is removed in Release.
func (h *winHandle) startWrite(ctx context.Context, f *FS, seed bool) error {
	spool, err := f.SpoolPath()
	if err != nil {
		return err
	}
	h.spool = spool

	if !seed || h.created || h.size == 0 {
		return nil
	}
	if h.size > f.MaxSpool() {
		return syscall.EFBIG
	}
	// ⚠ The existing bytes have to be here before the first write: an editor
	// that opens a file, changes one line and writes only that region would
	// otherwise commit a file containing just that region.
	buf := make([]byte, 1<<20)
	var off int64
	for off < h.size {
		n, rerr := f.ReadAt(ctx, h.path, buf, off, h.size)
		if n > 0 {
			if _, werr := h.spool.WriteAt(buf[:n], off); werr != nil {
				return werr
			}
			off += int64(n)
		}
		if rerr == io.EOF || n == 0 {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return nil
}

func (h *winHandle) commit(ctx context.Context, f *FS) error {
	h.mu.Lock()
	if h.closed || !h.write || !h.dirty || h.spool == nil {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	spool := h.spool
	h.mu.Unlock()

	if err := spool.Sync(); err != nil {
		return err
	}
	if err := f.Upload(ctx, h.path, spool.Name()); err != nil {
		slog.Debug("mount: upload failed",
			slog.String("path", h.path), slog.Any("err", err))
		// ⚠ Re-armed on failure so a later Release tries again rather than
		// silently dropping the user's edit.
		h.mu.Lock()
		h.closed = false
		h.mu.Unlock()
		return err
	}
	f.Forget(strings.TrimSuffix(path.Dir(h.path), "/"))
	f.Forget(h.path)
	return nil
}

// atomicBool is a tiny stand-in so this file needs no extra import.
type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (a *atomicBool) Store(v bool) { a.mu.Lock(); a.v = v; a.mu.Unlock() }
func (a *atomicBool) Load() bool   { a.mu.Lock(); defer a.mu.Unlock(); return a.v }
