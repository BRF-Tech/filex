package handlers

import (
	"archive/zip"
	"context"
	"crypto/md5"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Share handles share creation and the public viewer endpoints.
type Share struct {
	Service         *share.Service
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
	PublicURL       string
	ACL             *acl.Resolver
	// Zip caches generated folder-share ZIPs (keyed by node id + content
	// signature) so downloads don't re-zip the whole folder every time. A nil
	// or disabled cache falls back to streaming a fresh zip. See serveFolderZip.
	Zip *sharezip.Cache
	// Branding resolves the public pages' identity (logo/name/accent/footer)
	// from settings (wiring:e1). Nil-safe: unwired = stock filex chrome.
	Branding *BrandingSource
}

// AttachBranding wires the shared branding source (wiring:e1).
func (h *Share) AttachBranding(b *BrandingSource) { h.Branding = b }

// chrome computes the branded page fragments for one request (wiring:e1).
func (h *Share) chrome(r *http.Request) publicChrome { return publicChromeFor(h.Branding, r) }

// AttachACL wires the RBAC resolver so minting a public share link requires
// ≥editor on the target node (sharing grants outside access — a write action).
func (h *Share) AttachACL(r *acl.Resolver) { h.ACL = r }

// NewShare constructs a Share handler. zipCache enables the folder-share ZIP
// cache (pass a disabled/nil cache to stream fresh on every folder download).
func NewShare(svc *share.Service, store db.Store, resolver func(int64) (storage.Driver, error), publicURL string, zipCache *sharezip.Cache) *Share {
	return &Share{
		Service:         svc,
		Store:           store,
		StorageResolver: resolver,
		PublicURL:       strings.TrimRight(publicURL, "/"),
		Zip:             zipCache,
	}
}

// shareCreateReq accepts both the modern `{path, password (bool), …}`
// shape the SFC sends AND the legacy `{node_id, pin, expires_in, …}`
// shape kept for embed.js consumers. When `password=true` we generate a
// random 8-char PIN and return it in the response so the UI can show
// the user the unlock code once.
type shareCreateReq struct {
	// Modern shape (filex-core SFC).
	Path     string `json:"path,omitempty"`     // <adapter>://<rel>
	Password *bool  `json:"password,omitempty"` // bool: generate-PIN flag

	// Legacy shape (embed.js + early integrators).
	NodeID    int64  `json:"node_id,omitempty"`
	PIN       string `json:"pin,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"` // seconds from now

	// Shared.
	ExpiresAt    string `json:"expires_at,omitempty"` // RFC3339 — overrides expires_in
	MaxDownloads int    `json:"max_downloads,omitempty"`

	// File-drop (public upload link) fields. kind=="drop" mints an UPLOAD
	// link into a folder — the inverse of a download share. The target must
	// be a directory. drop_settings carries the per-link limits blob
	// {max_files, max_file_size_mb, allowed_ext, ask_name}; max_uploads caps
	// the total number of files the link may ever receive.
	Kind         string          `json:"kind,omitempty"`
	MaxUploads   int             `json:"max_uploads,omitempty"`
	DropSettings json.RawMessage `json:"drop_settings,omitempty"`
}

// shareCreateRespInner is the payload nested under `share` in the
// response — the SFC accesses it as `body.share.*`.
type shareCreateRespInner struct {
	ID           int64      `json:"id"`
	UUID         string     `json:"uuid"` // alias for token (frontend uses uuid in delete URL)
	Token        string     `json:"token"`
	URL          string     `json:"url"`
	Kind         string     `json:"kind,omitempty"` // "download" | "drop"
	Path         string     `json:"path,omitempty"`
	Filename     string     `json:"filename,omitempty"`
	HasPin       bool       `json:"has_pin"`
	PasswordPin  string     `json:"password_pin,omitempty"` // ONLY on creation when we generated it
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	MaxDownloads *int       `json:"max_downloads,omitempty"`
}

// HandleCreate mints a new share token.
//
// The SFC's `useFileApi.createShare` posts:
//
//	{ path: "<adapter>://<rel>", password: true|false, expires_at: …, max_downloads: … }
//
// and reads `body.share.url` / `body.share.password_pin` afterwards.
// The legacy embed.js posts `{ node_id, pin, expires_in, … }` and reads
// the flat fields. We support both.
func (h *Share) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req shareCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}

	// Resolve node_id from either input shape.
	nodeID := req.NodeID
	if nodeID == 0 && req.Path != "" {
		resolved, err := h.resolveNodeIDFromPath(r.Context(), req.Path)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		nodeID = resolved
	}
	if nodeID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path or node_id"})
		return
	}

	// RBAC: creating a public share is an outbound-access grant → ≥editor.
	if h.ACL != nil {
		node, err := h.Store.GetNode(r.Context(), nodeID)
		if err != nil || node == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return
		}
	}

	// File-drop links mint a public UPLOAD endpoint into a folder — validate
	// the target is a directory up front so a public uploader can never be
	// pointed at (and made to overwrite) a single file.
	isDrop := req.Kind == model.ShareKindDrop
	if isDrop {
		node, err := h.Store.GetNode(r.Context(), nodeID)
		if err != nil || node == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if node.Type != model.NodeTypeDirectory {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "drop links require a folder target"})
			return
		}
	}

	// PIN: explicit string wins; password=true generates one; otherwise empty.
	pin := req.PIN
	pinGenerated := ""
	if pin == "" && req.Password != nil && *req.Password {
		pin = randomPIN(8)
		pinGenerated = pin
	}

	user := auth.UserFrom(r.Context())
	var userID *int64
	if user != nil {
		uid := user.ID
		userID = &uid
	}
	opts := share.CreateOpts{
		NodeID:     nodeID,
		PIN:        pin,
		CreatedBy:  userID,
		CreatedVia: auth.TokenUserFrom(r.Context()),
	}
	switch {
	case req.ExpiresAt != "":
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad expires_at"})
			return
		}
		opts.ExpiresAt = &t
	case req.ExpiresIn > 0:
		t := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second)
		opts.ExpiresAt = &t
	}
	if req.MaxDownloads > 0 {
		opts.MaxDownloads = &req.MaxDownloads
	}
	if isDrop {
		opts.Kind = model.ShareKindDrop
		if req.MaxUploads > 0 {
			opts.MaxUploads = &req.MaxUploads
		}
		if len(req.DropSettings) > 0 {
			ds := string(req.DropSettings)
			opts.DropSettings = &ds
		}
	}
	sh, err := h.Service.Create(r.Context(), opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	linkURL := h.shareURL(sh.Token)
	if sh.IsDrop() {
		linkURL = h.dropURL(sh.Token)
	}
	inner := shareCreateRespInner{
		ID:           sh.ID,
		UUID:         sh.Token,
		Token:        sh.Token,
		URL:          linkURL,
		Kind:         sh.Kind,
		HasPin:       sh.PinHash != "",
		PasswordPin:  pinGenerated,
		ExpiresAt:    sh.ExpiresAt,
		MaxDownloads: sh.MaxDownloads,
	}
	node, _ := h.Store.GetNode(r.Context(), nodeID)
	if node != nil {
		inner.Filename = node.Name
		inner.Path = node.Path
	}

	/* bag:b3 event */
	shareEv := notify.Event{
		Event: notify.EventShareCreated,
		Body:  inner.Path,
		Share: &notify.ShareRef{Token: sh.Token, Path: inner.Path},
		Meta:  map[string]any{"kind": sh.Kind, "has_pin": sh.PinHash != ""},
	}
	if node != nil {
		shareEv.Node = &notify.NodeRef{StorageID: node.StorageID, Path: node.Path, Name: node.Name, Size: node.Size}
	}
	emitFileEvent(r.Context(), shareEv)

	// Dual envelope: nested `share` for the SFC + flat fields at the
	// top level for legacy embed.js. Cheap to ship both.
	writeJSON(w, http.StatusOK, map[string]any{
		"share":         inner,
		"id":            inner.ID,
		"token":         inner.Token,
		"url":           inner.URL,
		"kind":          inner.Kind,
		"has_pin":       inner.HasPin,
		"expires_at":    inner.ExpiresAt,
		"max_downloads": inner.MaxDownloads,
	})
}

