package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"gitlab.com/brftech/filemanager/backend/internal/auth"
	"gitlab.com/brftech/filemanager/backend/internal/confine"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/share"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// aiOps is the storage-facing core shared by the AI REST handler and the
// MCP server. It owns no HTTP concerns — every method takes/returns plain
// Go values so the REST layer and the MCP tool layer stay thin adapters.
//
// Paths use the same `adapter://relative/path` wire form as the rest of
// filex (adapter == storage name). An empty/relative path defaults to the
// first enabled storage at its root.
type aiOps struct {
	store     db.Store
	resolver  func(int64) (storage.Driver, error)
	share     *share.Service // optional — nil disables file_share/unshare
	publicURL string         // base for /s/<token> links
}

func newAIOps(store db.Store, resolver func(int64) (storage.Driver, error), shareSvc *share.Service, publicURL string) *aiOps {
	return &aiOps{store: store, resolver: resolver, share: shareSvc, publicURL: publicURL}
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
			return nil, "", fmt.Errorf("%q is outside your confined root %s — use a bare relative path (e.g. \"sub/file.txt\") or a path under %s (call file_root to see your root)", p, q, q)
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
			if strings.Contains(rel, "..") {
				return nil, "", errors.New("bad path")
			}
			return s, strings.Trim(rel, "/"), nil
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
	Storages []string `json:"storages"` // addressable adapter names
	Hint     string   `json:"hint"`
}

// RootInfo reports the caller's confinement root + reachable storages.
func (a *aiOps) RootInfo(ctx context.Context) aiRootInfo {
	info := aiRootInfo{Storages: []string{}}
	if storages, err := a.store.ListEnabledStorages(ctx); err == nil {
		for _, s := range storages {
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
	st, err := drv.Stat(ctx, rel)
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
	rc, err := drv.Read(ctx, rel)
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
func (a *aiOps) Write(ctx context.Context, p string, data []byte) (*aiEntry, error) {
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

	mime := ""
	if len(data) > 0 {
		head := data
		if len(head) > 512 {
			head = head[:512]
		}
		mime = storage.RefineOfficeMime(http.DetectContentType(head), name)
	}

	if err := wr.Write(ctx, rel, bytes.NewReader(data), int64(len(data))); err != nil {
		return nil, err
	}

	a.cacheUpsertFile(ctx, s, rel, int64(len(data)), mime)

	return &aiEntry{
		Path:         joinAdapterPath(s.Name, rel),
		Name:         name,
		Type:         "file",
		Size:         int64(len(data)),
		Mime:         mime,
		LastModified: time.Now().UnixMilli(),
	}, nil
}

// Delete soft-deletes a file/dir (rename into .filex-trash, flip the cache
// row's deleted_at) mirroring the SFC's vfDelete contract — so AI deletes
// land in the same trash the UI restores from.
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
	drv, err := a.resolver(s.ID)
	if err != nil {
		return err
	}
	base := path.Base(rel)
	trashRel := fmt.Sprintf("%s/%d-%s__%s", trashPrefix, time.Now().Unix(), randHex6(), base)

	if mover, ok := drv.(storage.Mover); ok {
		if err := mover.Move(ctx, rel, trashRel); err != nil {
			return err
		}
		origClean := normalizeDBPath(rel)
		origHash := managerPathHash(s.ID, origClean)
		if existing, gerr := a.store.GetNodeByPath(ctx, s.ID, origHash); gerr == nil && existing != nil {
			newClean := normalizeDBPath(trashRel)
			newHash := managerPathHash(s.ID, newClean)
			_ = a.store.SoftDeleteAndRetag(ctx, existing.ID, newClean, newHash, origClean)
		}
		return nil
	}

	// No move support — hard delete (legacy drivers).
	del, ok := drv.(storage.Deleter)
	if !ok {
		return storage.ErrUnsupported
	}
	if err := del.Delete(ctx, rel); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	origHash := managerPathHash(s.ID, normalizeDBPath(rel))
	if existing, gerr := a.store.GetNodeByPath(ctx, s.ID, origHash); gerr == nil && existing != nil {
		_ = a.store.SoftDeleteNode(ctx, existing.ID)
	}
	return nil
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
	if sSrc.ID != sDst.ID {
		return nil, errors.New("cross-storage move not supported")
	}
	if relSrc == "" || relDst == "" {
		return nil, errors.New("src and dst required")
	}
	if sSrc.ReadOnly {
		return nil, storage.ErrReadOnly
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
	drv, err := a.resolver(s.ID)
	if err != nil {
		return nil, err
	}
	mk, ok := drv.(storage.Mkdirer)
	if !ok {
		return nil, storage.ErrUnsupported
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
	node, err := a.store.GetNodeByPath(ctx, s.ID, sharePathHash(s.ID, rel))
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
	opts := share.CreateOpts{NodeID: node.ID, PIN: pinVal, CreatedBy: userID}
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

// ───── cache mirror helpers (best-effort; sync reconciles later) ─────

func (a *aiOps) cacheUpsertFile(ctx context.Context, s *model.Storage, rel string, size int64, mime string) {
	parentID, perr := a.walkParent(ctx, s.ID, rel)
	clean := normalizeDBPath(rel)
	hash := managerPathHash(s.ID, clean)
	if existing, _ := a.store.GetNodeByPath(ctx, s.ID, hash); existing != nil {
		_ = a.store.UpdateNodeMeta(ctx, existing.ID, size, mime, existing.Etag, time.Now())
		return
	}
	if perr != nil {
		return
	}
	_, _ = a.store.CreateNode(ctx, &model.Node{
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
}

func (a *aiOps) cacheUpsertDir(ctx context.Context, s *model.Storage, rel string) {
	parentID, perr := a.walkParent(ctx, s.ID, rel)
	if perr != nil {
		return
	}
	clean := normalizeDBPath(rel)
	hash := managerPathHash(s.ID, clean)
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
	srcHash := managerPathHash(s.ID, normalizeDBPath(srcRel))
	existing, err := a.store.GetNodeByPath(ctx, s.ID, srcHash)
	if err != nil || existing == nil {
		return
	}
	dstClean := normalizeDBPath(dstRel)
	dstHash := managerPathHash(s.ID, dstClean)
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
