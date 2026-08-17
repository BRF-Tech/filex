//go:build linux

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

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// The FUSE binding.
//
// ⚠ Linux only, and pure Go on purpose. `hanwen/go-fuse` speaks the kernel
// protocol directly, so `filex mount` stays in the same CGO_ENABLED=0 binary as
// everything else — no compiler, no libfuse, nothing to install beyond a kernel
// that has FUSE (every desktop distribution since ~2005). macOS needs macFUSE
// and Windows needs WinFsp; both are separate installs with their own licences,
// and neither is pretended at here. On those platforms `filex mount` says so
// and points at the folder sync, which is honest and takes a second to read.

// Mount attaches fsys at mountpoint and blocks until it is unmounted.
func Mount(fsys *FS, mountpoint string, debug bool) (*Server, error) {
	root := &node{fs: fsys, path: "/"}
	sec := time.Duration(fsys.cfg.AttrTTL)
	opts := &fs.Options{
		AttrTimeout:  &sec,
		EntryTimeout: &sec,
		MountOptions: fuse.MountOptions{
			// ⚠ The name a user sees in `df` and in their file manager's
			// sidebar. Without it the mount shows up as "fuse", which tells
			// nobody which of several mounts this is.
			FsName: "filex",
			Name:   "filex",
			Debug:  debug,
			// AllowOther is deliberately NOT set. It would let every account on
			// the machine read the mount with this user's credential, and on a
			// shared box that is the whole point of having accounts.
			AllowOther: false,
			// Writes arrive as 128 KiB rather than 4 KiB, which is the
			// difference between one request per block and thirty-two.
			MaxWrite: 1 << 17,
		},
	}
	srv, err := fs.Mount(mountpoint, root, opts)
	if err != nil {
		return nil, err
	}
	return &Server{srv: srv, mountpoint: mountpoint}, nil
}

// Server is a live mount.
type Server struct {
	srv        *fuse.Server
	mountpoint string
}

// Wait blocks until the filesystem is unmounted.
func (s *Server) Wait() { s.srv.Wait() }

// Unmount detaches it.
//
// ⚠ A mount left behind after the process dies is worse than no mount: every
// `ls` in that directory hangs until somebody runs `fusermount -u` by hand. The
// command that owns this always unmounts on the way out, including on a signal.
func (s *Server) Unmount() error { return s.srv.Unmount() }

// Mountpoint is where it is attached.
func (s *Server) Mountpoint() string { return s.mountpoint }

// ─────────────────────────── nodes ───────────────────────────

// node is one path in the mounted tree.
type node struct {
	fs.Inode
	fs   *FS
	path string
}

var (
	_ fs.NodeGetattrer = (*node)(nil)
	_ fs.NodeLookuper  = (*node)(nil)
	_ fs.NodeReaddirer = (*node)(nil)
	_ fs.NodeOpener    = (*node)(nil)
	_ fs.NodeCreater   = (*node)(nil)
	_ fs.NodeMkdirer   = (*node)(nil)
	_ fs.NodeUnlinker  = (*node)(nil)
	_ fs.NodeRmdirer   = (*node)(nil)
	_ fs.NodeRenamer   = (*node)(nil)
	_ fs.NodeSetattrer = (*node)(nil)
	_ fs.NodeStatfser  = (*node)(nil)
)

func (n *node) child(name string) string { return path.Join(n.path, name) }

// modeOf synthesises the permission bits.
//
// ⚠ Same reasoning as every other protocol here: filex has no POSIX bits, the
// kernel ACTS on the ones it is given (it refuses a write to a file it was told
// is read-only before any request reaches this process), and a read-only mount
// that showed writable files would produce failures the user cannot explain.
func (n *node) modeOf(st *cliclient.Stat) uint32 {
	if st.IsDir {
		if n.fs.ReadOnly() {
			return fuse.S_IFDIR | 0o555
		}
		return fuse.S_IFDIR | 0o755
	}
	if n.fs.ReadOnly() {
		return fuse.S_IFREG | 0o444
	}
	return fuse.S_IFREG | 0o644
}

