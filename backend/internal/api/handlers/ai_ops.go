package handlers

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// aiOps is the storage-facing core shared by the AI REST handler and the
// MCP server. It owns no HTTP concerns — every method takes/returns plain
// Go values so the REST layer and the MCP tool layer stay thin adapters.
//
// Paths use the same `adapter://relative/path` wire form as the rest of
// filex (adapter == storage name). An empty/relative path defaults to the
// first enabled storage at its root.
type aiOps struct {
	store      db.Store
	resolver   func(int64) (storage.Driver, error)
	share      *share.Service  // optional — nil disables file_share/unshare
	publicURL  string          // base for /s/<token> links
	convertURL string          // external converter URL (empty = not configured)
	acl        *acl.Resolver   // RBAC — nil disables per-user grant enforcement
	thumbs     *thumb.Pipeline // optional — nil skips thumbnail dispatch (manager-upload parity)
	origin     string          // writehook origin stamp — "ai" by default, "sharex" for the ShareX wrapper
	// staged, when wired, takes writes above the chunk threshold into filex's
	// staging area and lets the ops worker move them to the driver. nil keeps
	// the synchronous write, so an instance without staging is unaffected.
	staged *StagedUpload
	// body resolves where a file's bytes are: the driver, or filex's staging
	// area while a staged upload is still transferring. Nil-safe.
	body *filebody.Resolver
	// tickets, when wired, lets this surface mint credential-free upload URLs
	// for files too large to travel inside a tool call (see upload_ticket.go).
	tickets *uploadTicketStore
}

// attachBody wires the byte-source resolver so the AI/REST read and zip
// surfaces serve a file that is still being transferred.
func (a *aiOps) attachBody(b *filebody.Resolver) { a.body = b }

// allow reports whether the bound user has at least `need` on rel within s.
// The AI surface bypasses confine.Middleware and manager gating, so every op
// routes through resolveStorage / these asserts. nil resolver (unwired) allows.
func (a *aiOps) allow(ctx context.Context, s *model.Storage, rel string, need acl.Level) bool {
	if a.acl == nil {
		return true
	}
	set, err := a.acl.LoadSet(ctx, auth.UserFrom(ctx), s)
	if err != nil || set == nil {
		return false
	}
	return set.Effective(rel) >= need
}

func newAIOps(store db.Store, resolver func(int64) (storage.Driver, error), shareSvc *share.Service, publicURL, convertURL string) *aiOps {
	return &aiOps{store: store, resolver: resolver, share: shareSvc, publicURL: publicURL, convertURL: convertURL, origin: writehook.OriginAI}
}

// aiEntry is the JSON-shaped directory/file row returned to AI callers.
type aiEntry struct {
	Path         string `json:"path"` // adapter://rel
	Name         string `json:"name"` // basename
	Type         string `json:"type"` // "file" | "dir"
	Size         int64  `json:"size"`
	Mime         string `json:"mime,omitempty"`
	LastModified int64  `json:"last_modified,omitempty"` // unix millis
}

// errAINoStorage is returned when no storage is configured / resolvable.
var errAINoStorage = errors.New("no storage configured")

// errAIForbidden is returned when the bound user lacks the required grant level
// for a mutating AI op (read denials surface from resolveStorage instead).
var errAIForbidden = errors.New("access denied: insufficient permission")

// deniedErr carries a caller-facing message while still matching a sentinel via
// errors.Is, so aiStatus can map denials to 403. Without it a confinement or
// permission refusal falls through to mapDriverErr's 500 default — and a 5xx
// tells automation "server glitch, retry" when the refusal is in fact permanent.
type deniedErr struct {
	error
	sentinel error
}

func (e deniedErr) Unwrap() error { return e.sentinel }

// denied wraps msg so that errors.Is(err, sentinel) holds.
func denied(sentinel error, format string, args ...any) error {
	return deniedErr{error: fmt.Errorf(format, args...), sentinel: sentinel}
}

// resolveStorage maps an adapter://path to (storage, relativePath). When the
// path carries no adapter prefix the first enabled storage is used.
func (a *aiOps) resolveStorage(ctx context.Context, p string) (*model.Storage, string, error) {
	// Honor a token's `root:` confinement ceiling. The AI surface bypasses
	// confine.Middleware, so enforce it here — the single chokepoint every op
	// routes through.
	if root, ok := confine.RootFromToken(ctx); ok {
		// A confined caller treats its root as "/": an adapter-less (bare) path
		// is interpreted relative to the root, so mkdir("sub") lands INSIDE the
		// root — not the storage root. Fully-qualified adapter://… paths are
		// validated as-is. (Empty path → the root itself.)
		if !strings.Contains(p, "://") {
			rel := strings.Trim(strings.TrimSpace(p), "/")
			base := root.Adapter + "://" + root.Rel
			if root.Rel == "" {
				base = root.Adapter + "://"
			}
			if rel == "" {
				p = base
			} else {
				p = strings.TrimRight(base, "/") + "/" + rel
			}
		}
		np, err := root.EnforcePath(p)
		if err != nil {
			q := root.Adapter + "://" + root.Rel
			return nil, "", denied(confine.ErrOutOfRoot, "%q is outside your confined root %s — use a bare relative path (e.g. \"sub/file.txt\") or a path under %s (call file_root to see your root)", p, q, q)
		}
		p = np
	}
	storages, err := a.store.ListEnabledStorages(ctx)
	if err != nil {
		return nil, "", err
	}
	if len(storages) == 0 {
		return nil, "", errAINoStorage
	}
	adapter, rel := splitAdapterPath(p)
	if adapter == "" {
		adapter = storages[0].Name
	}
	for _, s := range storages {
		if s.Name == adapter {
			if pathHasDotDot(rel) {
				return nil, "", errors.New("bad path")
			}
			clean := strings.Trim(rel, "/")
			// RBAC read floor: the bound user needs ≥viewer on the path. This is
			// the single chokepoint for the AI surface (it bypasses the /api/files
			// confine + ACL gating), so reads are denied here and writes assert
			// ≥editor in their own methods.
			if !a.allow(ctx, s, clean, acl.LevelViewer) {
				return nil, "", denied(errAIForbidden, "access denied: no permission for %s", joinAdapterPath(s.Name, clean))
			}
			return s, clean, nil
		}
	}
	return nil, "", fmt.Errorf("unknown storage: %s", adapter)
}

