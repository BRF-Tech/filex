package handlers

import (
	"context"
	cryptoRand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
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
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/sharezip"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
	"github.com/brf-tech/filex/backend/internal/zipstream"

	"github.com/brf-tech/filex/backend/internal/httpx"
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
	// Thumbs renders the gallery tiles on the public folder page. Nil-safe:
	// unwired, that page falls back to streaming the ORIGINALS — which is what
	// it always did, and what made a folder of photos crawl on open.
	Thumbs *thumb.Pipeline
	// DefaultLocale is the public pages' fallback language (the server's own
	// `default_locale`). The visitor's request wins over it — see publicLocale.
	DefaultLocale string
	// Body resolves where a shared file's bytes are: the driver, or filex's
	// staging area while a staged upload is still transferring. Nil-safe.
	Body *filebody.Resolver
	// Tenants resolves which origin a minted /s/ or /d/ link is built on.
	Tenants tenanturl.Resolver
	// Shell is the SPA document a JavaScript browser gets at /s/{token}; the
	// pages in this file are the no-JS answer behind it. nil (frontend not
	// bundled) means every visitor gets the Go pages. See public_shell.go for
	// which request gets which.
	Shell http.Handler
	// Apps lets the no-JS page list the copies an app link exposed. nil means
	// an app link falls back to "this needs a browser with JavaScript".
	Apps *AppPlugins
	// Index keeps the search index in step when sharing records a file the
	// catalogue had not seen yet (catalogueOnDemand). nil-safe.
	Index *search.Index
}

// AttachIndex wires the search index for on-demand cataloguing.
func (h *Share) AttachIndex(idx *search.Index) { h.Index = idx }

// AttachTenants wires the shared per-request origin resolver (internal/tenanturl).
func (h *Share) AttachTenants(rv tenanturl.Resolver) { h.Tenants = rv }

// AttachBody wires the byte-source resolver so a share link serves a file that
// is still being transferred to the backend.
func (h *Share) AttachBody(b *filebody.Resolver) { h.Body = b }

// AttachBranding wires the shared branding source (wiring:e1).
func (h *Share) AttachBranding(b *BrandingSource) { h.Branding = b }

// AttachThumbs wires the thumbnail pipeline used by the public folder page.
func (h *Share) AttachThumbs(p *thumb.Pipeline) { h.Thumbs = p }

// AttachLocale sets the fallback language for the public pages — used when the
// visitor's browser asks for neither of the two we ship.
func (h *Share) AttachLocale(def string) { h.DefaultLocale = def }

// pub resolves everything a public page needs to render in ONE language: the
// tag for <html lang>, the string table, and the matching footer. Resolved once
// per request so a page cannot mix two languages (which is exactly what the PIN
// gate and the page behind it used to do).
func (h *Share) pub(r *http.Request) (lang string, t map[string]string, footer template.HTML) {
	lang, t, _, footer = publicPageLang(h.Branding, r, h.DefaultLocale)
	return lang, t, footer
}

// chrome computes the branded page fragments for one request (wiring:e1).
func (h *Share) chrome(r *http.Request) publicChrome { return publicChromeFor(h.Branding, r) }

// AttachACL wires the RBAC resolver so minting a public share link requires
// ≥editor on the target node (sharing grants outside access — a write action).
func (h *Share) AttachACL(r *acl.Resolver) { h.ACL = r }

// AttachShell wires the SPA document served to JavaScript browsers.
func (h *Share) AttachShell(shell http.Handler) { h.Shell = shell }

// AttachApps wires the app-plugin handler for the no-JS app page.
func (h *Share) AttachApps(a *AppPlugins) { h.Apps = a }

