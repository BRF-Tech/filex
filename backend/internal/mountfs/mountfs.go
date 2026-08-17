// Package mountfs is the filesystem behind `filex mount` — a remote filex
// server, seen as a folder on the machine that runs the command.
//
// # Why this exists when NFS and SFTP already do
//
// Both of those need something on the other end that filex does not control:
// NFSv3 has no encryption and belongs on a LAN, and an SFTP mount needs sshfs
// or WinFsp installed and configured. `filex mount` is the one that works from
// anywhere over the same HTTPS the web app uses, with the same session token,
// through whatever proxy or tunnel is already between the two — because it is
// just the REST API with a filesystem in front of it.
//
// # What it is not
//
// It is not a sync. Nothing is copied to the machine except a bounded read
// cache; closing the mount leaves nothing behind. `filex sync` is still the
// answer for "keep a folder on my laptop", and this is the answer for "open one
// file out of a hundred thousand without downloading the other 99,999".
//
// # The shape, and the reason for it
//
// Reads are cached in fixed blocks. A mounted filesystem gets asked for 128 KiB
// at arbitrary offsets by whatever program the user opened, and answering each
// one with its own HTTPS request would make a video unplayable; a block cache
// turns a sequential read into a handful of large requests and a random seek
// into one.
//
// Writes are spooled whole and uploaded on close. That is not laziness: the
// REST API takes a complete file, there is no ranged write on the wire, and a
// partial upload committed under the real name would replace a good file with
// a torn one. The cost is that a 4 GB write needs 4 GB of scratch space, which
// is bounded and stated rather than discovered.
package mountfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/brf-tech/filex/backend/internal/cliclient"
)

// ErrUnsupported is what Mount returns where there is no FUSE driver.
//
// ⚠ Declared HERE rather than in the platform file so a caller can test for it
// on every OS — a Linux build that cannot see the sentinel would print a raw
// error instead of the sentence that tells somebody what to install.
var ErrUnsupported = errors.New(
	"filex mount needs a filesystem driver, and on this platform there is none " +
		"it can use. Linux needs nothing (FUSE is in the kernel) and Windows " +
		"needs WinFsp, a free separate install — https://winfsp.dev. macOS is " +
		"not supported: it needs macFUSE, whose licence forbids a commercial " +
		"program from installing it and whose Go binding needs a C toolchain " +
		"filex deliberately does not use. There, use `filex sync` for a folder, " +
		"or the desktop app")

// ErrTooLarge is a file bigger than the write spool allows.
var ErrTooLarge = errors.New("mountfs: file is larger than the write spool allows")

// Mount failures, shared so a caller can tell them apart on any platform.
var (
	// ErrMountFailed — the driver refused. Almost always a mountpoint that is
	// in use, or a drive letter that is taken.
	ErrMountFailed = errors.New(
		"the filesystem driver refused to mount (is the drive letter or folder already in use?)")
	// ErrMountTimeout — it neither failed nor came up. On Windows this is
	// usually WinFsp missing: the DLL loads, the mount never registers.
	ErrMountTimeout = errors.New(
		"the mount did not come up (on Windows, check that WinFsp is installed — https://winfsp.dev)")
	// ErrUnmountFailed — the driver would not let go.
	ErrUnmountFailed = errors.New("could not unmount")
)

// Config wires the filesystem to a remote server.
type Config struct {
	// Client talks to the remote filex. Required.
	Client *cliclient.Client
	// Remote is what to mount: "" for every storage the account can see,
	// "docs://" for one storage, "docs://projects/acme" for a subtree.
	Remote string
	// ReadOnly refuses every write. The safe default for a mount somebody
	// pointed at a production server to look at something.
	ReadOnly bool
	// BlockSize is the read granularity. 0 uses defaultBlockSize.
	BlockSize int64
	// CacheBlocks bounds the read cache, in blocks. 0 uses the default.
	CacheBlocks int
	// SpoolDir is where writes are spooled. Empty uses the OS temp dir.
	SpoolDir string
	// MaxSpool caps one write. 0 uses the default.
	MaxSpool int64
	// AttrTTL is how long a listing or a stat is trusted.
	//
	// ⚠ Not zero. A filesystem call that always goes to the network makes `ls`
	// on a large folder take seconds and a shell tab-completion feel broken;
	// but a long TTL means somebody else's upload is invisible for that long.
	// A few seconds is the honest middle, and it is a knob rather than a
	// constant because the right answer differs between a laptop and a build
	// machine.
	AttrTTL time.Duration
}

const (
	defaultBlockSize  = 4 << 20
	defaultCacheBlock = 64 // 256 MiB at the default block size
	defaultMaxSpool   = 16 << 30
	defaultAttrTTL    = 5 * time.Second
)

// FS is the remote filesystem.
type FS struct {
	cfg Config
	// root is the remote path the mount point maps to. Empty means the storage
	// list itself is the root.
	rootAdapter string
	rootRel     string

	blocks *blockCache

	mu    sync.Mutex
	attrs map[string]attrEntry
}