func (n *node) fill(out *fuse.Attr, st *cliclient.Stat) {
	out.Mode = n.modeOf(st)
	out.Size = uint64(st.Size)
	if !st.ModTime.IsZero() {
		t := uint64(st.ModTime.Unix())
		out.Mtime, out.Ctime, out.Atime = t, t, t
	}
	// The mount belongs to whoever ran the command; there is nobody else to
	// attribute it to, and a foreign uid would make every file unopenable.
	out.Owner.Uid = uint32(os.Getuid())
	out.Owner.Gid = uint32(os.Getgid())
	out.Nlink = 1
	if st.IsDir {
		out.Nlink = 2
	}
}

func (n *node) Getattr(ctx context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	st, err := n.fs.Stat(ctx, n.path)
	if err != nil {
		return errnoOf(err)
	}
	n.fill(&out.Attr, st)
	return 0
}

func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	p := n.child(name)
	st, err := n.fs.Stat(ctx, p)
	if err != nil {
		return nil, errnoOf(err)
	}
	child := &node{fs: n.fs, path: p}
	n.fill(&out.Attr, st)
	mode := uint32(fuse.S_IFREG)
	if st.IsDir {
		mode = fuse.S_IFDIR
	}
	return n.NewInode(ctx, child, fs.StableAttr{Mode: mode}), 0
}

func (n *node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries, err := n.fs.ReadDir(ctx, n.path)
	if err != nil {
		return nil, errnoOf(err)
	}
	out := make([]fuse.DirEntry, 0, len(entries))
	for _, e := range entries {
		mode := uint32(fuse.S_IFREG)
		if e.IsDir {
			mode = fuse.S_IFDIR
		}
		out = append(out, fuse.DirEntry{Name: e.Name, Mode: mode})
	}
	return fs.NewListDirStream(out), 0
}

func (n *node) Mkdir(ctx context.Context, name string, _ uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	p := n.child(name)
	if err := n.fs.Mkdir(ctx, p); err != nil {
		return nil, errnoOf(err)
	}
	child := &node{fs: n.fs, path: p}
	out.Attr.Mode = fuse.S_IFDIR | 0o755
	out.Attr.Owner.Uid = uint32(os.Getuid())
	out.Attr.Owner.Gid = uint32(os.Getgid())
	return n.NewInode(ctx, child, fs.StableAttr{Mode: fuse.S_IFDIR}), 0
}

func (n *node) Unlink(ctx context.Context, name string) syscall.Errno {
	return errnoOf(n.fs.Remove(ctx, n.child(name)))
}

func (n *node) Rmdir(ctx context.Context, name string) syscall.Errno {
	return errnoOf(n.fs.Remove(ctx, n.child(name)))
}

func (n *node) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, _ uint32) syscall.Errno {
	target, ok := newParent.(*node)
	if !ok {
		return syscall.EXDEV
	}
	return errnoOf(n.fs.Rename(ctx, n.child(name), target.child(newName)))
}

// Setattr accepts what it cannot keep.
//
// ⚠ Returning an error here breaks ordinary commands: `cp` chmods, editors
// truncate before writing, and `touch` sets times. filex has no permission bits
// to store and the REST API has no ranged write, so the honest thing is to
// accept the call, report what the file actually is, and let the write path
// decide the content — rather than failing an operation the user's tool
// considers routine.
func (n *node) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if h, ok := fh.(*handle); ok {
		if sz, ok := in.GetSize(); ok {
			if err := h.truncate(int64(sz)); err != nil {
				return errnoOf(err)
			}
		}
	}
	return n.Getattr(ctx, fh, out)
}