// HandleList returns the current caller's active share links for one item, so
// the permissions modal's "Existing links" section can list (and revoke) them.
//
//	GET /api/files/share?path=<adapter://rel>   (or ?node_id=<n>)
//
// Non-admins only see links they created; admins see every link on the item.
// A path with no indexed node (or no links yet) returns an empty list rather
// than an error so the modal shows "none" instead of failing. The `uuid` field
// carries the numeric share id (what DELETE /share/{id} expects), while `url`
// is built from the token — matching the ShareInfo shape the SFC consumes.
func (h *Share) HandleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	nodeID := int64(0)
	if v := q.Get("node_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			nodeID = id
		}
	}
	if nodeID == 0 {
		p := q.Get("path")
		if p == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path or node_id"})
			return
		}
		resolved, err := h.resolveNodeIDFromPath(r.Context(), p)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"shares": []any{}})
			return
		}
		nodeID = resolved
	}

	// RBAC: seeing an item's links is the same bar as minting one (≥editor).
	if h.ACL != nil {
		node, err := h.Store.GetNode(r.Context(), nodeID)
		if err != nil || node == nil {
			writeJSON(w, http.StatusOK, map[string]any{"shares": []any{}})
			return
		}
		if !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return
		}
	}

	rows, err := h.Store.ListSharesByNode(r.Context(), nodeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	user := auth.UserFrom(r.Context())
	now := time.Now()
	out := make([]map[string]any, 0, len(rows))
	for _, sh := range rows {
		if sh.ExpiresAt != nil && !sh.ExpiresAt.After(now) {
			continue // revoked / expired — keep it out of the active list
		}
		if user != nil && !user.IsAdmin() && (sh.CreatedBy == nil || *sh.CreatedBy != user.ID) {
			continue // non-admins only manage their own links
		}
		link := h.shareURL(sh.Token)
		if sh.IsDrop() {
			link = h.dropURL(sh.Token)
		}
		out = append(out, map[string]any{
			"uuid":          strconv.FormatInt(sh.ID, 10),
			"url":           link,
			"kind":          sh.Kind,
			"expires_at":    sh.ExpiresAt,
			"max_downloads": sh.MaxDownloads,
			"downloads":     sh.DownloadCount,
			"created_at":    sh.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": out})
}

// resolveNodeIDFromPath looks up a node by `<adapter>://<rel>` (or bare
// rel against the first storage). Returns 0 + an error when no row.
func (h *Share) resolveNodeIDFromPath(ctx context.Context, fullPath string) (int64, error) {
	idx := strings.Index(fullPath, "://")
	var adapter, rel string
	if idx >= 0 {
		adapter = fullPath[:idx]
		rel = strings.Trim(fullPath[idx+3:], "/")
	} else {
		rel = strings.Trim(fullPath, "/")
	}
	storages, err := h.Store.ListEnabledStorages(ctx)
	if err != nil {
		return 0, err
	}
	if len(storages) == 0 {
		return 0, errNoStorages
	}
	if adapter == "" {
		adapter = storages[0].Name
	}
	var st *model.Storage
	for _, s := range storages {
		if s.Name == adapter {
			st = s
			break
		}
	}
	if st == nil {
		return 0, fmt.Errorf("unknown adapter: %s", adapter)
	}
	clean := strings.TrimRight(path.Clean("/"+rel), "/")
	if clean == "" {
		return 0, fmt.Errorf("share target path is empty")
	}
	hash := sharePathHash(st.ID, clean)
	node, err := h.Store.GetNodeByPath(ctx, st.ID, hash)
	if err != nil || node == nil {
		return 0, fmt.Errorf("file not found: %s", fullPath)
	}
	return node.ID, nil
}

// sharePathHash mirrors managerPathHash so the share lookup hits the
// same cache row the manager handler created.
func sharePathHash(storageID int64, p string) string {
	h := md5.New()
	_, _ = h.Write([]byte(strings.TrimRight(path.Clean("/"+p), "/")))
	_, _ = h.Write([]byte{'\x00'})
	_, _ = h.Write([]byte{byte(storageID), byte(storageID >> 8), byte(storageID >> 16), byte(storageID >> 24)})
	return hex.EncodeToString(h.Sum(nil))
}

// randomPIN returns an n-char numeric PIN (digits only — easier to type
// from a phone than a mixed-case string).
func randomPIN(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	if _, err := cryptoRand.Read(b); err != nil {
		// Fall back to time-based — we still want a usable PIN.
		ts := time.Now().UnixNano()
		for i := range b {
			b[i] = digits[ts%10]
			ts /= 10
		}
		return string(b)
	}
	for i := range b {
		b[i] = digits[int(b[i])%10]
	}
	return string(b)
}

var errNoStorages = errors.New("no storages configured")

// HandleDelete revokes a share.
func (h *Share) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
		return
	}
	user := auth.UserFrom(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	sh, err := h.Store.GetShareByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !user.IsAdmin() && (sh.CreatedBy == nil || *sh.CreatedBy != user.ID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	// Soft revoke (sets expires_at = NOW) — keeps audit trail.
	if err := h.Store.RevokeShare(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// HandleMetadata returns metadata for a share token (no PIN check).
//
// Used by the embed.js viewer to decide whether to render a PIN prompt.
func (h *Share) HandleMetadata(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	sh, err := h.Store.GetShareByToken(r.Context(), tok)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if sh.IsExpired(time.Now()) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "expired"})
		return
	}
	resp := map[string]any{
		"requires_pin":   sh.PinHash != "",
		"expires_at":     sh.ExpiresAt,
		"download_count": sh.DownloadCount,
		"max_downloads":  sh.MaxDownloads,
	}
	if node, err := h.Store.GetNode(r.Context(), sh.NodeID); err == nil {
		resp["filename"] = node.Name
		resp["size"] = node.Size
		resp["mime"] = node.Mime
		resp["is_directory"] = node.Type == "dir"
		if sh.MaxDownloads != nil {
			remaining := *sh.MaxDownloads - sh.DownloadCount
			if remaining < 0 {
				remaining = 0
			}
			resp["downloads_remaining"] = remaining
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleDownload streams the shared file (after PIN check).
//
// On a PIN-protected share without a PIN, GET renders an HTML form. POST
// (with a PIN field) is what the form submits to. ?pin= and X-Filex-Pin
// are also accepted for programmatic access.
func (h *Share) HandleDownload(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	pin := h.extractPIN(r)

	sh, err := h.Store.GetShareByToken(r.Context(), tok)
	if err != nil {
		h.renderErrorPage(w, r, http.StatusNotFound,
			"Not found",
			"This share link does not exist or has been removed.")
		return
	}
	if sh.IsExpired(time.Now()) {
		h.renderErrorPage(w, r, http.StatusNotFound,
			"Share expired",
			"This share link has expired or reached its download limit.")
		return
	}

	// PIN required path: render the form on GET when no PIN supplied.
	if sh.PinHash != "" && pin == "" {
		h.renderPINForm(w, r, tok, "")
		return
	}

	// Resolve runs the PIN bcrypt check + recomputes expiry.
	resolved, err := h.Service.Resolve(r.Context(), tok, pin)
	switch {
	case errors.Is(err, share.ErrExpired):
		h.renderErrorPage(w, r, http.StatusNotFound,
			"Share expired",
			"This share link has expired or reached its download limit.")
		return
	case errors.Is(err, share.ErrBadPIN):
		// Re-render with a friendly error rather than a flat 401.
		h.renderPINForm(w, r, tok, "Wrong PIN — try again.")
		return
	case err != nil:
		h.renderErrorPage(w, r, http.StatusNotFound,
			"Not found",
			"This share link does not exist or has been removed.")
		return
	}

	// Confirmed download step — when a PIN-protected share's POST
	// successfully resolved and the client hasn't yet seen the
	// "PIN accepted" page, render the success screen and let it
	// auto-submit a hidden form to itself with ?confirmed=1 so the
	// stream comes second. This gives the user clear feedback that
	// the PIN matched before the browser hijacks the page with an
	// `attachment` Content-Disposition.
	if sh.PinHash != "" && r.URL.Query().Get("confirmed") != "1" && r.Method == http.MethodPost {
		h.renderUnlockedPage(w, r, tok, pin)
		return
	}

	node, err := h.Store.GetNode(r.Context(), resolved.NodeID)
	if err != nil {
		http.Error(w, "node missing", http.StatusNotFound)
		return
	}
	drv, err := h.StorageResolver(node.StorageID)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}

	// Folder share → serve every file under it as a ZIP ("download all").
	// The single-file presign/Read path below can't open a directory as a
	// byte stream — a shared folder used to 500 here ("read error"). We cache
	// the generated ZIP on local disk (keyed by content signature) so repeat
	// downloads don't re-read + re-compress the whole folder from object
	// storage every time, and show a "preparing…" progress page while a cold
	// cache builds. serveFolderZip owns the download-count increment (only on a
	// real byte serve, not status polls / the wait page).
	if node.Type == model.NodeTypeDirectory {
		/* wiring:d2 — zip parametresi YOKSA klasör paylaşımı artık gezinme
		   sayfası gösterir (≥%60 görsel/video → galeri, değilse liste);
		   ZIP akışı sayfadaki "Tümünü indir" → ?zip=… arkasında aynen durur. */
		if r.URL.Query().Get("zip") == "" {
			h.renderFolderBrowse(r.Context(), w, r, drv, node, resolved, pin)
			return
		}
		h.serveFolderZip(r.Context(), w, r, drv, node.Path, node.Name, node.ID, resolved.ID, pin)
		return
	}

	// Use a presigned URL when the driver supports it AND the operator
	// hasn't opted out via `disable_presign: true` in storage config.
	// Honor `Capabilities().Presign` so drivers can advertise no-presign
	// at runtime (e.g. Hetzner Object Storage / Ceph RGW which produces
	// SignatureDoesNotMatch on AWS SDK v2 SigV4 — sweep-2026-05-09 bug 23).
	// When presign is disabled, fall through to the backend-stream path
	// below.
	if pres, ok := drv.(storage.Presigner); ok && drv.Capabilities().Presign {
		if u, err := pres.PresignDownload(r.Context(), node.Path, 5*time.Minute); err == nil && u != "" {
			_ = h.Service.IncrementDownload(r.Context(), resolved.ID)
			http.Redirect(w, r, u, http.StatusFound)
			return
		}
	}

	rc, err := drv.Read(r.Context(), node.Path)
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	defer rc.Close()

	disposition := "attachment"
	if r.URL.Query().Get("inline") == "1" {
		disposition = "inline"
	}
	mime := node.Mime
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disposition, sanitizeFilename(node.Name)))
	if node.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(node.Size, 10))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(w, rc); err != nil {
		// Headers already sent.
		return
	}
	_ = h.Service.IncrementDownload(r.Context(), resolved.ID)
}

// streamFolderZip walks `root` on the driver and writes every file under it
// into a ZIP streamed to w. Entry names are relative to `root`, so the archive
// unpacks into a clean tree. Internal dirs (trash, thumbnails) are skipped, and
// individually unreadable files are skipped rather than aborting the whole
// download. The write is streaming — no full buffer — so large folders are fine.
func (h *Share) streamFolderZip(ctx context.Context, w http.ResponseWriter, drv storage.Driver, root, name string) error {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, sanitizeFilename(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")

	zw := zip.NewWriter(w)
	defer zw.Close()

	var walk func(dir, prefix string) error
	walk = func(dir, prefix string) error {
		objs, err := drv.List(ctx, dir)
		if err != nil {
			return err
		}
		for _, o := range objs {
			if o.Name == ".filex-trash" || o.Name == ".thumbs" || o.Name == ".keepdir" {
				continue
			}
			entry := prefix + o.Name
			switch o.Kind {
			case storage.KindDirectory:
				if err := walk(o.Path, entry+"/"); err != nil {
					return err
				}
			case storage.KindFile:
				rc, err := drv.Read(ctx, o.Path)
				if err != nil {
					continue
				}
				fw, err := zw.Create(entry)
				if err != nil {
					_ = rc.Close()
					return err
				}
				_, _ = io.Copy(fw, rc)
				_ = rc.Close()
			}
		}
		return nil
	}
	return walk(root, "")
}

// serveFolderZip serves a shared folder as a ZIP ("download all"), backed by
// the on-disk cache in internal/sharezip so we don't re-read + re-compress the
// whole folder from object storage on every download (slow for large folders
// like receipt months). Behaviour by request:
//   - cache warm  → serve the finished file immediately (known Content-Length)
//   - ?zip=status → JSON {ready, percent} for the progress page (starts a build
//     if idle); does not count as a download
//   - ?zip=wait   → block until the build finishes then serve (no-JS fallback +
//     the "ready" redirect target)
//   - otherwise   → start the build and render a "preparing…" progress page
//
// The download counter is only bumped on a real byte serve. Any cache problem
// falls back to streaming a fresh zip so a broken cache never blocks a download.
func (h *Share) serveFolderZip(ctx context.Context, w http.ResponseWriter, r *http.Request, drv storage.Driver, root, name string, nodeID, shareID int64, pin string) {
	stream := func() {
		if err := h.streamFolderZip(ctx, w, drv, root, name); err == nil {
			_ = h.Service.IncrementDownload(ctx, shareID)
		}
	}
	serve := func(cachePath string) {
		if err := serveZipFile(w, cachePath, name); err == nil {
			_ = h.Service.IncrementDownload(ctx, shareID)
		}
	}

	if h.Zip == nil || !h.Zip.Enabled() {
		stream()
		return
	}

	cachePath, files, err := h.Zip.Plan(ctx, drv, root, nodeID)
	if err != nil {
		stream()
		return
	}

	mode := r.URL.Query().Get("zip")

	// Progress poll — ALWAYS returns JSON, even once the cache is warm, so the
	// wait page's fetch().json() never chokes on zip bytes. Starts a build if
	// idle (polling alone drives generation). Never counts as a download.
	if mode == "status" {
		if _, ok := h.Zip.Cached(cachePath); ok {
			writeJSON(w, http.StatusOK, map[string]any{"ready": true, "percent": 100})
			return
		}
		g := h.Zip.StartOrGet(cachePath, files, nodeID, drv)
		if _, ok := h.Zip.Cached(cachePath); ok {
			writeJSON(w, http.StatusOK, map[string]any{"ready": true, "percent": 100})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ready": false, "percent": g.Percent()})
		return
	}

	// Cache hit → serve immediately (default + wait modes).
	if _, ok := h.Zip.Cached(cachePath); ok {
		serve(cachePath)
		return
	}

	switch mode {
	case "wait":
		// Block until the build completes then serve (no-JS fallback + the JS
		// "ready" redirect). A cancelled ctx (client left) just returns.
		g := h.Zip.StartOrGet(cachePath, files, nodeID, drv)
		_ = g.Wait(ctx)
		if _, ok := h.Zip.Cached(cachePath); ok {
			serve(cachePath)
			return
		}
		stream() // build failed → fresh stream fallback
	default:
		// Cold cache → kick off the build and show a progress page.
		h.Zip.StartOrGet(cachePath, files, nodeID, drv)
		h.renderZipWaitPage(w, r, name, pin)
	}
}

// serveZipFile streams a finished cached zip with an explicit Content-Length so
// the browser shows real download progress.
func serveZipFile(w http.ResponseWriter, cachePath, name string) error {
	f, err := os.Open(cachePath)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, sanitizeFilename(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	_, err = io.Copy(w, f)
	return err
}

// renderZipWaitPage shows a "preparing…" page that polls ?zip=status for build
// progress and, once ready, navigates to ?zip=wait to download. pin (when the
// share is PIN-protected) is threaded through so the poll/download requests stay
// authenticated — the viewer already proved it, so embedding it here is safe.
func (h *Share) renderZipWaitPage(w http.ResponseWriter, r *http.Request, name, pin string) {
	pinQuery := ""
	if pin != "" {
		pinQuery = "&pin=" + url.QueryEscape(pin)
	}
	chrome := h.chrome(r) /* wiring:e1 */
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// PinQuery is used only inside a JS string (html/template JS-escapes it); the
	// static href works for no-PIN shares and the script rewrites it with the pin
	// for PIN shares (which already require JS via the unlock page).
	_ = zipWaitTemplate.Execute(w, map[string]any{
		"Name":      name,
		"PinQuery":  pinQuery,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    chrome.FooterTR,
	})
}

// zipWaitTemplate is a dependency-free progress page for a folder-share ZIP
// that's still being built.
var zipWaitTemplate = template.Must(template.New("zipwait").Parse(`<!doctype html>
<html lang="tr"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Dosya hazırlanıyor…</title>
` + publicPageStyle + `
{{.BrandCSS}}
<style>
.track { height: 10px; border-radius: 999px; background: var(--px-line); overflow: hidden; }
.bar { height: 100%; width: 0%; border-radius: 999px; background: linear-gradient(90deg, var(--px-accent), var(--px-accent-hover)); transition: width 0.4s ease; }
.pct { margin-top: 10px; font-size: 0.95rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.hint { margin: 16px 0 0; font-size: 0.8rem; color: var(--px-muted); }
.hint a { color: var(--px-accent); }
@media (prefers-reduced-motion: reduce) { .bar { transition: none; } }
</style>
</head><body>
<main class="wrap">
{{.BrandHead}}
<div class="card">
<div class="icon-badge">` + publicIconFolderZip + `</div>
<h1>Dosya hazırlanıyor…</h1>
<p class="sub">{{.Name}} — klasör ZIP arşivi olarak paketleniyor.</p>
<div class="track" aria-hidden="true"><div id="bar" class="bar"></div></div>
<div class="pct"><span id="pct">%0</span></div>
<p class="hint">İndirme hazır olduğunda otomatik başlayacak. Başlamazsa <a id="dl" href="?zip=wait">buraya tıklayın</a>.</p>
</div>
{{.Footer}}
</main>
<script>
(function(){
  var q = "{{.PinQuery}}";
  var dl = document.getElementById("dl");
  if (dl) { dl.href = "?zip=wait" + q; }
  function tick(){
    fetch("?zip=status" + q, {headers:{"Accept":"application/json"}})
      .then(function(r){ return r.json(); })
      .then(function(d){
        var p = (d && typeof d.percent === "number") ? d.percent : 0;
        document.getElementById("bar").style.width = p + "%";
        document.getElementById("pct").textContent = "%" + p;
        if (d && d.ready) { window.location = "?zip=wait" + q; }
        else { setTimeout(tick, 1000); }
      })
      .catch(function(){ setTimeout(tick, 2000); });
  }
  tick();
})();
</script>
</body></html>`))

// extractPIN returns the PIN from query, header, or POST form.
func (h *Share) extractPIN(r *http.Request) string {
	if v := r.URL.Query().Get("pin"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Filex-Pin"); v != "" {
		return v
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		if v := r.PostForm.Get("pin"); v != "" {
			return v
		}
	}
	return ""
}

// shareURL returns the canonical /s/{token} URL.
func (h *Share) shareURL(token string) string {
	if h.PublicURL == "" {
		return "/s/" + token
	}
	return h.PublicURL + "/s/" + token
}

// dropURL returns the canonical /d/{token} public upload (file-drop) URL.
func (h *Share) dropURL(token string) string {
	if h.PublicURL == "" {
		return "/d/" + token
	}
	return h.PublicURL + "/d/" + token
}

// shareURLPath returns the URL path for a share token.
func shareURLPath(token string) string {
	return "/s/" + path.Clean(token)
}

// publicPageStyle is the shared inline stylesheet for every public
// share-facing page (PIN gate, unlocked, error, zip-wait, drop). These pages
// are served standalone with no access to the SPA's asset pipeline, so
// everything stays inline and dependency-free. Light/dark comes from CSS
// custom properties + prefers-color-scheme.
const publicPageStyle = `<style>
:root {
  color-scheme: light dark;
  --px-bg1: #f5f7fb; --px-bg2: #e9edf4;
  --px-card: #ffffff;
  --px-fg: #1b2129; --px-muted: #66727f;
  --px-line: #d7dde5;
  --px-accent: #4f46e5; --px-accent-hover: #4338ca;
  --px-accent-soft: rgba(79, 70, 229, 0.10);
  --px-ok: #16a34a; --px-ok-soft: rgba(22, 163, 74, 0.12);
  --px-err: #dc2626; --px-err-soft: rgba(220, 38, 38, 0.10);
  --px-shadow: 0 12px 40px rgba(15, 23, 42, 0.10);
}
@media (prefers-color-scheme: dark) {
  :root {
    --px-bg1: #12151a; --px-bg2: #191e25;
    --px-card: #1f242c;
    --px-fg: #e7ebf1; --px-muted: #97a1af;
    --px-line: #3a424d;
    --px-accent: #6366f1; --px-accent-hover: #818cf8;
    --px-accent-soft: rgba(99, 102, 241, 0.18);
    --px-ok: #22c55e; --px-ok-soft: rgba(34, 197, 94, 0.14);
    --px-err: #f87171; --px-err-soft: rgba(248, 113, 113, 0.12);
    --px-shadow: 0 12px 40px rgba(0, 0, 0, 0.45);
  }
}
* { box-sizing: border-box; }
body { font-family: system-ui, -apple-system, "Segoe UI", sans-serif; margin: 0; min-height: 100vh; display: grid; place-items: center; padding: 24px 16px; background: linear-gradient(160deg, var(--px-bg1), var(--px-bg2)); color: var(--px-fg); }
.wrap { width: 100%; display: flex; flex-direction: column; align-items: center; gap: 18px; }
.card { width: 400px; max-width: 100%; padding: 32px 28px; border-radius: 16px; background: var(--px-card); border: 1px solid var(--px-line); box-shadow: var(--px-shadow); text-align: center; }
.icon-badge { width: 64px; height: 64px; margin: 0 auto 16px; border-radius: 50%; display: grid; place-items: center; background: var(--px-accent-soft); color: var(--px-accent); }
.icon-badge.ok { background: var(--px-ok-soft); color: var(--px-ok); }
.icon-badge.err { background: var(--px-err-soft); color: var(--px-err); }
.icon-badge svg { width: 30px; height: 30px; }
h1 { font-size: 1.25rem; margin: 0 0 6px; letter-spacing: -0.01em; }
.sub { margin: 0 0 20px; color: var(--px-muted); font-size: 0.9rem; line-height: 1.5; overflow-wrap: anywhere; }
.btn { display: block; width: 100%; margin-top: 18px; padding: 13px; border: 0; border-radius: 10px; font-size: 1rem; font-weight: 600; font-family: inherit; cursor: pointer; background: var(--px-accent); color: #fff; transition: background 0.15s ease; }
.btn:hover:not(:disabled) { background: var(--px-accent-hover); }
.btn:focus-visible { outline: 2px solid var(--px-accent); outline-offset: 2px; }
.btn:disabled { opacity: 0.5; cursor: default; }
.error { margin-top: 14px; padding: 10px 12px; border-radius: 9px; background: var(--px-err-soft); color: var(--px-err); font-size: 0.86rem; }
.brand { display: inline-flex; align-items: center; gap: 7px; color: var(--px-muted); font-size: 0.78rem; }
.brand svg { width: 16px; height: 16px; flex: none; }
.brand a { color: inherit; font-weight: 600; text-decoration: none; }
.brand a:hover { text-decoration: underline; color: var(--px-accent); }
.spinner { width: 15px; height: 15px; border: 2px solid currentColor; border-right-color: transparent; border-radius: 50%; display: inline-block; vertical-align: -2px; animation: px-spin 0.8s linear infinite; opacity: 0.5; margin-right: 8px; }
@keyframes px-spin { to { transform: rotate(360deg); } }
@media (prefers-reduced-motion: reduce) { .spinner { animation-duration: 2.4s; } .btn { transition: none; } }
/* wiring:e1 — branding chrome (logo/name header + custom footer) */
.pbrand { display: inline-flex; align-items: center; gap: 10px; max-width: 100%; }
.pbrand__logo { height: 34px; max-width: 200px; object-fit: contain; display: block; }
.pbrand__name { font-size: 1.05rem; font-weight: 700; letter-spacing: -0.01em; overflow-wrap: anywhere; }
.pfoot { display: flex; flex-direction: column; align-items: center; gap: 8px; }
.pfoot__custom { color: var(--px-muted); font-size: 0.78rem; text-align: center; max-width: 480px; overflow-wrap: anywhere; }
</style>`

// publicBrandMark is the tiny inline filex logo used in the public-page
// footer — the same folder+check mark as the SPA's LogoMark.vue, with a solid
// fill so no gradient ids can collide across pages.
const publicBrandMark = `<svg viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="#6366f1"/><path d="M7 11a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H9a2 2 0 0 1-2-2V11z" fill="none" stroke="#fff" stroke-width="1.8" stroke-linejoin="round"/><path d="M11.5 17.5l3 2.5 5.5-6" fill="none" stroke="#fff" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>`

// Public-page footers. Each template keeps its existing content language, so
// there is a Turkish and an English variant of the same modest brand line.
const (
	publicFooterTR = `<footer class="brand">` + publicBrandMark + `<span><a href="https://filex.sh" target="_blank" rel="noopener">filex</a> ile paylaşıldı</span></footer>`
	publicFooterEN = `<footer class="brand">` + publicBrandMark + `<span>Shared with <a href="https://filex.sh" target="_blank" rel="noopener">filex</a></span></footer>`
)

// Inline line-style icons (currentColor) for the public pages.
const (
	publicIconLock      = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="4.5" y="10.5" width="15" height="9.5" rx="2"/><path d="M8 10.5V7a4 4 0 0 1 8 0v3.5"/><path d="M12 14.5v2"/></svg>`
	publicIconCheck     = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4.5 12.5l5 5 10-11"/></svg>`
	publicIconAlert     = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10.3 4.2 2.6 17.9a2 2 0 0 0 1.7 3h15.4a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0z"/><path d="M12 9.5v4.5"/><path d="M12 17.4h.01"/></svg>`
	publicIconFolderZip = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3.5 7a2 2 0 0 1 2-2h4l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2V7z"/><path d="M12 10.5V16"/><path d="M9.5 13.5 12 16l2.5-2.5"/></svg>`
)

// pinFormTemplate is a dependency-free HTML page rendered when a share
// requires a PIN and none was provided.
var pinFormTemplate = template.Must(template.New("pin").Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Enter PIN</title>
` + publicPageStyle + `
{{.BrandCSS}}
<style>
.pin-input { width: 100%; padding: 13px 12px; font-size: 1.45rem; letter-spacing: 0.35em; text-align: center; border: 1.5px solid var(--px-line); border-radius: 10px; background: transparent; color: inherit; font-family: inherit; }
.pin-input:focus { outline: none; border-color: var(--px-accent); box-shadow: 0 0 0 3px var(--px-accent-soft); }
</style>
</head><body>
<main class="wrap">
{{.BrandHead}}
<form class="card" method="post" action="{{.Action}}">
<div class="icon-badge">` + publicIconLock + `</div>
<h1>This share is PIN-protected</h1>
<p class="sub">Enter the PIN to access the file.</p>
<input class="pin-input" type="password" name="pin" inputmode="numeric" autocomplete="one-time-code" aria-label="PIN" autofocus required>
{{if .Error}}<div class="error" role="alert">{{.Error}}</div>{{end}}
<button class="btn" type="submit">Unlock</button>
</form>
{{.Footer}}
</main>
</body></html>`))

func (h *Share) renderPINForm(w http.ResponseWriter, r *http.Request, token, errMsg string) {
	chrome := h.chrome(r) /* wiring:e1 */
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = pinFormTemplate.Execute(w, map[string]any{
		"Action":    shareURLPath(token),
		"Error":     errMsg,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    chrome.FooterEN,
	})
}

// renderUnlockedPage tells the user the PIN matched and auto-posts a
// confirmed download to the same URL after a brief delay. Without
// this the browser jumps straight from the PIN form to a streamed
// attachment and the user has no indication of whether their PIN
// was accepted.
func (h *Share) renderUnlockedPage(w http.ResponseWriter, r *http.Request, token, pin string) {
	chrome := h.chrome(r) /* wiring:e1 */
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = unlockedTemplate.Execute(w, map[string]any{
		"Action":    shareURLPath(token) + "?confirmed=1",
		"PIN":       pin,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    chrome.FooterTR,
	})
}

// renderErrorPage shows a styled HTML error page (404 / expired)
// instead of plain text.
func (h *Share) renderErrorPage(w http.ResponseWriter, r *http.Request, status int, title, body string) {
	chrome := h.chrome(r) /* wiring:e1 */
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = errorPageTemplate.Execute(w, map[string]any{
		"Title":     title,
		"Body":      body,
		"Code":      status,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    chrome.FooterEN,
	})
}

var unlockedTemplate = template.Must(template.New("unlocked").Parse(`<!doctype html>
<html lang="tr"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>PIN accepted</title>
` + publicPageStyle + `
{{.BrandCSS}}
</head><body>
<main class="wrap">
{{.BrandHead}}
<div class="card">
<div class="icon-badge ok">` + publicIconCheck + `</div>
<h1>PIN doğru</h1>
<p class="sub" style="margin-bottom:0"><span class="spinner" aria-hidden="true"></span>İndirme birazdan başlayacak…</p>
<form id="f" method="post" action="{{.Action}}" style="display:none">
<input type="hidden" name="pin" value="{{.PIN}}">
</form>
<script>setTimeout(function(){document.getElementById('f').submit();}, 700);</script>
</div>
{{.Footer}}
</main>
</body></html>`))

var errorPageTemplate = template.Must(template.New("err").Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
` + publicPageStyle + `
{{.BrandCSS}}
<style>
.code { display: inline-block; margin-bottom: 14px; padding: 3px 10px; border: 1px solid var(--px-line); border-radius: 999px; font-size: 0.75rem; font-weight: 700; letter-spacing: 0.08em; color: var(--px-muted); font-variant-numeric: tabular-nums; }
</style>
</head><body>
<main class="wrap">
{{.BrandHead}}
<div class="card">
<div class="icon-badge err">` + publicIconAlert + `</div>
<div class="code">{{.Code}}</div>
<h1>{{.Title}}</h1>
<p class="sub" style="margin-bottom:0">{{.Body}}</p>
</div>
{{.Footer}}
</main>
</body></html>`))