// NewShare constructs a Share handler. zipCache enables the folder-share ZIP
// cache (pass a disabled/nil cache to stream fresh on every folder download).
func NewShare(svc *share.Service, store db.Store, resolver func(int64) (storage.Driver, error), publicURL string, zipCache *sharezip.Cache) *Share {
	return &Share{
		Service:         svc,
		Store:           store,
		StorageResolver: resolver,
		PublicURL:       strings.TrimRight(publicURL, "/"),
		Tenants:         tenanturl.New(store, publicURL, false),
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
	ID          int64      `json:"id"`
	UUID        string     `json:"uuid"` // alias for token (frontend uses uuid in delete URL)
	Token       string     `json:"token"`
	URL         string     `json:"url"`
	Kind        string     `json:"kind,omitempty"` // "download" | "drop"
	Path        string     `json:"path,omitempty"`
	Filename    string     `json:"filename,omitempty"`
	HasPin      bool       `json:"has_pin"`
	PasswordPin string     `json:"password_pin,omitempty"` // ONLY on creation when we generated it
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	// ExpiryClamped is true when the server shortened (or set) the expiry to
	// honour the max-TTL setting — so a UI can say "valid until X" instead of
	// echoing a date the user never picked.
	ExpiryClamped bool `json:"expiry_clamped,omitempty"`
	MaxDownloads  *int `json:"max_downloads,omitempty"`
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
			// ⚠ The file may be on the storage and not in the catalogue yet —
			// the explorer lists it from the driver, so a person can pick it
			// and press Share, and this answered 404 "file not found".
			if n := h.catalogueForShare(r, req.Path); n != nil {
				resolved, err = n.ID, nil
			}
		}
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

	// Resolve the node ONCE, before anything decides what may be done to it.
	//
	// ⚠ This used to happen twice — once inside the RBAC branch, once inside
	// the drop branch — and neither lookup asked whose node it was. `GetNode`
	// is one of the pass-through methods tenantstore does NOT confine (it
	// wraps exactly three list queries), so in multi-tenant mode a
	// client-supplied `node_id` reached straight across the tenant boundary.
	//
	// The aclAllowID check below is not a boundary either, which is what let
	// this survive a reading of the code: storages.rbac_enabled defaults
	// FALSE, and with it off acl.Set.Effective returns the account-role base
	// for every path — Editor for a plain `user`. The gate was satisfied by
	// anyone with an account, on anyone's file.
	//
	// The {"path": …} shape of this same request was always safe, because it
	// resolves through the CONFINED ListEnabledStorages (resolveNodeIDFromPath
	// below). Two body shapes, one handler, different security properties —
	// that difference is the tell.
	//
	// ⚠ The refusal is the same 404 "not found" a node id that never existed
	// already produced. A distinct body (or a 403) would turn this into an
	// oracle: repeated over an id range it enumerates the other customers'
	// files. Written inline rather than through ownsNode for exactly that
	// reason — ownsNode writes its own "<what> not found" body, which here
	// would be distinguishable from the genuine miss beside it.
	node, err := h.Store.GetNode(r.Context(), nodeID)
	if err != nil || node == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if scope, confined := confinedScope(r.Context()); confined && !scope.CanAccessStorage(node.StorageID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// Root confinement: the `node_id` shape skips confine.Middleware, so a
	// confined token could mint a public link to a file outside its folder.
	// The `path` shape was already safe (resolveNodeIDFromPath is confined).
	// Same 404 as the miss above.
	if !rootAllows(r.Context(), h.Store, node.StorageID, node.Path) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// A public link to `.filex-trash` would hand every deleted file on the
	// storage to whoever holds the URL; to `.filex-open`, other people's
	// open documents.
	if gate(w, r, h.ACL, node.StorageID, writegate.Names(node.Path)) {
		return
	}

	// RBAC: creating a public share is an outbound-access grant → ≥editor.
	if h.ACL != nil {
		if !aclAllowID(r.Context(), h.ACL, h.Store, node.StorageID, node.Path, acl.LevelEditor) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
			return
		}
	}

	// File-drop links mint a public UPLOAD endpoint into a folder — validate
	// the target is a directory up front so a public uploader can never be
	// pointed at (and made to overwrite) a single file.
	//
	// ⚠ This is also why the ownership check above is not merely a disclosure
	// fix: a drop link over somebody else's folder is a public endpoint that
	// DEPOSITS files into another tenant's storage. Same create path, so one
	// check closes both halves.
	isDrop := req.Kind == model.ShareKindDrop
	if isDrop && node.Type != model.NodeTypeDirectory {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "drop links require a folder target"})
		return
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
	requestedExpiry := opts.ExpiresAt
	sh, err := h.Service.Create(r.Context(), opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// The service may have shortened (or set) the expiry — see
	// share.ClampExpiry. Say so explicitly rather than letting the caller
	// notice a date it did not ask for.
	expiryClamped := sh.ExpiresAt != nil && (requestedExpiry == nil || !requestedExpiry.Equal(*sh.ExpiresAt))

	linkURL := h.shareURL(r, sh.Token)
	if sh.IsDrop() {
		linkURL = h.dropURL(r, sh.Token)
	}
	inner := shareCreateRespInner{
		ID:            sh.ID,
		UUID:          sh.Token,
		Token:         sh.Token,
		URL:           linkURL,
		Kind:          sh.Kind,
		HasPin:        sh.PinHash != "",
		PasswordPin:   pinGenerated,
		ExpiresAt:     sh.ExpiresAt,
		ExpiryClamped: expiryClamped,
		MaxDownloads:  sh.MaxDownloads,
	}
	// `node` was already resolved (and ownership-checked) at the top of the
	// handler; re-reading it here would be a third lookup of the same row.
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
		// The link, not the file behind it: the event is "a share now
		// exists", and the thing to look at is the share.
		Target: notify.ShareTarget(sh.Token),
	}
	if node != nil {
		shareEv.Node = &notify.NodeRef{StorageID: node.StorageID, Path: node.Path, Name: node.Name, Size: node.Size}
	}
	emitFileEvent(r.Context(), shareEv)

	// ⭐ Build the folder's ZIP NOW, not when somebody clicks download.
	// The background warmer would have got to it within five minutes, but the
	// person who just created the link is usually the next person to open it —
	// so the five minutes were being spent watching a progress bar instead of
	// being spent before anyone was waiting. Costs nothing new: it is the same
	// build the warmer was going to do anyway, moved earlier.
	// ⚠ Not for a file request: a drop link is upload-only, so its archive
	// could never be downloaded by anyone. It used to be built anyway — the
	// full folder read from object storage, for a file that the warmer never
	// looks at again and (before the sweeper) nothing ever deleted.
	if sh.Kind != model.ShareKindDrop {
		h.warmFolderZip(node)
	}
	h.warmFolderThumbs(node)

	auth.SetAuditTarget(r.Context(), strconv.FormatInt(sh.ID, 10), node.Path)
	// Dual envelope: nested `share` for the SFC + flat fields at the
	// top level for legacy embed.js. Cheap to ship both.
	writeJSON(w, http.StatusOK, map[string]any{
		"share":          inner,
		"id":             inner.ID,
		"token":          inner.Token,
		"url":            inner.URL,
		"kind":           inner.Kind,
		"has_pin":        inner.HasPin,
		"expires_at":     inner.ExpiresAt,
		"expiry_clamped": inner.ExpiryClamped,
		"max_downloads":  inner.MaxDownloads,
	})
}