// Statfs answers `df`.
func (n *node) Statfs(_ context.Context, out *fuse.StatfsOut) syscall.Errno {
	const blockSize = 4096
	out.Bsize = blockSize
	out.Frsize = blockSize
	// A large-but-finite figure: zero makes tools report a full disk and refuse
	// to copy, and the real ceiling lives on the server (quota, backend).
	out.Blocks = 1 << 38
	out.Bfree = 1 << 38
	out.Bavail = 1 << 38
	out.Files = 1 << 30
	out.Ffree = 1 << 30
	out.NameLen = 255
	return 0
}

// ─────────────────────────── file handles ───────────────────────────

func (n *node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	write := flags&(syscall.O_WRONLY|syscall.O_RDWR|syscall.O_APPEND|syscall.O_TRUNC) != 0
	if write && n.fs.ReadOnly() {
		return nil, 0, syscall.EROFS
	}
	st, err := n.fs.Stat(ctx, n.path)
	if err != nil {
		return nil, 0, errnoOf(err)
	}
	h := &handle{node: n, size: st.Size, write: write}
	if write {
		if err := h.startWrite(ctx, flags&syscall.O_TRUNC == 0); err != nil {
			return nil, 0, errnoOf(err)
		}
	}
	return h, 0, 0
}

func (n *node) Create(ctx context.Context, name string, flags uint32, _ uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	if n.fs.ReadOnly() {
		return nil, nil, 0, syscall.EROFS
	}
	p := n.child(name)
	child := &node{fs: n.fs, path: p}
	h := &handle{node: child, write: true, created: true}
	if err := h.startWrite(ctx, false); err != nil {
		return nil, nil, 0, errnoOf(err)
	}
	out.Attr.Mode = fuse.S_IFREG | 0o644
	out.Attr.Owner.Uid = uint32(os.Getuid())
	out.Attr.Owner.Gid = uint32(os.Getgid())
	inode := n.NewInode(ctx, child, fs.StableAttr{Mode: fuse.S_IFREG})
	return inode, h, 0, 0
}

// handle is one open file.
//
// A read handle answers from the block cache. A write handle spools locally and
// uploads on release, because the REST API takes a whole file — there is no
// ranged write on the wire, and committing a partial upload under the real name
// would replace a good file with a torn one.
type handle struct {
	node    *node
	size    int64
	write   bool
	created bool

	mu     sync.Mutex
	spool  *os.File
	dirty  bool
	closed bool
}

var (
	_ fs.FileReader   = (*handle)(nil)
	_ fs.FileWriter   = (*handle)(nil)
	_ fs.FileReleaser = (*handle)(nil)
	_ fs.FileFlusher  = (*handle)(nil)
)