// aiRootInfo describes a token's effective access scope — its confinement root
// (if any) and the storage adapters it can address. The AI surface exposes it
// (GET /api/ai/root + the file_root MCP tool) so a confined agent learns where
// it is instead of guessing adapter names and paths.
type aiRootInfo struct {
	Confined bool     `json:"confined"`
	Root     string   `json:"root,omitempty"` // qualified adapter://rel
	Adapter  string   `json:"adapter,omitempty"`
	Storages []string `json:"storages"`          // addressable adapter names
	Convert  string   `json:"convert,omitempty"` // external converter URL (empty = unavailable)
	Hint     string   `json:"hint"`
}

// RootInfo reports the caller's confinement root + reachable storages.
func (a *aiOps) RootInfo(ctx context.Context) aiRootInfo {
	info := aiRootInfo{Storages: []string{}}
	if storages, err := a.store.ListEnabledStorages(ctx); err == nil {
		user := auth.UserFrom(ctx)
		for _, s := range storages {
			// RBAC: only advertise storages the bound user can see.
			if a.acl != nil {
				if set, _ := a.acl.LoadSet(ctx, user, s); set == nil || !set.StorageVisible() {
					continue
				}
			}
			info.Storages = append(info.Storages, s.Name)
		}
	}
	if root, ok := confine.RootFromToken(ctx); ok {
		info.Confined = true
		info.Adapter = root.Adapter
		info.Root = root.Adapter + "://" + root.Rel
		info.Storages = []string{root.Adapter}
		info.Hint = "You are confined to " + info.Root + ". Use bare relative paths (e.g. \"sub/file.txt\") — they resolve UNDER this root — or full \"" + info.Root + "/...\" paths. Anything outside is rejected; an empty path = your root."
	} else {
		first := ""
		if len(info.Storages) > 0 {
			first = info.Storages[0]
		}
		info.Hint = "Full access. Address files as \"<adapter>://<path>\" using a storage listed above; an empty path uses the first storage (" + first + ")."
	}
	// Conversion is NOT a server-side MCP operation — it runs in an external
	// converter. Surface the URL (when configured) so the agent points the user
	// there instead of trying a non-existent file_convert tool.
	if a.convertURL != "" {
		info.Convert = a.convertURL
		info.Hint += " File conversion is not a server-side MCP operation: it runs in the external converter at " + a.convertURL + " (use the filex UI's Convert action)."
	} else {
		info.Hint += " File conversion is not a server-side MCP operation; it runs in an external converter (admin → External services / Dış servisler) — none is configured here."
	}
	return info
}

// List returns the directory entries under `p`. Driver-direct (not cache)
// so freshly-written files show immediately.
func (a *aiOps) List(ctx context.Context, p string) ([]aiEntry, error) {
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, err
	}
	drv, err := a.resolver(s.ID)
	if err != nil {
		return nil, err
	}
	objs, err := drv.List(ctx, rel)
	if err != nil {
		return nil, err
	}
	out := make([]aiEntry, 0, len(objs))
	for _, o := range objs {
		if o.Name == ".filex-trash" || strings.Contains(o.Path, ".filex-trash") ||
			strings.Contains(o.Path, ".thumbs") || o.Name == ".keepdir" {
			continue
		}
		objRel := o.Path
		if objRel == "" {
			objRel = path.Join(rel, o.Name)
		}
		typ := "file"
		if o.Kind == storage.KindDirectory {
			typ = "dir"
		}
		e := aiEntry{
			Path: joinAdapterPath(s.Name, objRel),
			Name: o.Name,
			Type: typ,
			Size: o.Size,
			Mime: o.Mime,
		}
		if !o.Mtime.IsZero() {
			e.LastModified = o.Mtime.UnixMilli()
		}
		out = append(out, e)
	}
	return out, nil
}

// Info stats a single path and returns its metadata.
func (a *aiOps) Info(ctx context.Context, p string) (*aiEntry, error) {
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return nil, errors.New("path required")
	}
	drv, err := a.resolver(s.ID)
	if err != nil {
		return nil, err
	}
	o, err := drv.Stat(ctx, rel)
	if err != nil {
		return nil, err
	}
	typ := "file"
	if o.Kind == storage.KindDirectory {
		typ = "dir"
	}
	e := &aiEntry{
		Path: joinAdapterPath(s.Name, rel),
		Name: path.Base(rel),
		Type: typ,
		Size: o.Size,
		Mime: o.Mime,
	}
	if !o.Mtime.IsZero() {
		e.LastModified = o.Mtime.UnixMilli()
	}
	return e, nil
}

// Read streams the bytes of a file. The caller closes the returned reader.
// Also returns the resolved mime + size for header population.
func (a *aiOps) Read(ctx context.Context, p string) (io.ReadCloser, string, int64, error) {
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, "", 0, err
	}
	if rel == "" {
		return nil, "", 0, errors.New("path required")
	}
	drv, err := a.resolver(s.ID)
	if err != nil {
		return nil, "", 0, err
	}
	src, err := a.body.Resolve(ctx, drv, s.ID, rel, nil)
	if err != nil {
		return nil, "", 0, err
	}
	st, err := src.Stat(ctx)
	if err != nil {
		return nil, "", 0, err
	}
	if st.Kind == storage.KindDirectory {
		return nil, "", 0, errors.New("is a directory")
	}
	mime := mimeByExt(rel)
	if mime == "" {
		mime = st.Mime
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	rc, err := src.Open(ctx)
	if err != nil {
		return nil, "", 0, err
	}
	return rc, mime, st.Size, nil
}