// sharedThumbMaxSource caps what the public page will render on demand. Above
// it, the tile falls back to the original — a visitor with a link must not be
// able to make the server decode an arbitrarily large image per request, and a
// file that big is not a photo somebody is browsing a wall of.
const sharedThumbMaxSource = 64 << 20 // 64 MiB

// serveSharedThumb writes the cached thumbnail for a file inside a shared
// folder and reports whether it did. False means "no thumbnail to serve" and
// the caller streams the original, which is what this endpoint always did.
//
// The thumbnail is the SAME artefact the app's own gallery uses — keyed by node
// id, rendered once, cached on disk — so the public page costs nothing extra
// after the first visit. When the file has never had one rendered, it is
// rendered here rather than dispatched in the background: the visitor is
// looking at the tile right now, and a background job would leave them staring
// at a broken image until they reloaded.
func (h *Share) serveSharedThumb(w http.ResponseWriter, r *http.Request, storageID int64, full string, size int64) bool {
	if h.Thumbs == nil || h.Store == nil {
		return false
	}
	ctx := r.Context()
	n, err := h.Store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, full))
	// An unindexed storage has no node, so no thumbnail can exist for it.
	if err != nil || n == nil || n.Type != model.NodeTypeFile {
		return false
	}
	if !h.thumbReady(ctx, n.ID) {
		if size > sharedThumbMaxSource {
			return false
		}
		if err := h.Thumbs.GenerateThumb(ctx, n); err != nil {
			return false
		}
		if !h.thumbReady(ctx, n.ID) {
			return false
		}
	}
	f, err := os.Open(h.Thumbs.CachePath(n.ID))
	if err != nil {
		return false
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// ⚠ Reported to the caller as handled even if the copy dies mid-stream:
	// the headers are already out, so falling through would write a second
	// response body into the same request.
	_, _ = io.Copy(w, f)
	return true
}

func (h *Share) thumbReady(ctx context.Context, nodeID int64) bool {
	t, err := h.Store.GetThumbnail(ctx, nodeID)
	return err == nil && t != nil && t.State == "ready"
}

// zipWarmTimeout bounds a share-creation warm. Generous — a folder share can
// be tens of gigabytes — but bounded, so a build that wedges on a sick storage
// backend releases its slot instead of living for the process's lifetime.
const zipWarmTimeout = 30 * time.Minute

// warmFolderZip pre-builds the ZIP for a freshly created FOLDER share.
//
// ⚠ Detached context on purpose: the request is about to be answered, and
// `r.Context()` is cancelled the moment it is — a warm started on it would be
// killed a few milliseconds in, leaving a partial build and the same wait the
// downloader had before.
//
// Silent by design. This is an optimisation on top of a cache that already
// falls back to streaming a fresh ZIP; a failure here must not fail the share
// creation the user actually asked for.
func (h *Share) warmFolderZip(node *model.Node) {
	if h.Zip == nil || !h.Zip.Enabled() || node == nil || node.Type != model.NodeTypeDirectory {
		return
	}
	drv, err := h.StorageResolver(node.StorageID)
	if err != nil || drv == nil {
		return
	}
	path, id := node.Path, node.ID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), zipWarmTimeout)
		defer cancel()
		_, _ = h.Zip.Warm(ctx, drv, path, id)
	}()
}

// prewarmThumbMax bounds how many tiles one share creation renders up front. A
// folder of receipts is tens of files; a photo archive can be tens of
// thousands, and rendering all of them because somebody minted a link is not a
// trade anybody asked for. Past the cap the rest render on first view — still
// far cheaper than shipping the originals.
const prewarmThumbMax = 500