// startWrite opens the spool, seeding it with the current contents unless the
// caller asked to truncate.
func (h *handle) startWrite(ctx context.Context, seed bool) error {
	spool, err := h.node.fs.SpoolPath()
	if err != nil {
		return err
	}
	// Unlinked at once: the bytes stay reachable through the handle and a crash
	// leaves nothing behind.
	os.Remove(spool.Name())
	h.spool = spool

	if !seed || h.created || h.size == 0 {
		return nil
	}
	if h.size > h.node.fs.MaxSpool() {
		return syscall.EFBIG
	}
	// ⚠ The existing bytes have to be here before the first write: an editor
	// that opens a file, changes one line and writes only that region would
	// otherwise commit a file containing just that region.
	buf := make([]byte, 1<<20)
	var off int64
	for off < h.size {
		n, err := h.node.fs.ReadAt(ctx, h.node.path, buf, off, h.size)
		if n > 0 {
			if _, werr := h.spool.WriteAt(buf[:n], off); werr != nil {
				return werr
			}
			off += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	return nil
}

func (h *handle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	h.mu.Lock()
	spool, size := h.spool, h.size
	h.mu.Unlock()

	// A handle open for writing reads back what it has written so far — a
	// program that writes and then reads the same file (every editor doing a
	// safe save) must see its own bytes.
	if spool != nil {
		n, err := spool.ReadAt(dest, off)
		if err != nil && err != io.EOF {
			return nil, errnoOf(err)
		}
		return fuse.ReadResultData(dest[:n]), 0
	}
	n, err := h.node.fs.ReadAt(ctx, h.node.path, dest, off, size)
	if err != nil && err != io.EOF {
		return nil, errnoOf(err)
	}
	return fuse.ReadResultData(dest[:n]), 0
}

func (h *handle) Write(_ context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.write || h.spool == nil {
		return 0, syscall.EBADF
	}
	if off+int64(len(data)) > h.node.fs.MaxSpool() {
		return 0, syscall.EFBIG
	}
	n, err := h.spool.WriteAt(data, off)
	if err != nil {
		return 0, errnoOf(err)
	}
	if end := off + int64(n); end > h.size {
		h.size = end
	}
	h.dirty = true
	return uint32(n), 0
}

func (h *handle) truncate(size int64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.spool == nil {
		return syscall.EBADF
	}
	if err := h.spool.Truncate(size); err != nil {
		return err
	}
	h.size = size
	h.dirty = true
	return nil
}

// Flush runs on close(2). It is where the upload happens, so that a program
// which closes the file and immediately reads it back sees its own bytes.
func (h *handle) Flush(ctx context.Context) syscall.Errno {
	return errnoOf(h.commit(ctx))
}

// Release runs when the last reference goes.
func (h *handle) Release(ctx context.Context) syscall.Errno {
	err := h.commit(ctx)
	h.mu.Lock()
	if h.spool != nil {
		_ = h.spool.Close()
		h.spool = nil
	}
	h.mu.Unlock()
	return errnoOf(err)
}

func (h *handle) commit(ctx context.Context) error {
	h.mu.Lock()
	if h.closed || !h.write || !h.dirty || h.spool == nil {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	spool, size := h.spool, h.size
	h.mu.Unlock()

	if err := spool.Sync(); err != nil {
		return err
	}
	// Upload takes a path, and the spool is unlinked — so it is republished
	// through /proc, which is the one place an unlinked fd has a name.
	tmpName := procPath(spool)
	if tmpName == "" {
		return syscall.EIO
	}
	if err := h.node.fs.Upload(ctx, h.node.path, tmpName); err != nil {
		slog.Debug("mount: upload failed",
			"path", h.node.path, "bytes", size, "err", err)
		return err
	}
	h.mu.Lock()
	h.dirty = false
	h.mu.Unlock()
	return nil
}

// procPath names an unlinked file through /proc/self/fd.
func procPath(f *os.File) string {
	if f == nil {
		return ""
	}
	return "/proc/self/fd/" + strings.TrimSpace(itoa(int(f.Fd())))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// errnoOf maps an error onto what the kernel expects.
//
// ⚠ The mapping matters more than it looks: a client behaves completely
// differently on ENOENT (create it) than on EACCES (tell the user) or EIO
// (retry, then give up). Collapsing everything to EIO makes a permission
// problem look like a broken disk.
// errnoOf turns an error into the Linux errno the kernel expects.
//
// ⚠ The JUDGEMENT of what each error means lives in Classify (errkind.go),
// shared with the Windows binding. Only the vocabulary is here: two copies of
// "is this out-of-space or permission-denied?" would drift, and the drift
// would show as one platform reporting a quota rejection as a generic I/O
// error while the other got it right.
func errnoOf(err error) syscall.Errno {
	switch Classify(err) {
	case ErrNone:
		return 0
	case ErrNotFound:
		return syscall.ENOENT
	case ErrDenied:
		return syscall.EACCES
	case ErrExists:
		return syscall.EEXIST
	case ErrReadOnlyFS:
		return syscall.EROFS
	case ErrNoSpace:
		return syscall.ENOSPC
	case ErrTooBig:
		return syscall.EFBIG
	}
	return syscall.EIO
}
