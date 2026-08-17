package nfssrv

import (
	"context"
	"log/slog"
	"net"
	"sync"

	billy "github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"
	nfshelper "github.com/willscott/go-nfs/helpers"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// The mount handshake, which is where the whole identity model lives.

// handleLimit bounds the file-handle cache the library keeps. Handles are how
// NFS refers to files across requests, so a limit that is too small makes a
// client's open file "disappear" mid-transfer.
const handleLimit = 1 << 16

// newHandler builds the go-nfs handler, wrapped in the library's own handle
// cache. The cache is not optional: NFS is handle-based, and a handler with no
// ToHandle/FromHandle cannot answer a second request about the same file.
func newHandler(s *Server) nfs.Handler {
	return nfshelper.NewCachingHandler(&handler{srv: s}, handleLimit)
}

// denied is the filesystem a refused mount is answered with.
//
// It is a real object with nothing in it, shared by every refusal so a
// probing client cannot grow the handle cache one entry per guess. Nothing is
// reachable through it: the status the client receives is a refusal, so the
// handle is never even written to the response.
var denied billy.Filesystem = &deniedFS{}

type handler struct {
	srv *Server

	mu     sync.Mutex
	mounts map[int64]*mount
}

// mount is one live export: its filesystem, its principal and the export row it
// came from. Keyed by export id, see Mount.
type mount struct {
	fs        *fs
	principal *protocolauth.Principal
	export    *model.NFSExport
}

// Mount resolves the export path into a filesystem.
//
// ⚠⚠ This is the ONLY authentication point in the protocol, and it is the whole
// design: the path carries 32 bytes of entropy, and what comes back is a
// filesystem already bound to one principal, one tenant scope, one ACL set and
// one confinement. Nothing downstream re-derives identity, because there is
// nothing downstream to derive it from — NFS requests carry no credential.
func (h *handler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (nfs.MountStatus, billy.Filesystem, []nfs.AuthFlavor) {
	remote := remoteIP(conn)
	p, export, err := h.srv.cfg.Auth.Export(ctx, string(req.Dirpath), remote)
	if err != nil {
		// ⚠ One status for every failure — unknown path, expired export,
		// disabled account, address not in the allow-list. Distinguishing them
		// would let somebody probe for valid export paths.
		slog.Debug("nfs: mount refused",
			slog.String("remote", addrOf(conn)),
			slog.String("path", string(req.Dirpath)))
		// ⚠⚠ A refusal returns `denied`, NOT nil — and this is a crash, not a
		// style point. go-nfs calls ToHandle(handle, …) UNCONDITIONALLY, before
		// it looks at the status (mount.go:42), so a nil filesystem is
		// dereferenced inside the library and the panic takes the whole process
		// with it: any stranger who probes one wrong export path stops filex
		// for everybody. Measured 2026-08-16, on the first refused mount the
		// test suite attempted.
		return nfs.MountStatusErrAcces, denied, nil
	}

	// ⚠ One filesystem per EXPORT, not per mount call.
	//
	// Nothing tells this handler when a client unmounts — go-nfs answers UMNT
	// itself and never calls back (mount.go:51) — so anything created per mount
	// call is created and never released. Keying on the export bounds it by the
	// number of exports instead of by the number of times somebody typed
	// `mount`, and it means remounting the same export keeps its file handles
	// valid. Safe because the identity is the export: an export cannot be edited
	// in place, only disabled or deleted, and both of those are what `revoked`
	// below is for.
	h.mu.Lock()
	if h.mounts == nil {
		h.mounts = map[int64]*mount{}
	}
	m, ok := h.mounts[export.ID]
	if !ok || m.fs.revoked() {
		fsys := newFS(h.srv, p, export)
		// Registered with NO closer: there is nothing to close (see fs.live).
		// The registry's job here is to flip the flag every operation consults,
		// so that deleting an export stops the mount that export opened instead
		// of only the next one.
		fsys.live = h.srv.cfg.Auth.Enter(p, "nfs", addrOf(conn), export.Label, nil)
		m = &mount{fs: fsys, principal: p, export: export}
		h.mounts[export.ID] = m
	}
	fsys := m.fs
	h.mu.Unlock()

	slog.Info("nfs: mounted",
		slog.String("user", p.User.Email),
		slog.String("remote", addrOf(conn)),
		slog.String("label", export.Label),
		slog.Bool("read_only", export.ReadOnly))

	// ⚠ AuthFlavorNull, not AuthFlavorUnix. Advertising UNIX auth would tell
	// the client its uid/gid means something here; it does not — the mount is
	// already bound to an account, and every uid on the wire is discarded.
	return nfs.MountStatusOk, fsys, []nfs.AuthFlavor{nfs.AuthFlavorNull}
}

// Change exposes the mutating half. Returning nil marks the export read-only,
// which is how an export with ReadOnly set refuses writes at the protocol level
// rather than one error at a time.
func (h *handler) Change(fsys billy.Filesystem) billy.Change {
	f, ok := fsys.(*fs)
	if !ok || f.readOnly {
		return nil
	}
	return f
}

// FSStat fills in the free-space numbers a client shows in its file manager.
func (h *handler) FSStat(ctx context.Context, fsys billy.Filesystem, st *nfs.FSStat) error {
	f, ok := fsys.(*fs)
	if !ok {
		return nil
	}
	return f.fsStat(ctx, st)
}

// The handle cache supplies these; the wrapper in newHandler is what actually
// answers them.
func (h *handler) ToHandle(billy.Filesystem, []string) []byte { return nil }
func (h *handler) FromHandle([]byte) (billy.Filesystem, []string, error) {
	return nil, nil, &nfs.NFSStatusError{NFSStatus: nfs.NFSStatusStale}
}
func (h *handler) InvalidateHandle(billy.Filesystem, []byte) error { return nil }
func (h *handler) HandleLimit() int                                { return handleLimit }

func remoteIP(conn net.Conn) net.IP {
	if conn == nil {
		return nil
	}
	if a, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		return a.IP
	}
	host, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

func addrOf(conn net.Conn) string {
	if conn == nil {
		return ""
	}
	return conn.RemoteAddr().String()
}