type attrEntry struct {
	stat *cliclient.Stat
	// dir is the listing when this entry is a directory and it was fetched.
	dir []*cliclient.Stat
	at  time.Time
}

// New builds a filesystem.
func New(cfg Config) (*FS, error) {
	if cfg.Client == nil {
		return nil, errors.New("mountfs: a client is required")
	}
	if cfg.BlockSize <= 0 {
		cfg.BlockSize = defaultBlockSize
	}
	if cfg.CacheBlocks <= 0 {
		cfg.CacheBlocks = defaultCacheBlock
	}
	if cfg.MaxSpool <= 0 {
		cfg.MaxSpool = defaultMaxSpool
	}
	if cfg.AttrTTL <= 0 {
		cfg.AttrTTL = defaultAttrTTL
	}

	f := &FS{cfg: cfg, attrs: map[string]attrEntry{}}
	f.blocks = newBlockCache(cfg.CacheBlocks)

	if strings.TrimSpace(cfg.Remote) != "" {
		rp, err := cliclient.ParseRemotePath(cfg.Remote)
		if err != nil {
			return nil, err
		}
		f.rootAdapter, f.rootRel = rp.Adapter, rp.Rel
	}
	return f, nil
}

// ReadOnly reports whether writes are refused.
func (f *FS) ReadOnly() bool { return f.cfg.ReadOnly }

// remoteOf maps a mount-relative path onto a remote path.
//
// ⚠ The mount root is NOT always a storage. With no --remote the root is the
// list of storages, so `/docs/report.pdf` names storage `docs`; with one it is
// that folder, and the storage name never appears. Getting this wrong makes
// every path off by one level, which shows up as "no such file" on everything.
func (f *FS) remoteOf(p string) (string, bool) {
	rel := strings.Trim(path.Clean("/"+p), "/")
	if rel == "." {
		rel = ""
	}
	if f.rootAdapter == "" {
		if rel == "" {
			return "", true // the storage list
		}
		adapter, rest, _ := strings.Cut(rel, "/")
		return adapter + "://" + rest, true
	}
	full := rel
	if f.rootRel != "" {
		full = path.Join(f.rootRel, rel)
	}
	return f.rootAdapter + "://" + strings.Trim(full, "/"), true
}

// isRoot reports whether p is the mount point itself in storage-list mode.
func (f *FS) isRoot(p string) bool {
	rel := strings.Trim(path.Clean("/"+p), "/")
	return f.rootAdapter == "" && (rel == "" || rel == ".")
}

// ─────────────────────────── metadata ───────────────────────────

// Stat returns metadata for a mount-relative path.
func (f *FS) Stat(ctx context.Context, p string) (*cliclient.Stat, error) {
	if f.isRoot(p) {
		return &cliclient.Stat{Name: "/", IsDir: true}, nil
	}
	if e, ok := f.cachedAttr(p); ok {
		return e.stat, nil
	}
	remote, _ := f.remoteOf(p)

	// A storage root has no parent listing to look in, so it is stat'ed by
	// listing it: a storage that lists is a storage that exists.
	if rp, err := cliclient.ParseRemotePath(remote); err == nil && rp.IsRoot() {
		if _, err := f.cfg.Client.ListStats(ctx, remote); err != nil {
			return nil, err
		}
		st := &cliclient.Stat{Name: rp.Adapter, IsDir: true}
		f.putAttr(p, st, nil)
		return st, nil
	}

	st, err := f.cfg.Client.StatPath(ctx, remote)
	if err != nil {
		return nil, err
	}
	f.putAttr(p, st, nil)
	return st, nil
}

// ReadDir lists a directory.
func (f *FS) ReadDir(ctx context.Context, p string) ([]*cliclient.Stat, error) {
	if e, ok := f.cachedAttr(p); ok && e.dir != nil {
		return e.dir, nil
	}

	var (
		out []*cliclient.Stat
		err error
	)
	if f.isRoot(p) {
		// The storage list. `List("")` is the server's "what can I see" view.
		var res *cliclient.ListResult
		res, err = f.cfg.Client.List(ctx, "")
		if err == nil {
			for _, name := range res.Storages {
				out = append(out, &cliclient.Stat{Name: name, IsDir: true})
			}
		}
	} else {
		remote, _ := f.remoteOf(p)
		out, err = f.cfg.Client.ListStats(ctx, remote)
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	self := &cliclient.Stat{Name: path.Base("/" + strings.Trim(p, "/")), IsDir: true}
	f.putAttr(p, self, out)
	// Children come free with the listing: a shell that stats every entry after
	// an `ls` would otherwise make one request per file.
	for _, c := range out {
		f.putAttr(path.Join(p, c.Name), c, nil)
	}
	return out, nil
}

func (f *FS) cachedAttr(p string) (attrEntry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.attrs[normalize(p)]
	if !ok || time.Since(e.at) > f.cfg.AttrTTL {
		return attrEntry{}, false
	}
	return e, true
}

func (f *FS) putAttr(p string, st *cliclient.Stat, dir []*cliclient.Stat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.attrs) > 8192 {
		// A crude bound: the entries are small and the working set is whatever
		// the user is looking at, so dropping everything beats tracking an LRU.
		f.attrs = map[string]attrEntry{}
	}
	f.attrs[normalize(p)] = attrEntry{stat: st, dir: dir, at: time.Now()}
}