// ReadBytes is a convenience for MCP (returns the full file content). A
// hard cap protects against streaming a multi-GB blob into a JSON-RPC
// response — callers above that limit should use the REST download stream.
const aiMaxReadBytes = 8 << 20 // 8 MiB

func (a *aiOps) ReadBytes(ctx context.Context, p string) ([]byte, string, error) {
	rc, mime, size, err := a.Read(ctx, p)
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	if size > aiMaxReadBytes {
		return nil, "", fmt.Errorf("file too large for inline read (%d bytes > %d); use the download endpoint", size, aiMaxReadBytes)
	}
	b, err := io.ReadAll(io.LimitReader(rc, aiMaxReadBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > aiMaxReadBytes {
		return nil, "", fmt.Errorf("file too large for inline read (> %d bytes); use the download endpoint", aiMaxReadBytes)
	}
	return b, mime, nil
}

// Write creates or overwrites a file with the given bytes and mirrors the
// result into the DB cache so it lists immediately. Returns the new entry.
//
// It is WriteStream over a byte slice — kept because most agent writes really
// are small in-memory payloads (a JSON blob, a note), and a caller with bytes
// in hand should not have to build a reader.
func (a *aiOps) Write(ctx context.Context, p string, data []byte) (*aiEntry, error) {
	return a.WriteStream(ctx, p, bytes.NewReader(data), int64(len(data)))
}

// WriteStream creates or overwrites a file from a stream of exactly size bytes.
//
// Above the staging threshold the bytes go into filex's own staging area and
// the driver write is handed to the ops worker, so the caller's request returns
// as soon as filex holds the data instead of waiting out a slow backend. Below
// it — and on any instance without staging configured — this is the same
// synchronous write it always was.
//
// ⚠ src is read at most once. On the staged path the body is consumed into
// staging, so a failure there must NOT fall back to the synchronous write:
// the reader is already drained and the file would land truncated.
func (a *aiOps) WriteStream(ctx context.Context, p string, src io.Reader, size int64) (*aiEntry, error) {
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return nil, errors.New("path required")
	}
	if s.ReadOnly {
		return nil, storage.ErrReadOnly
	}
	if !a.allow(ctx, s, rel, acl.LevelEditor) {
		return nil, errAIForbidden
	}
	name := path.Base(rel)
	if name == "" || name == "." || name == "/" {
		return nil, errors.New("bad filename")
	}
	drv, err := a.resolver(s.ID)
	if err != nil {
		return nil, err
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		return nil, storage.ErrUnsupported
	}
	// A caller that passes a FOLDER as `path` used to get a file written at
	// that exact key, leaving `X` and `X/…` side by side on an object store.
	// See storage.ErrKindConflict for what that did to the DR mirror.
	if err := storage.EnsureFileTarget(ctx, drv, rel); err != nil {
		return nil, err
	}

	if a.staged.ShouldStage(size) {
		node, serr := a.staged.IngestStream(ctx, s.ID, rel, src, size, currentUserID(ctx), "")
		switch {
		case serr == nil:
			return &aiEntry{
				Path:         joinAdapterPath(s.Name, rel),
				Name:         name,
				Type:         "file",
				Size:         size,
				Mime:         node.Mime,
				LastModified: time.Now().UnixMilli(),
			}, nil
		case !errors.Is(serr, ErrStagingUnavailable):
			return nil, serr
		}
		// ErrStagingUnavailable is refused before a byte of src is read, so the
		// synchronous path below still sees the whole body.
	}

	// Sniff the head, then hand the driver the ORIGINAL reader when it can
	// rewind. Wrapping a seekable body in io.MultiReader destroys the Seeker,
	// which is what once put every upload on the chunked path with no
	// Content-Length (olivov H1, 2026-08-05).
	var sniff [512]byte
	n, _ := io.ReadFull(src, sniff[:])
	mime := ""
	if n > 0 {
		mime = storage.RefineOfficeMime(http.DetectContentType(sniff[:n]), name)
	}
	body := io.Reader(io.MultiReader(bytes.NewReader(sniff[:n]), src))
	if sk, ok := src.(io.Seeker); ok && n > 0 {
		if _, serr := sk.Seek(0, io.SeekStart); serr == nil {
			body = src
		}
	}

	if err := wr.Write(ctx, rel, body, size); err != nil {
		return nil, err
	}

	a.cacheUpsertFile(ctx, s, rel, size, mime)

	return &aiEntry{
		Path:         joinAdapterPath(s.Name, rel),
		Name:         name,
		Type:         "file",
		Size:         size,
		Mime:         mime,
		LastModified: time.Now().UnixMilli(),
	}, nil
}

// Delete soft-deletes a file or folder (rename into .filex-trash, flip the
// cache row's deleted_at) mirroring the SFC's vfDelete contract — so AI deletes
// land in the same trash the UI restores from.
//
// Object stores (S3) have no real object at a folder prefix, so a plain
// Move/Copy of the folder path 404s ("CopyObject 404"). For folders we walk the
// prefix and trash each file individually, preserving sub-structure.
func (a *aiOps) Delete(ctx context.Context, p string) error {
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return err
	}
	if rel == "" {
		return errors.New("path required")
	}
	if s.ReadOnly {
		return storage.ErrReadOnly
	}
	if !a.allow(ctx, s, rel, acl.LevelEditor) {
		return errAIForbidden
	}
	drv, err := a.resolver(s.ID)
	if err != nil {
		return err
	}
	base := path.Base(rel)

	// trash.Put is the shared implementation behind every delete surface: it
	// renames the object into `.filex-trash/`, walks the prefix per-object when
	// an object store has nothing at the folder path, and falls back to
	// Copy+Delete on drivers without Move. It never destroys data — when it
	// cannot preserve the bytes it says so, and only then do we hard delete.
	//
	// The hook now follows what actually happened. It used to be decided ahead
	// of time, so a driver with Delete but no Move permanently erased a
	// folder's contents while still reporting OnFileTrashed and retagging the
	// row into the trash — the UI offered a Restore for bytes long gone.
	out, terr := trash.Put(ctx, drv, rel)
	switch {
	case terr == nil && out.Trashed:
		a.trashRetagCache(ctx, s.ID, rel, out.Key)
		/* bag:b3 event */
		writehook.OnFileTrashed(ctx, s.ID, normalizeDBPath(rel), base, normalizeDBPath(out.Key), a.origin)
		return nil

	case terr == nil && out.Missing:
		// Nothing was there to keep (an empty folder marker, or a stale cache
		// row). Drop the row rather than parking an unrestorable trash entry.
		a.dropCacheRow(ctx, s.ID, rel)
		/* bag:b3 event */
		writehook.OnFileDeleted(ctx, s.ID, normalizeDBPath(rel), base, a.origin)
		return nil

	case errors.Is(terr, trash.ErrUnsupported):
		deleter, ok := drv.(storage.Deleter)
		if !ok {
			return storage.ErrUnsupported
		}
		if err := deleter.Delete(ctx, rel); err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		a.dropCacheRow(ctx, s.ID, rel)
		/* bag:b3 event */
		writehook.OnFileDeleted(ctx, s.ID, normalizeDBPath(rel), base, a.origin)
		return nil

	default:
		return terr
	}
}