// warmFolderThumbs renders the gallery tiles for a freshly shared folder.
//
// The thumbnail cache makes the SECOND visit fast; this makes the FIRST one
// fast, and the first is the visit that matters: whoever creates a link
// normally opens it straight away to check it, which is exactly when the page
// used to crawl.
//
// Same shape as warmFolderZip — detached, asynchronous, bounded, silent. A
// failure here costs a slower first paint, never a failed share.
func (h *Share) warmFolderThumbs(node *model.Node) {
	if h.Thumbs == nil || h.Store == nil || node == nil || node.Type != model.NodeTypeDirectory {
		return
	}
	drv, err := h.StorageResolver(node.StorageID)
	if err != nil || drv == nil {
		return
	}
	root, storageID := node.Path, node.StorageID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), zipWarmTimeout)
		defer cancel()
		rendered := 0
		var walk func(dir string)
		walk = func(dir string) {
			if rendered >= prewarmThumbMax || ctx.Err() != nil {
				return
			}
			objs, err := drv.List(ctx, dir)
			if err != nil {
				return
			}
			for _, o := range objs {
				if rendered >= prewarmThumbMax || ctx.Err() != nil {
					return
				}
				if browseSkip(o.Name) {
					continue
				}
				child := joinShareRel(dir, o.Name)
				if o.Kind == storage.KindDirectory {
					walk(child)
					continue
				}
				// Only what the public gallery actually inlines, and only what
				// serveSharedThumb would have rendered on demand anyway.
				if share.ClassifyEntry(o.Name, false) != share.EntryImage || o.Size > sharedThumbMaxSource {
					continue
				}
				n, err := h.Store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, child))
				if err != nil || n == nil || n.Type != model.NodeTypeFile || h.thumbReady(ctx, n.ID) {
					continue
				}
				if err := h.Thumbs.GenerateThumb(ctx, n); err == nil {
					rendered++
				}
			}
		}
		walk(root)
	}()
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

	// Whose node is it? Same hole as HandleCreate and the same reasoning: the
	// `node_id` shape reaches GetNode unconfined while the `path` shape goes
	// through ListEnabledStorages. What leaked here is worse than metadata —
	// the rows below carry the share TOKEN in `url`, so a foreign node id
	// handed the caller a working public link to somebody else's file.
	//
	// ⚠ The refusal is this endpoint's own "nothing here" answer — 200 with
	// an empty list, exactly what an unresolvable path and a node id that
	// does not exist already produce. A 404 would be MORE informative than
	// the miss it is imitating, i.e. an existence oracle; that is the whole
	// reason the refusals in this audit copy the shape of a genuine miss
	// rather than announcing themselves.
	node, err := h.Store.GetNode(r.Context(), nodeID)
	if err != nil || node == nil {
		writeJSON(w, http.StatusOK, map[string]any{"shares": []any{}})
		return
	}
	if scope, confined := confinedScope(r.Context()); confined && !scope.CanAccessStorage(node.StorageID) {
		writeJSON(w, http.StatusOK, map[string]any{"shares": []any{}})
		return
	}
	// Root confinement: the `node_id` shape skips confine.Middleware. The row
	// carries the share TOKEN, so an out-of-root leak here is a working link to
	// somebody else's file — refused with this endpoint's own empty-list shape.
	if !rootAllows(r.Context(), h.Store, node.StorageID, node.Path) {
		writeJSON(w, http.StatusOK, map[string]any{"shares": []any{}})
		return
	}

	// RBAC: seeing an item's links is the same bar as minting one (≥editor).
	if h.ACL != nil {
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
	// Resolved once: the origin is a property of the request, not of the row,
	// and asking per row would be one provider lookup per link.
	base := h.Tenants.FromRequest(r)
	out := make([]map[string]any, 0, len(rows))
	for _, sh := range rows {
		if sh.ExpiresAt != nil && !sh.ExpiresAt.After(now) {
			continue // revoked / expired — keep it out of the active list
		}
		if user != nil && !user.IsAdmin() && (sh.CreatedBy == nil || *sh.CreatedBy != user.ID) {
			continue // non-admins only manage their own links
		}
		link := base + "/s/" + sh.Token
		if sh.IsDrop() {
			link = base + "/d/" + sh.Token
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
	hash := pathkey.Hash(st.ID, clean)
	node, err := h.Store.GetNodeByPath(ctx, st.ID, hash)
	if err != nil || node == nil {
		return 0, fmt.Errorf("file not found: %s", fullPath)
	}
	return node.ID, nil
}

// catalogueForShare records, for the Share dialog, an entry the storage has
// and the catalogue has not seen yet (catalogueOnDemand), or answers nil.
//
// ⚠ Every question HandleCreate asks of a node is asked of the PATH first,
// because this writes a row: the storage has to be one the caller's tenant
// reaches (ListEnabledStorages is the confined listing), the path has to be
// inside a root-confined token's folder, and the caller needs editor rights
// on it. A caller who fails any of them gets the same 404 a missing file
// gets, and nothing is written.
func (h *Share) catalogueForShare(r *http.Request, fullPath string) *model.Node {
	ctx := r.Context()
	adapter, rel := splitAdapterPath(strings.ReplaceAll(fullPath, "\\", "/"))
	clean := strings.TrimRight(path.Clean("/"+rel), "/")
	if adapter == "" || clean == "" {
		return nil
	}
	storages, err := h.Store.ListEnabledStorages(ctx)
	if err != nil {
		return nil
	}
	var st *model.Storage
	for _, s := range storages {
		if s.Name == adapter {
			st = s
		}
	}
	if st == nil || !rootAllows(ctx, h.Store, st.ID, clean) {
		return nil
	}
	if h.ACL != nil && !aclAllowID(ctx, h.ACL, h.Store, st.ID, clean, acl.LevelEditor) {
		return nil
	}
	n, err := catalogueOnDemand(ctx, h.Store, h.StorageResolver,
		protocolsync.New(h.Store, h.Index, h.Thumbs, writehook.OriginManager), st.ID, clean)
	if err != nil {
		return nil
	}
	return n
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
	if err != nil || sh == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	// Whose share is it? The non-admin half of the check below was right all
	// along — a plain user only manages the links they created. The admin
	// half was `!user.IsAdmin() && …`, and under multi-tenancy "an admin" is
	// the admin of EVERY tenant, so that early-out handed every share on the
	// instance to any tenant's administrator.
	//
	// GetShareByID is another pass-through, so the row has to be walked back
	// to its storage: share → node → storage. ⚠ Fails closed — an id whose
	// node cannot be read is refused rather than treated as ownerless, which
	// is the same choice ownsNode makes and for the same reason.
	//
	// Refusal is the 404 already used for a share id that does not exist —
	// written inline rather than through ownsStorage, whose "<what> not
	// found" body would be distinguishable from that miss. The pre-existing
	// 403 stays for the case it was written for (a real share in your own
	// tenant that you did not create): there the caller is allowed to know
	// the row exists.
	if scope, confined := confinedScope(r.Context()); confined {
		n, nerr := h.Store.GetNode(r.Context(), sh.NodeID)
		if nerr != nil || n == nil || !scope.CanAccessStorage(n.StorageID) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
	}
	// Root confinement: a confined token may only revoke links on files inside
	// its folder — the share id skips confine.Middleware. Walk share → node →
	// path; the same 404 the missing-share branch above produces.
	if _, confined := confine.RootFrom(r.Context()); confined {
		n, nerr := h.Store.GetNode(r.Context(), sh.NodeID)
		if nerr != nil || n == nil || !rootAllows(r.Context(), h.Store, n.StorageID, n.Path) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
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
	var reg *wasmplugin.Registry
	if h.Apps != nil {
		reg = h.Apps.Registry
	}
	appLinkEnded(r.Context(), reg, sh, "revoked")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// appLinkEnded is called wherever a PERSON ends a link — My shares' revoke,
// the administrator's Revoke and Delete. For a link an app opened, it brings
// that app's hourly wake-up forward (wasmplugin.Registry.WakeSoon), and the
// app's own `tick` asks after its links and finds this one gone.
//
// ⚠⚠ Why this exists: a signing link revoked from Shares stopped working, and
// the signature request it belonged to stayed "Sent · 0/1 signed" — the file
// frozen, the requester waiting — because nothing ever told the app. Measured
// 2026-09-21: revoke, then a wake-up that saw the request and scheduled
// nothing. The app now asks on every wake-up; this makes the asking prompt.
//
// ⚠ Nothing is sent TO the app — no event, no payload. The links are the
// app's own facts to read, and a second channel beside the wake-up would be a
// second thing to deliver, order and retry. nil-safe on every argument, and a
// link no app opened is not the app layer's business at all.
func appLinkEnded(ctx context.Context, reg *wasmplugin.Registry, sh *model.Share, how string) {
	if reg == nil || sh == nil || sh.PluginID == 0 {
		return
	}
	reg.WakeSoon(ctx, sh.PluginID, "one of its links (share "+strconv.FormatInt(sh.ID, 10)+") was "+how+" from Shares")
}

// HandleMetadata returns metadata for a share token (no PIN check).
//
// Used by the embed.js viewer to decide whether to render a PIN prompt.
func (h *Share) HandleMetadata(w http.ResponseWriter, r *http.Request) {
	tok := pathParam(r, "token")
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
			remaining := *sh.MaxDownloads - sh.CappedCount()
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
	// A JavaScript browser gets the branded public surface; everything else
	// — no-JS browsers, curl, download managers, the PIN form's own POST —
	// falls through to the pages below (public_shell.go says why).
	if wantsPublicShell(r, h.Shell) {
		h.Shell.ServeHTTP(w, r)
		return
	}
	tok := pathParam(r, "token")
	pin := h.extractPIN(r)

	sh, err := h.Store.GetShareByToken(r.Context(), tok)
	if err != nil {
		h.renderErrorPage(w, r, http.StatusNotFound, "notfound")
		return
	}
	if sh.IsExpired(time.Now()) {
		h.renderErrorPage(w, r, http.StatusNotFound, "expired")
		return
	}

	// ⚠ An unlock cookie counts as having answered: the visitor entered the
	// PIN on this browser — possibly in the SPA shell, before JavaScript
	// failed or before they followed a plain link — and asking a second time
	// for something already answered is how a public page loses the person it
	// was sent to.
	unlocked := sh.PinHash == "" || shareUnlocked(h.Service, r, sh)

	// PIN required and none supplied: the form.
	if !unlocked && pin == "" {
		h.renderPINForm(w, r, tok, "")
		return
	}

	// The PIN gate: bcrypt PLUS the five-strikes-then-ten-minutes lock
	// (internal/share/pin.go).
	//
	// ⚠ Not Service.Resolve any more. That one compares the hash and counts
	// nothing, so a four-digit PIN on a /s/ link could be walked through at
	// the speed of HTTP — the lock existed, but only on the app-plugin pages.
	// One gate now, for every public link.
	if sh.PinHash != "" && pin != "" {
		switch err := h.Service.CheckPIN(r.Context(), sh, pin); {
		case errors.Is(err, share.ErrLocked):
			h.renderPINForm(w, r, tok, "pin_locked")
			return
		case err != nil:
			h.renderPINForm(w, r, tok, "pin_wrong")
			return
		}
		// Mint the unlock so the visitor's NEXT request — the confirmed
		// download, a file off an app page — carries no PIN at all.
		http.SetCookie(w, &http.Cookie{
			Name: share.CookieName(sh.Token), Value: h.Service.MintUnlock(sh.Token),
			Path: "/", HttpOnly: true, Secure: requestIsHTTPS(r), SameSite: http.SameSiteLaxMode,
			MaxAge: int(share.UnlockTTL().Seconds()),
		})
	}
	resolved := sh

	// Confirmed download step — when a PIN-protected share's POST
	// successfully resolved and the client hasn't yet seen the
	// "PIN accepted" page, render the success screen and let it
	// auto-submit a hidden form to itself with ?confirmed=1 so the
	// stream comes second. This gives the user clear feedback that
	// the PIN matched before the browser hijacks the page with an
	// `attachment` Content-Disposition.
	//
	// ⚠ Skipped for an app link: nothing is about to be attached there, so
	// the interstitial would put a second page between the visitor and the
	// thing they were sent for.
	if sh.PinHash != "" && !resolved.IsApp() && r.URL.Query().Get("confirmed") != "1" && r.Method == http.MethodPost {
		h.renderUnlockedPage(w, r, tok, pin)
		return
	}

	// An app link's body is the app's surface, and the only bytes a visitor
	// may have are the copies the app exposed. ⚠ The node behind it is the
	// ANCHOR the app's state and its follow-up job hang on — never a
	// download: no storage driver is opened for an anonymous request, which
	// is the property the app pages had before they became shares.
	if resolved.IsApp() {
		h.renderAppPage(w, r, tok, resolved)
		return
	}

	node, err := h.Store.GetNode(r.Context(), resolved.NodeID)
	if err != nil {
		http.Error(w, "node missing", http.StatusNotFound)
		return
	}
	// A link never serves filex's own bookkeeping. The storage root has no row
	// and cannot be shared, but a row INSIDE `.versions/` can exist (older
	// scans minted one per snapshot folder and file), and a share names a row
	// by id: such a link listed, zipped and streamed the previous contents of
	// somebody's files.
	if syspath.Hidden(node.Path) {
		h.renderErrorPage(w, r, http.StatusNotFound, "notfound")
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
		/* wiring:d2 — with NO zip parameter a folder share now renders the
		   browse page (≥60% images/video → gallery, otherwise a list); the ZIP
		   flow stays exactly as it was, behind the page's "Tümünü indir"
		   (Download all) → ?zip=… */
		if r.URL.Query().Get("zip") == "" {
			h.renderFolderBrowse(r.Context(), w, r, drv, node, resolved, pin)
			return
		}
		h.serveFolderZip(r.Context(), w, r, drv, node.StorageID, node.Path, node.Name, node.ID, resolved.ID, pin)
		return
	}

	// Where are this file's bytes? Resolved BEFORE the claim below, so a link
	// whose staging has vanished answers an error without spending one of its
	// downloads — the claim itself stays exactly where it was relative to the
	// bytes, which is what the cap depends on.
	src, err := h.Body.Resolve(r.Context(), drv, node.StorageID, node.Path, node)
	if err != nil {
		stagingGoneText(w, err)
		return
	}

	// ⭐ Claim the download BEFORE serving it. The cap used to be checked
	// against a counter that was only bumped once the bytes had left, so every
	// request that started while an earlier one was still streaming read the
	// same pre-download count and was let through — a link capped at one
	// download handed three full files to three overlapping clients on
	// fm.example.com. Claiming first makes "3 downloads" mean three.
	if !h.claimDownload(w, r, resolved.ID) {
		return
	}

	// Use a presigned URL when the driver supports it AND the operator
	// hasn't opted out via `disable_presign: true` in storage config.
	// Honor `Capabilities().Presign` so drivers can advertise no-presign
	// at runtime (e.g. Hetzner Object Storage / Ceph RGW which produces
	// SignatureDoesNotMatch on AWS SDK v2 SigV4 — sweep-2026-05-09 bug 23).
	// When presign is disabled, fall through to the backend-stream path
	// below.
	//
	// ⚠ Never while staged: a presigned URL points at the backend, and the
	// backend is precisely what does not have this object yet. Redirecting
	// there would hand the visitor a 404 (or, on an overwrite, the previous
	// version) with the download already charged against the link.
	if pres, ok := drv.(storage.Presigner); ok && !src.Staged && drv.Capabilities().Presign {
		if u, err := pres.PresignDownload(r.Context(), node.Path, 5*time.Minute); err == nil && u != "" {
			http.Redirect(w, r, u, http.StatusFound)
			return
		}
	}

	rc, err := src.Open(r.Context())
	if err != nil {
		// Nothing was served — give the slot back rather than charge the
		// visitor for our storage error.
		_ = h.Service.ReleaseDownload(r.Context(), resolved.ID)
		stagingGoneText(w, err)
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
	w.Header().Set("Content-Disposition", httpx.ContentDisposition(disposition, node.Name))
	declareBodyLength(r.Context(), w, src, node)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The slot is already claimed; a transfer that dies half-way still counts
	// (the visitor got bytes, and a refund here would reopen the very gap this
	// claim closes).
	_, _ = io.Copy(w, rc)
}

// claimDownload takes one download off the share's cap before anything is
// served, and renders the expired page when there is none left. Reporting
// false means the caller must write NOTHING further to w.
//
// A failing counter is treated as "no slot": the alternative is serving an
// unbounded number of downloads whenever the database hiccups, which is the
// exact failure the cap exists to prevent.
func (h *Share) claimDownload(w http.ResponseWriter, r *http.Request, shareID int64) bool {
	if h.claimDownloadSlot(r, shareID) {
		return true
	}
	h.renderErrorPage(w, r, http.StatusNotFound, "expired")
	return false
}

// claimDownloadSlot is claimDownload without the HTML page, for the endpoints
// that answer in plain text (the browse page's /f/ media route).
func (h *Share) claimDownloadSlot(r *http.Request, shareID int64) bool {
	ok, err := h.Service.ReserveDownload(r.Context(), shareID)
	return err == nil && ok
}

// streamFolderZip walks `root` on the driver and writes every file under it
// into a ZIP streamed to w. Entry names are relative to `root`, so the archive
// unpacks into a clean tree. Internal dirs (trash, thumbnails) are skipped, and
// individually unreadable files are skipped rather than aborting the whole
// download. The write is streaming — no full buffer — so large folders are fine.
//
// Every member is opened through filebody, so a file the driver still holds an
// OLDER copy of — an overwrite whose staged bytes have not landed yet — is
// archived with the version the user committed rather than the one it replaced.
// ⚠ The walk itself is still the driver's listing, so a brand-new staged file
// (no object on the backend at all) is not in the archive; see the note on
// serveFolderZip.
//
// ⚠ The walk and the zip loop both used to be written out here, a second time
// in internal/sharezip (the cached twin of this very archive) and a third time
// would have joined them for the authenticated selection download. They are now
// one walk (sharezip.CollectFiles) and one writer (zipstream.Write). What is
// still local to this function is the only thing that was ever different: the
// bytes come from filebody rather than straight off the driver.
//
// ⚠ No Content-Length. There is no way to know one before the last member is
// deflated, and this repo has already shipped a download that promised a length
// it did not have (see declareBodyLength). Chunked is the honest answer.
func (h *Share) streamFolderZip(ctx context.Context, w http.ResponseWriter, drv storage.Driver, storageID int64, root, name string) error {
	files, err := sharezip.CollectFiles(ctx, drv, root)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", httpx.ContentDisposition("attachment", name+".zip"))
	w.Header().Set("X-Content-Type-Options", "nosniff")

	members := make([]zipstream.Member, 0, len(files))
	for _, f := range files {
		p := f.Path
		members = append(members, zipstream.Member{
			Name:  f.Rel,
			Size:  f.Size,
			Mtime: f.Mtime,
			Open: func(c context.Context) (io.ReadCloser, error) {
				src, err := h.Body.Resolve(c, drv, storageID, p, nil)
				if err != nil {
					return nil, err
				}
				return src.Open(c)
			},
		})
	}
	skips, err := zipstream.Write(ctx, w, members, zipstream.Options{
		Flush:    flusher(w),
		Manifest: incompleteArchiveNote,
	})
	for _, s := range skips {
		slog.Warn("folder share archive: member left out", slog.String("entry", s.Name),
			slog.Bool("partial", s.Partial), slog.String("err", s.Err.Error()))
	}
	return err
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
//
// ⚠ Known gap, deliberate: the CACHED path (internal/sharezip) is not
// staging-aware. Its file list and its cache key (the content signature over
// path+size+mtime) both come from drv.List, so a staged file is invisible to it
// and an overwritten one signs with the pre-overwrite metadata. Making only its
// reads staging-aware would produce a cached archive keyed by stale metadata —
// a worse failure than the current one, because it would then be served to
// everyone until the signature changed. Closing it properly means merging the
// node catalogue into the walk, which is a listing change, not a read change.
func (h *Share) serveFolderZip(ctx context.Context, w http.ResponseWriter, r *http.Request, drv storage.Driver, storageID int64, root, name string, nodeID, shareID int64, pin string) {
	// Both serving paths claim their download BEFORE writing (see
	// claimDownload): a folder ZIP takes long enough that two clicks used to
	// overlap comfortably inside the old check-then-count gap.
	stream := func() {
		if !h.claimDownload(w, r, shareID) {
			return
		}
		// streamFolderZip writes its headers up front, so a failure part-way
		// through has already handed bytes over — the claim stands.
		_ = h.streamFolderZip(ctx, w, drv, storageID, root, name)
	}
	serve := func(cachePath string) {
		if !h.claimDownload(w, r, shareID) {
			return
		}
		if err := serveZipFile(w, cachePath, name); err != nil && errors.Is(err, errZipUnopened) {
			// The cache file could not even be opened — nothing was served.
			_ = h.Service.ReleaseDownload(ctx, shareID)
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

// errZipUnopened marks a cached-ZIP failure that happened BEFORE any byte (or
// header) was written, which is the only case where a claimed download slot is
// safe to hand back.
var errZipUnopened = errors.New("share: cached zip could not be opened")

// serveZipFile streams a finished cached zip with an explicit Content-Length so
// the browser shows real download progress.
func serveZipFile(w http.ResponseWriter, cachePath, name string) error {
	f, err := os.Open(cachePath)
	if err != nil {
		return fmt.Errorf("%w: %v", errZipUnopened, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("%w: %v", errZipUnopened, err)
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", httpx.ContentDisposition("attachment", name+".zip"))
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
	lang, t, footer := h.pub(r)
	chrome := h.chrome(r) /* wiring:e1 */
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	// PinQuery is used only inside a JS string (html/template JS-escapes it); the
	// static href works for no-PIN shares and the script rewrites it with the pin
	// for PIN shares (which already require JS via the unlock page).
	_ = zipWaitTemplate.Execute(w, map[string]any{
		"Lang":      lang,
		"Dir":       pageDir(lang),
		"T":         t,
		"Name":      name,
		"Sub":       publicFill(t, "zip_sub", map[string]string{"name": name}),
		"Hint":      publicLinkSentence(t, "zip_hint", "zip_link", "dl", "?zip=wait"),
		"PinQuery":  pinQuery,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    footer,
	})
}

// zipWaitTemplate is a dependency-free progress page for a folder-share ZIP
// that's still being built.
var zipWaitTemplate = template.Must(template.New("zipwait").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.T.zip_title}}</title>
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
<h1>{{.T.zip_heading}}</h1>
<p class="sub">{{.Sub}}</p>
<div class="track" aria-hidden="true"><div id="bar" class="bar"></div></div>
<div class="pct"><span id="pct">%0</span></div>
<p class="hint">{{.Hint}}</p>
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

// shareURL returns the canonical /s/{token} URL for the origin r arrived on.
// An unconfigured PublicURL still yields the relative "/s/{token}".
func (h *Share) shareURL(r *http.Request, token string) string {
	return h.Tenants.FromRequest(r) + "/s/" + token
}

// dropURL returns the canonical /d/{token} public upload (file-drop) URL for
// the origin r arrived on.
func (h *Share) dropURL(r *http.Request, token string) string {
	return h.Tenants.FromRequest(r) + "/d/" + token
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
  --px-accent: #2f6ceb; --px-accent-hover: #2559c9;
  --px-accent-soft: rgba(47, 108, 235, 0.10);
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
    --px-accent: #5b8cff; --px-accent-hover: #8fb0f7;
    --px-accent-soft: rgba(91, 140, 255, 0.18);
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
.btn { display: block; width: 100%; margin-top: 18px; padding: 13px; border: 0; border-radius: 10px; font-size: 1rem; font-weight: 600; font-family: inherit; cursor: pointer; background: var(--px-accent); color: var(--px-on-accent, #fff); box-shadow: inset 0 0 0 1px var(--px-accent-edge, transparent); transition: background 0.15s ease; }
.btn:hover:not(:disabled) { background: var(--px-accent-hover); }
.btn:focus-visible { outline: 2px solid var(--px-accent); outline-offset: 2px; }
.btn:disabled { opacity: 0.5; cursor: default; }
.error { margin-top: 14px; padding: 10px 12px; border-radius: 9px; background: var(--px-err-soft); color: var(--px-err); font-size: 0.86rem; }
.brand { display: inline-flex; align-items: center; gap: 7px; color: var(--px-muted); font-size: 0.78rem; }
.brand svg { width: 16px; height: 16px; flex: none; }
.brand a { color: inherit; font-weight: 600; text-decoration: none; }
.brand a:hover { text-decoration: underline; color: var(--px-accent); }
.spinner { width: 15px; height: 15px; border: 2px solid currentColor; border-inline-end-color: transparent; border-radius: 50%; display: inline-block; vertical-align: -2px; animation: px-spin 0.8s linear infinite; opacity: 0.5; margin-inline-end: 8px; }
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
// ⚠ The brand mark is hand-typed here because this page is served by the Go
// binary with no bundler and no access to packages/core. It MUST be kept in
// step with web/src/components/LogoMark.vue, web/public/favicon.svg,
// web/public/icons/icon.svg, site/index.html and desktop/ui/*.html — this
// copy shipped the pre-rebrand indigo on the page strangers see for a whole
// wave after the others went blue (2026-09-13, found by the duplicate gate).
const publicBrandMark = `<svg viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="7" fill="#2f6ceb"/><path d="M7 11a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H9a2 2 0 0 1-2-2V11z" fill="none" stroke="#fff" stroke-width="1.8" stroke-linejoin="round"/><path d="M11.5 17.5l3 2.5 5.5-6" fill="none" stroke="#fff" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg>`

// The public-page footer line ("Shared with filex") is built per language by
// publicFooterLine (public_i18n.go) from the server catalogue.

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
<html lang="{{.Lang}}" dir="{{.Dir}}"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.T.pin_title}}</title>
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
<h1>{{.T.pin_heading}}</h1>
<p class="sub">{{.T.pin_sub}}</p>
<input class="pin-input" type="password" name="pin" inputmode="numeric" autocomplete="one-time-code" aria-label="{{.T.pin_aria}}" autofocus required>
{{if .Error}}<div class="error" role="alert">{{.Error}}</div>{{end}}
<button class="btn" type="submit">{{.T.pin_submit}}</button>
</form>
{{.Footer}}
</main>
</body></html>`))

// renderPINForm renders the gate. errKey is a string-table key ("pin_wrong")
// or empty — never a sentence, so the gate and the page behind it cannot end up
// in different languages.
func (h *Share) renderPINForm(w http.ResponseWriter, r *http.Request, token, errKey string) {
	lang, t, footer := h.pub(r)
	chrome := h.chrome(r) /* wiring:e1 */
	errMsg := ""
	if errKey != "" {
		errMsg = t[errKey]
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = pinFormTemplate.Execute(w, map[string]any{
		"Lang":      lang,
		"Dir":       pageDir(lang),
		"T":         t,
		"Action":    shareURLPath(token),
		"Error":     errMsg,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    footer,
	})
}

// renderUnlockedPage tells the user the PIN matched and auto-posts a
// confirmed download to the same URL after a brief delay. Without
// this the browser jumps straight from the PIN form to a streamed
// attachment and the user has no indication of whether their PIN
// was accepted.
func (h *Share) renderUnlockedPage(w http.ResponseWriter, r *http.Request, token, pin string) {
	lang, t, footer := h.pub(r)
	chrome := h.chrome(r) /* wiring:e1 */
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = unlockedTemplate.Execute(w, map[string]any{
		"Lang":      lang,
		"Dir":       pageDir(lang),
		"T":         t,
		"Action":    shareURLPath(token) + "?confirmed=1",
		"PIN":       pin,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    footer,
	})
}

// renderErrorPage shows a styled HTML error page (404 / expired) instead of
// plain text.
//
// It takes a string-table KEY ("notfound", "expired", "folder"),
// not a sentence: the caller is a code path, and the language belongs to the
// visitor. `err_<key>_title` / `err_<key>_body` are looked up per request.
func (h *Share) renderErrorPage(w http.ResponseWriter, r *http.Request, status int, key string) {
	lang, t, footer := h.pub(r)
	chrome := h.chrome(r) /* wiring:e1 */
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = errorPageTemplate.Execute(w, map[string]any{
		"Lang":      lang,
		"Dir":       pageDir(lang),
		"T":         t,
		"Title":     t["err_"+key+"_title"],
		"Body":      t["err_"+key+"_body"],
		"Code":      status,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    footer,
	})
}

var unlockedTemplate = template.Must(template.New("unlocked").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.T.unlocked_title}}</title>
` + publicPageStyle + `
{{.BrandCSS}}
</head><body>
<main class="wrap">
{{.BrandHead}}
<div class="card">
<div class="icon-badge ok">` + publicIconCheck + `</div>
<h1>{{.T.unlocked_heading}}</h1>
<p class="sub" style="margin-bottom:0"><span class="spinner" aria-hidden="true"></span>{{.T.unlocked_sub}}</p>
<form id="f" method="post" action="{{.Action}}" style="display:none">
<input type="hidden" name="pin" value="{{.PIN}}">
</form>
<script>setTimeout(function(){document.getElementById('f').submit();}, 700);</script>
</div>
{{.Footer}}
</main>
</body></html>`))

var errorPageTemplate = template.Must(template.New("err").Parse(`<!doctype html>
<html lang="{{.Lang}}" dir="{{.Dir}}"><head>
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