// Forget drops cached metadata for a path and its parent.
//
// ⚠ The PARENT too, and that is the half people forget: after a create or a
// delete the parent's LISTING is what is wrong, and a stale listing is how a
// file the user just made does not appear in `ls`.
func (f *FS) Forget(p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.attrs, normalize(p))
	delete(f.attrs, normalize(path.Dir(strings.TrimSuffix(normalize(p), "/"))))
}

func normalize(p string) string {
	c := path.Clean("/" + strings.Trim(p, "/"))
	if c == "/." {
		return "/"
	}
	return c
}

// ─────────────────────────── mutation ───────────────────────────

var errReadOnly = errors.New("mountfs: this mount is read-only")

// Mkdir creates a directory.
func (f *FS) Mkdir(ctx context.Context, p string) error {
	if f.cfg.ReadOnly {
		return errReadOnly
	}
	if f.isRoot(p) {
		return os.ErrPermission
	}
	remote, _ := f.remoteOf(p)
	if _, err := f.cfg.Client.Mkdir(ctx, remote); err != nil {
		return err
	}
	f.Forget(p)
	return nil
}

// Remove deletes a file or directory.
//
// ⚠ It goes to the filex TRASH, like every other surface — the server decides
// that, not this client. Worth knowing when a user "deletes" from a mount and
// expects the space back immediately.
func (f *FS) Remove(ctx context.Context, p string) error {
	if f.cfg.ReadOnly {
		return errReadOnly
	}
	if f.isRoot(p) {
		return os.ErrPermission
	}
	remote, _ := f.remoteOf(p)
	if _, err := f.cfg.Client.Remove(ctx, remote); err != nil {
		return err
	}
	f.blocks.dropFile(remote)
	f.Forget(p)
	return nil
}

// Rename moves a path.
func (f *FS) Rename(ctx context.Context, oldPath, newPath string) error {
	if f.cfg.ReadOnly {
		return errReadOnly
	}
	if f.isRoot(oldPath) || f.isRoot(newPath) {
		return os.ErrPermission
	}
	src, _ := f.remoteOf(oldPath)
	dst, _ := f.remoteOf(newPath)
	if _, _, err := f.cfg.Client.Move(ctx, src, dst); err != nil {
		return err
	}
	f.blocks.dropFile(src)
	f.Forget(oldPath)
	f.Forget(newPath)
	return nil
}

// ─────────────────────────── reading ───────────────────────────

// ReadAt fills p from the remote file at off, through the block cache.
func (f *FS) ReadAt(ctx context.Context, mountPath string, dst []byte, off int64, size int64) (int, error) {
	remote, _ := f.remoteOf(mountPath)
	if off >= size {
		return 0, io.EOF
	}
	n := 0
	for n < len(dst) && off+int64(n) < size {
		at := off + int64(n)
		blk, err := f.block(ctx, remote, at/f.cfg.BlockSize, size)
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		start := at % f.cfg.BlockSize
		if start >= int64(len(blk)) {
			break
		}
		copied := copy(dst[n:], blk[start:])
		if copied == 0 {
			break
		}
		n += copied
	}
	if n == 0 {
		return 0, io.EOF
	}
	return n, nil
}

// block returns one cached block.
func (f *FS) block(ctx context.Context, remote string, idx, size int64) ([]byte, error) {
	if b, ok := f.blocks.get(remote, idx); ok {
		return b, nil
	}
	off := idx * f.cfg.BlockSize
	length := f.cfg.BlockSize
	if off+length > size {
		length = size - off
	}
	if length <= 0 {
		return nil, io.EOF
	}
	data, err := f.cfg.Client.ReadRange(ctx, remote, off, length)
	if err != nil {
		return nil, err
	}
	f.blocks.put(remote, idx, data)
	return data, nil
}

// Upload writes a whole local file to a remote path.
func (f *FS) Upload(ctx context.Context, mountPath, localPath string) error {
	if f.cfg.ReadOnly {
		return errReadOnly
	}
	remote, _ := f.remoteOf(mountPath)
	if _, _, err := f.cfg.Client.Upload(ctx, localPath, remote); err != nil {
		return err
	}
	f.blocks.dropFile(remote)
	f.Forget(mountPath)
	return nil
}

// SpoolPath returns a scratch file for a write in flight.
func (f *FS) SpoolPath() (*os.File, error) {
	return os.CreateTemp(f.cfg.SpoolDir, "filex-mount-*")
}

// MaxSpool is the write ceiling.
func (f *FS) MaxSpool() int64 { return f.cfg.MaxSpool }

// Describe is what the command prints when the mount comes up.
func (f *FS) Describe() string {
	target := "every storage you can see"
	if f.rootAdapter != "" {
		target = f.rootAdapter + "://" + f.rootRel
	}
	mode := "read/write"
	if f.cfg.ReadOnly {
		mode = "read-only"
	}
	return fmt.Sprintf("%s (%s)", target, mode)
}