// dropCacheRow removes the node row for rel outright. Used when the bytes are
// gone for good, so the trash listing never offers a Restore that cannot work.
func (a *aiOps) dropCacheRow(ctx context.Context, storageID int64, rel string) {
	origHash := pathkey.Hash(storageID, normalizeDBPath(rel))
	if existing, gerr := a.store.GetNodeByPath(ctx, storageID, origHash); gerr == nil && existing != nil {
		_ = a.store.HardDeleteNode(ctx, existing.ID)
	}
}

// listAllFiles recursively returns every FILE object path under root (skipping
// trash / thumbnail internals). Empty when root is a file or has no children.
func (a *aiOps) listAllFiles(ctx context.Context, drv storage.Driver, root string) ([]string, error) {
	var out []string
	var walk func(dir string) error
	walk = func(dir string) error {
		objs, err := drv.List(ctx, dir)
		if err != nil {
			return err
		}
		for _, o := range objs {
			if o.Name == ".filex-trash" || o.Name == ".thumbs" {
				continue
			}
			switch o.Kind {
			case storage.KindDirectory:
				if err := walk(o.Path); err != nil {
					return err
				}
			case storage.KindFile:
				out = append(out, o.Path)
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return out, nil
}

// trashRetagCache soft-deletes the cache node at rel and retags it to its trash
// location so Restore can find it and a fresh write at the original path works.
func (a *aiOps) trashRetagCache(ctx context.Context, storageID int64, rel, trashRel string) {
	origClean := normalizeDBPath(rel)
	origHash := pathkey.Hash(storageID, origClean)
	if existing, gerr := a.store.GetNodeByPath(ctx, storageID, origHash); gerr == nil && existing != nil {
		newClean := normalizeDBPath(trashRel)
		newHash := pathkey.Hash(storageID, newClean)
		_ = a.store.SoftDeleteAndRetag(ctx, existing.ID, newClean, newHash, origClean)
	}
}

// Move renames/moves src to dst within the same storage.
func (a *aiOps) Move(ctx context.Context, src, dst string) (*aiEntry, error) {
	sSrc, relSrc, err := a.resolveStorage(ctx, src)
	if err != nil {
		return nil, err
	}
	sDst, relDst, err := a.resolveStorage(ctx, dst)
	if err != nil {
		return nil, err
	}
	if relSrc == "" || relDst == "" {
		return nil, errors.New("src and dst required")
	}
	if sSrc.ReadOnly {
		return nil, storage.ErrReadOnly
	}
	if !a.allow(ctx, sSrc, relSrc, acl.LevelEditor) || !a.allow(ctx, sDst, relDst, acl.LevelEditor) {
		return nil, errAIForbidden
	}
	/* wiring:e2 — the AI/MCP surface obeys the same encryption boundary as
	 * the web UI. `dst` here is a full path (move is also rename), so the
	 * destination DIRECTORY is what the guard is asked about. */
	if lk, ok := a.store.(e2e.NodeByPathLookup); ok {
		dstDir := path.Dir(relDst)
		if dstDir == "." {
			dstDir = ""
		}
		if err := e2e.GuardTransfer(ctx, lk, sSrc.ID, []string{relSrc}, sDst.ID, dstDir); err != nil {
			return nil, err
		}
	}

	if sSrc.ID != sDst.ID {
		// Two storages have no rename between them, so the bytes travel — the
		// same engine the queue uses for a cross-depo paste, deliberately not a
		// second copy of it (ops.Transfer).
		return a.moveAcross(ctx, sSrc, relSrc, sDst, relDst)
	}
	drv, err := a.resolver(sSrc.ID)
	if err != nil {
		return nil, err
	}
	mv, ok := drv.(storage.Mover)
	if !ok {
		return nil, storage.ErrUnsupported
	}
	if err := mv.Move(ctx, relSrc, relDst); err != nil {
		return nil, err
	}
	a.cacheMove(ctx, sSrc, relSrc, relDst)
	/* bag:b3 event */
	writehook.OnFileMoved(ctx, sSrc.ID, normalizeDBPath(relSrc), normalizeDBPath(relDst), path.Base(relDst), a.origin)
	return &aiEntry{
		Path: joinAdapterPath(sDst.Name, relDst),
		Name: path.Base(relDst),
		Type: "file",
	}, nil
}

// moveAcross carries src to another storage and then removes the original.
//
// The order is the point: the source is deleted only after every file has been
// written AND stat-verified on the far side (ops.Transfer's contract), so a
// transfer that fails leaves the original where it was. Like the queue's
// cross-storage move — and unlike a same-storage delete — the source does NOT
// go through the trash: moving between depolar is done to free the first one.
func (a *aiOps) moveAcross(ctx context.Context, sSrc *model.Storage, relSrc string, sDst *model.Storage, relDst string) (*aiEntry, error) {
	if sDst.ReadOnly {
		return nil, storage.ErrReadOnly
	}
	srcDrv, err := a.resolver(sSrc.ID)
	if err != nil {
		return nil, err
	}
	dstDrv, err := a.resolver(sDst.ID)
	if err != nil {
		return nil, err
	}
	del, ok := srcDrv.(storage.Deleter)
	if !ok {
		return nil, fmt.Errorf("%s cannot delete, so a move out of it would leave a duplicate: copy instead", sSrc.Name)
	}

	hooks := ops.TransferHooks{
		OnDir:  func(_, dst string) { a.cacheUpsertDir(ctx, sDst, dst) },
		OnFile: func(_, dst string, size int64) { a.cacheUpsertFile(ctx, sDst, dst, size, "") },
	}
	if err := ops.Transfer(ctx, srcDrv, dstDrv, relSrc, relDst, hooks); err != nil {
		return nil, err
	}

	// Cache rows for the source side go before the bytes: a row pointing at a
	// path that is about to disappear is what makes a listing show a file that
	// is not there.
	if files, lerr := a.listAllFiles(ctx, srcDrv, relSrc); lerr == nil {
		for _, f := range files {
			a.dropCacheRow(ctx, sSrc.ID, f)
		}
	}
	a.dropCacheRow(ctx, sSrc.ID, relSrc)
	if err := del.Delete(ctx, relSrc); err != nil {
		return nil, fmt.Errorf("copied to %s, but deleting the source failed: %w", sDst.Name, err)
	}
	/* bag:b3 event — written on the far side, gone on this one */
	writehook.OnFileDeleted(ctx, sSrc.ID, normalizeDBPath(relSrc), path.Base(relSrc), a.origin)
	return &aiEntry{
		Path: joinAdapterPath(sDst.Name, relDst),
		Name: path.Base(relDst),
		Type: "file",
	}, nil
}

// Mkdir creates a directory at `p` and mirrors it into the cache.
func (a *aiOps) Mkdir(ctx context.Context, p string) (*aiEntry, error) {
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return nil, errors.New("path required")
	}
	if s.ReadOnly {
		return nil, storage.ErrReadOnly
	}
	if !a.allow(ctx, s, rel, acl.LevelEditor) {
		return nil, errAIForbidden
	}
	drv, err := a.resolver(s.ID)
	if err != nil {
		return nil, err
	}
	mk, ok := drv.(storage.Mkdirer)
	if !ok {
		return nil, storage.ErrUnsupported
	}
	// The same collision from the other side: a folder opened on top of an
	// existing file name.
	if err := storage.EnsureDirTarget(ctx, drv, rel); err != nil {
		return nil, err
	}
	if err := mk.Mkdir(ctx, rel); err != nil {
		return nil, err
	}
	a.cacheUpsertDir(ctx, s, rel)
	return &aiEntry{
		Path: joinAdapterPath(s.Name, rel),
		Name: path.Base(rel),
		Type: "dir",
	}, nil
}

// Search runs a name/content search scoped to one storage (or all when the
// path has no adapter and multiple storages exist).
func (a *aiOps) Search(ctx context.Context, p, query string) ([]aiEntry, error) {
	s, _, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, err
	}
	root, confined := confine.RootFromToken(ctx)
	var set *acl.Set
	if a.acl != nil {
		set, _ = a.acl.LoadSet(ctx, auth.UserFrom(ctx), s)
	}
	rows, err := a.store.SearchNodes(ctx, s.ID, "%"+query+"%", 200)
	if err != nil {
		return nil, err
	}
	out := make([]aiEntry, 0, len(rows))
	for _, n := range rows {
		if n.DeletedAt != nil {
			continue
		}
		if confined && !root.Within(s.Name, n.Path) {
			continue // outside the token's confinement root
		}
		if set != nil && !set.CanSee(n.Path) {
			continue // outside the user's RBAC grants
		}
		typ := "file"
		if n.Type == model.NodeTypeDirectory {
			typ = "dir"
		}
		e := aiEntry{
			Path: joinAdapterPath(s.Name, n.Path),
			Name: n.Name,
			Type: typ,
			Size: n.Size,
			Mime: n.Mime,
		}
		if n.BackendMtime != nil {
			e.LastModified = n.BackendMtime.UnixMilli()
		}
		out = append(out, e)
	}
	return out, nil
}

// aiShareResult is the AI-surface share payload: a public link (+ optional PIN
// shown once) for a file or folder.
type aiShareResult struct {
	URL          string     `json:"url"`
	Token        string     `json:"token"`
	Path         string     `json:"path"`
	HasPin       bool       `json:"has_pin"`
	Pin          string     `json:"pin,omitempty"` // present ONLY when generated now
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	MaxDownloads *int       `json:"max_downloads,omitempty"`
}

// CreateShare mints a public share link for a file/folder. Honors the token's
// confinement root (the path is validated via resolveStorage). pin=true
// generates a random unlock PIN (returned ONCE); expiresInDays / maxDownloads
// are optional (0 = none). The target must be indexed (write or list it first).
func (a *aiOps) CreateShare(ctx context.Context, p string, pin bool, expiresInDays, maxDownloads int) (*aiShareResult, error) {
	if a.share == nil {
		return nil, errors.New("sharing is not enabled on this server")
	}
	s, rel, err := a.resolveStorage(ctx, p)
	if err != nil {
		return nil, err
	}
	if rel == "" {
		return nil, errors.New("share target path required (cannot share a storage root)")
	}
	node, err := a.store.GetNodeByPath(ctx, s.ID, pathkey.Hash(s.ID, rel))
	if err != nil || node == nil {
		return nil, fmt.Errorf("not indexed yet: %s — write or list it first so filex caches the entry", joinAdapterPath(s.Name, rel))
	}
	pinVal, pinGen := "", ""
	if pin {
		pinVal = randomPIN(8)
		pinGen = pinVal
	}
	var userID *int64
	if u := auth.UserFrom(ctx); u != nil {
		uid := u.ID
		userID = &uid
	}
	opts := share.CreateOpts{NodeID: node.ID, PIN: pinVal, CreatedBy: userID, CreatedVia: auth.TokenUserFrom(ctx)}
	if expiresInDays > 0 {
		t := time.Now().AddDate(0, 0, expiresInDays)
		opts.ExpiresAt = &t
	}
	if maxDownloads > 0 {
		opts.MaxDownloads = &maxDownloads
	}
	sh, err := a.share.Create(ctx, opts)
	if err != nil {
		return nil, err
	}
	url := "/s/" + sh.Token
	if base := strings.TrimRight(a.publicURL, "/"); base != "" {
		url = base + url
	}
	return &aiShareResult{
		URL:          url,
		Token:        sh.Token,
		Path:         joinAdapterPath(s.Name, node.Path),
		HasPin:       sh.PinHash != "",
		Pin:          pinGen,
		ExpiresAt:    sh.ExpiresAt,
		MaxDownloads: sh.MaxDownloads,
	}, nil
}

// RevokeShare revokes a share by its token. Only the share's creator (or an
// admin) may revoke it.
func (a *aiOps) RevokeShare(ctx context.Context, token string) error {
	if a.share == nil {
		return errors.New("sharing is not enabled on this server")
	}
	sh, err := a.store.GetShareByToken(ctx, token)
	if err != nil {
		return errors.New("share not found")
	}
	if u := auth.UserFrom(ctx); u != nil && !u.IsAdmin() && (sh.CreatedBy == nil || *sh.CreatedBy != u.ID) {
		return errors.New("forbidden: not your share")
	}
	return a.store.RevokeShare(ctx, sh.ID)
}

// ───── server-side zip / unzip ─────
//
// Both operations are SERVER-SIDE: the archive is assembled / extracted into
// the configured storage and only metadata (the dest entry / a file count)
// crosses the AI surface. Large archives never travel as a base64 blob over
// MCP — to hand a zip to someone, call CreateShare on the result.

// Zip packs one or more source paths (files or folders) into a new zip at dest.
// Every source AND the dest pass resolveStorage, so a confined token's root
// ceiling is enforced on each path. Folders are walked recursively via the
// driver's List/Read. All sources must live on the same storage as dest.
func (a *aiOps) Zip(ctx context.Context, sources []string, dest string) (*aiEntry, error) {
	if len(sources) == 0 {
		return nil, errors.New("at least one source path required")
	}
	sDest, relDest, err := a.resolveStorage(ctx, dest)
	if err != nil {
		return nil, err
	}
	if relDest == "" {
		return nil, errors.New("dest path required")
	}
	if sDest.ReadOnly {
		return nil, storage.ErrReadOnly
	}
	if !a.allow(ctx, sDest, relDest, acl.LevelEditor) {
		return nil, errAIForbidden
	}
	drvDest, err := a.resolver(sDest.ID)
	if err != nil {
		return nil, err
	}
	wr, ok := drvDest.(storage.Writer)
	if !ok {
		return nil, storage.ErrUnsupported
	}

	// archive/zip writes forward-only, so build into a tmp file then stream
	// the finished archive back into storage (archive.go Add pattern).
	tmp, err := os.CreateTemp("", "filex-ai-zip-*.zip")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	defer tmp.Close()

	zw := zip.NewWriter(tmp)
	seen := map[string]bool{}
	for _, src := range sources {
		sSrc, relSrc, rerr := a.resolveStorage(ctx, src)
		if rerr != nil {
			_ = zw.Close()
			return nil, rerr
		}
		if relSrc == "" {
			_ = zw.Close()
			return nil, errors.New("source path required (cannot zip a storage root)")
		}
		if sSrc.ID != sDest.ID {
			_ = zw.Close()
			return nil, errors.New("zip sources must be on the same storage as dest")
		}
		drvSrc, derr := a.resolver(sSrc.ID)
		if derr != nil {
			_ = zw.Close()
			return nil, derr
		}
		if aerr := a.zipAdd(ctx, zw, drvSrc, sSrc.ID, relSrc, path.Base(relSrc), seen); aerr != nil {
			_ = zw.Close()
			return nil, aerr
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		return nil, err
	}
	stat, err := tmp.Stat()
	if err != nil {
		return nil, err
	}
	size := stat.Size()
	if err := storage.EnsureFileTarget(ctx, drvDest, relDest); err != nil {
		return nil, err
	}
	if err := wr.Write(ctx, relDest, tmp, size); err != nil {
		return nil, err
	}
	mime := mimeByExt(relDest)
	if mime == "" {
		mime = "application/zip"
	}
	a.cacheUpsertFile(ctx, sDest, relDest, size, mime)
	return &aiEntry{
		Path:         joinAdapterPath(sDest.Name, relDest),
		Name:         path.Base(relDest),
		Type:         "file",
		Size:         size,
		Mime:         mime,
		LastModified: time.Now().UnixMilli(),
	}, nil
}

// zipAdd writes rel (a file or directory) into zw under the zip-internal path
// `base`. Directories recurse via the driver's List. `base` is composed from
// already-cleaned basenames, so it is zip-slip-safe by construction; the file
// branch still routes through sanitizeZipPath as defense in depth. `seen`
// dedups member names (first writer wins) so colliding sources don't error.
func (a *aiOps) zipAdd(ctx context.Context, zw *zip.Writer, drv storage.Driver, storageID int64, rel, base string, seen map[string]bool) error {
	st, err := drv.Stat(ctx, rel)
	if err != nil {
		return err
	}
	if st.Kind == storage.KindDirectory {
		objs, lerr := drv.List(ctx, rel)
		if lerr != nil {
			return lerr
		}
		if len(objs) == 0 {
			// Preserve the empty directory as a zip dir entry.
			if marker := strings.Trim(base, "/"); marker != "" {
				_, _ = zw.Create(marker + "/")
			}
			return nil
		}
		for _, o := range objs {
			if o.Name == ".filex-trash" || strings.Contains(o.Path, ".filex-trash") ||
				strings.Contains(o.Path, ".thumbs") || o.Name == ".keepdir" {
				continue
			}
			childRel := o.Path
			if childRel == "" {
				childRel = path.Join(rel, o.Name)
			}
			if aerr := a.zipAdd(ctx, zw, drv, storageID, childRel, path.Join(base, o.Name), seen); aerr != nil {
				return aerr
			}
		}
		return nil
	}
	safe, err := sanitizeZipPath(base)
	if err != nil {
		return err
	}
	if seen[safe] {
		return nil
	}
	src, err := a.body.Resolve(ctx, drv, storageID, rel, nil)
	if err != nil {
		return err
	}
	rc, err := src.Open(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	fw, err := zw.Create(safe)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, rc); err != nil {
		return err
	}
	seen[safe] = true
	return nil
}

// Unzip extracts the zip at src into destDir. Both pass resolveStorage (the
// confinement ceiling is enforced up front), and every member is zip-slip
// sanitized + re-checked to stay under destDir. Returns the count of files
// written. src and destDir must be on the same storage.
func (a *aiOps) Unzip(ctx context.Context, src, destDir string) (int, error) {
	sSrc, relSrc, err := a.resolveStorage(ctx, src)
	if err != nil {
		return 0, err
	}
	if relSrc == "" {
		return 0, errors.New("src path required")
	}
	sDst, relDst, err := a.resolveStorage(ctx, destDir)
	if err != nil {
		return 0, err
	}
	if sSrc.ID != sDst.ID {
		return 0, errors.New("unzip dest must be on the same storage as src")
	}
	if sDst.ReadOnly {
		return 0, storage.ErrReadOnly
	}
	if !a.allow(ctx, sDst, relDst, acl.LevelEditor) {
		return 0, errAIForbidden
	}
	drv, err := a.resolver(sSrc.ID)
	if err != nil {
		return 0, err
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		return 0, storage.ErrUnsupported
	}

	// archive/zip needs a ReaderAt+Seeker — materialize to a tmp file first.
	srcBody, err := a.body.Resolve(ctx, drv, sSrc.ID, relSrc, nil)
	if err != nil {
		return 0, err
	}
	rc, err := srcBody.Open(ctx)
	if err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp("", "filex-ai-unzip-*.zip")
	if err != nil {
		_ = rc.Close()
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	_, cerr := io.Copy(tmp, rc)
	_ = rc.Close()
	_ = tmp.Close()
	if cerr != nil {
		return 0, cerr
	}

	zr, err := zip.OpenReader(tmpName)
	if err != nil {
		return 0, fmt.Errorf("not a zip: %w", err)
	}
	defer zr.Close()

	dest := strings.Trim(relDst, "/")
	mkdirer, _ := drv.(storage.Mkdirer)
	count := 0
	for _, f := range zr.File {
		safeRel, serr := sanitizeZipPath(f.Name)
		if serr != nil {
			slog.Warn("ai unzip: skipped zip-slip entry", slog.String("name", f.Name), slog.String("err", serr.Error()))
			continue
		}
		target := strings.Trim(path.Join(dest, safeRel), "/")
		// Defense in depth: the joined target must stay under destDir (which is
		// itself within the confinement root, validated above).
		if dest != "" && !strings.HasPrefix(target+"/", dest+"/") {
			slog.Warn("ai unzip: target escapes dest after join", slog.String("target", target))
			continue
		}
		if strings.HasSuffix(f.Name, "/") {
			if mkdirer != nil {
				if kerr := storage.EnsureDirTarget(ctx, drv, target); kerr != nil {
					slog.Warn("ai unzip: skipped folder colliding with a file",
						slog.String("target", target), slog.String("err", kerr.Error()))
					continue
				}
				_ = mkdirer.Mkdir(ctx, target)
				a.cacheUpsertDir(ctx, sDst, target)
			}
			continue
		}
		// An archive carrying both `X` and `X/y` would reproduce the very
		// collision this guard exists to stop — skip the member, keep the rest.
		if kerr := storage.EnsureFileTarget(ctx, drv, target); kerr != nil {
			slog.Warn("ai unzip: skipped member colliding with a folder",
				slog.String("target", target), slog.String("err", kerr.Error()))
			continue
		}
		frc, oerr := f.Open()
		if oerr != nil {
			slog.Warn("ai unzip: member open", slog.String("name", f.Name), slog.String("err", oerr.Error()))
			continue
		}
		werr := wr.Write(ctx, target, frc, int64(f.UncompressedSize64))
		_ = frc.Close()
		if werr != nil {
			slog.Warn("ai unzip: write", slog.String("target", target), slog.String("err", werr.Error()))
			continue
		}
		a.cacheUpsertFile(ctx, sDst, target, int64(f.UncompressedSize64), mimeByExt(target))
		count++
	}
	return count, nil
}

// ───── cache mirror helpers (best-effort; sync reconciles later) ─────

func (a *aiOps) cacheUpsertFile(ctx context.Context, s *model.Storage, rel string, size int64, mime string) {
	parentID, perr := a.walkParent(ctx, s.ID, rel)
	clean := normalizeDBPath(rel)
	hash := pathkey.Hash(s.ID, clean)
	if existing, _ := a.store.GetNodeByPath(ctx, s.ID, hash); existing != nil {
		_ = a.store.UpdateNodeMeta(ctx, existing.ID, size, mime, existing.Etag, time.Now())
		existing.Size = size
		existing.Mime = mime
		a.dispatchThumb(existing)
		/* bag:b3 event + koru:k2 av — single post-write gate */
		writehook.OnFileWritten(ctx, s.ID, existing, a.origin)
		return
	}
	var node *model.Node
	if perr == nil {
		node, _ = a.store.CreateNode(ctx, &model.Node{
			StorageID:  s.ID,
			ParentID:   parentID,
			Name:       path.Base(clean),
			Path:       clean,
			PathHash:   hash,
			StorageKey: clean,
			Type:       model.NodeTypeFile,
			Size:       size,
			Mime:       mime,
			SyncState:  model.SyncStateSynced,
		})
		a.dispatchThumb(node)
	}
	if node == nil {
		// Cache mirror failed (unindexed parent / insert error) — the bytes
		// ARE on storage, so still emit file.uploaded with a transient node;
		// the writehook skips the AV enqueue for id-less rows.
		node = &model.Node{
			StorageID: s.ID,
			Name:      path.Base(clean),
			Path:      clean,
			Type:      model.NodeTypeFile,
			Size:      size,
			Mime:      mime,
		}
	}
	/* bag:b3 event + koru:k2 av — single post-write gate */
	writehook.OnFileWritten(ctx, s.ID, node, a.origin)
}

// dispatchThumb fires async thumbnail generation for a freshly written file —
// the same behaviour manager uploads get in upload.go. AI-surface writes
// (upload, write, unzip) previously skipped this entirely, so agent-uploaded
// images showed the broken-image placeholder in grid view (issue #3). Nil
// pipeline (tests / thumbs disabled) and nil node are no-ops; generation is
// not part of the write SLA.
func (a *aiOps) dispatchThumb(node *model.Node) {
	if a.thumbs == nil || node == nil {
		return
	}
	go func(n *model.Node) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Warn("ai: thumbnail panic", slog.Any("recover", rec))
			}
		}()
		tctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := a.thumbs.GenerateThumb(tctx, n); err != nil && err != thumb.ErrSkipped {
			slog.Warn("ai: thumbnail dispatch",
				slog.Int64("node", n.ID),
				slog.String("err", err.Error()))
		}
	}(node)
}

func (a *aiOps) cacheUpsertDir(ctx context.Context, s *model.Storage, rel string) {
	parentID, perr := a.walkParent(ctx, s.ID, rel)
	if perr != nil {
		return
	}
	clean := normalizeDBPath(rel)
	hash := pathkey.Hash(s.ID, clean)
	if existing, _ := a.store.GetNodeByPath(ctx, s.ID, hash); existing != nil {
		return
	}
	_, _ = a.store.CreateNode(ctx, &model.Node{
		StorageID:  s.ID,
		ParentID:   parentID,
		Name:       path.Base(clean),
		Path:       clean,
		PathHash:   hash,
		StorageKey: clean,
		Type:       model.NodeTypeDirectory,
		SyncState:  model.SyncStateSynced,
	})
}

func (a *aiOps) cacheMove(ctx context.Context, s *model.Storage, srcRel, dstRel string) {
	srcHash := pathkey.Hash(s.ID, normalizeDBPath(srcRel))
	existing, err := a.store.GetNodeByPath(ctx, s.ID, srcHash)
	if err != nil || existing == nil {
		return
	}
	dstClean := normalizeDBPath(dstRel)
	dstHash := pathkey.Hash(s.ID, dstClean)
	parentID, _ := a.walkParent(ctx, s.ID, dstRel)
	if merr := a.store.MoveNode(ctx, existing.ID, parentID, path.Base(dstClean), dstClean, dstHash); merr != nil {
		_ = a.store.SoftDeleteNode(ctx, existing.ID)
	}
}

// walkParent resolves the parent dir of rel to a *int64 node id (nil at
// root) using ListNodesByParent. Mirrors manager.walkDirID.
func (a *aiOps) walkParent(ctx context.Context, storageID int64, rel string) (*int64, error) {
	dir := path.Dir(strings.Trim(rel, "/"))
	if dir == "." || dir == "/" || dir == "" {
		return nil, nil
	}
	var parentPtr *int64
	for _, segment := range strings.Split(dir, "/") {
		if segment == "" {
			continue
		}
		nodes, err := a.store.ListNodesByParent(ctx, storageID, parentPtr)
		if err != nil {
			return nil, err
		}
		matched := false
		for _, n := range nodes {
			if n.Name == segment && n.Type == model.NodeTypeDirectory {
				id := n.ID
				parentPtr = &id
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("parent dir not found: %s", segment)
		}
	}
	return parentPtr, nil
}
