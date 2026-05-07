package handlers

import (
	"context"
	cryptoRand "crypto/rand"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gitlab.com/brftech/filemanager/backend/internal/auth"
	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/model"
	"gitlab.com/brftech/filemanager/backend/internal/share"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// Share handles share creation and the public viewer endpoints.
type Share struct {
	Service         *share.Service
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
	PublicURL       string
}

// NewShare constructs a Share handler.
func NewShare(svc *share.Service, store db.Store, resolver func(int64) (storage.Driver, error), publicURL string) *Share {
	return &Share{
		Service:         svc,
		Store:           store,
		StorageResolver: resolver,
		PublicURL:       strings.TrimRight(publicURL, "/"),
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
}

// shareCreateRespInner is the payload nested under `share` in the
// response — the SFC accesses it as `body.share.*`.
type shareCreateRespInner struct {
	ID           int64      `json:"id"`
	UUID         string     `json:"uuid"`     // alias for token (frontend uses uuid in delete URL)
	Token        string     `json:"token"`
	URL          string     `json:"url"`
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
		NodeID:    nodeID,
		PIN:       pin,
		CreatedBy: userID,
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
	sh, err := h.Service.Create(r.Context(), opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	inner := shareCreateRespInner{
		ID:           sh.ID,
		UUID:         sh.Token,
		Token:        sh.Token,
		URL:          h.shareURL(sh.Token),
		HasPin:       sh.PinHash != "",
		PasswordPin:  pinGenerated,
		ExpiresAt:    sh.ExpiresAt,
		MaxDownloads: sh.MaxDownloads,
	}
	if node, _ := h.Store.GetNode(r.Context(), nodeID); node != nil {
		inner.Filename = node.Name
		inner.Path = node.Path
	}

	// Dual envelope: nested `share` for the SFC + flat fields at the
	// top level for legacy embed.js. Cheap to ship both.
	writeJSON(w, http.StatusOK, map[string]any{
		"share":         inner,
		"id":            inner.ID,
		"token":         inner.Token,
		"url":           inner.URL,
		"has_pin":       inner.HasPin,
		"expires_at":    inner.ExpiresAt,
		"max_downloads": inner.MaxDownloads,
	})
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
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if sh.IsExpired(time.Now()) {
		http.Error(w, "expired", http.StatusGone)
		return
	}

	// PIN required path: render the form on GET when no PIN supplied.
	if sh.PinHash != "" && pin == "" {
		h.renderPINForm(w, tok, "")
		return
	}

	// Resolve runs the PIN bcrypt check + recomputes expiry.
	resolved, err := h.Service.Resolve(r.Context(), tok, pin)
	switch {
	case errors.Is(err, share.ErrExpired):
		http.Error(w, "expired", http.StatusGone)
		return
	case errors.Is(err, share.ErrBadPIN):
		// Re-render with a friendly error rather than a flat 401.
		h.renderPINForm(w, tok, "Wrong PIN — try again.")
		return
	case err != nil:
		http.Error(w, "not found", http.StatusNotFound)
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

	// Use a presigned URL when the driver supports it — saves us from
	// proxying the bytes.
	if pres, ok := drv.(storage.Presigner); ok {
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

// shareURLPath returns the URL path for a share token.
func shareURLPath(token string) string {
	return "/s/" + path.Clean(token)
}

// pinFormTemplate is a dependency-free HTML page rendered when a share
// requires a PIN and none was provided.
var pinFormTemplate = template.Must(template.New("pin").Parse(`<!doctype html>
<html lang="en"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Enter PIN</title>
<style>
:root { color-scheme: light dark; }
body { font-family: system-ui, -apple-system, Segoe UI, sans-serif; margin: 0; min-height: 100vh; display: grid; place-items: center; background: linear-gradient(135deg, #f6f8fb 0%, #e9eef5 100%); }
@media (prefers-color-scheme: dark) { body { background: linear-gradient(135deg, #14171c 0%, #1c2128 100%); color: #e6eaf0; } }
.card { width: 360px; max-width: 90%; padding: 32px; border-radius: 12px; background: rgba(255,255,255,0.85); box-shadow: 0 10px 40px rgba(0,0,0,0.08); backdrop-filter: blur(8px); }
@media (prefers-color-scheme: dark) { .card { background: rgba(36,40,48,0.85); box-shadow: 0 10px 40px rgba(0,0,0,0.4); } }
h1 { font-size: 1.25rem; margin: 0 0 8px; }
p { margin: 0 0 24px; opacity: 0.7; font-size: 0.9rem; }
input { width: 100%; padding: 12px; border: 1px solid #d0d7de; border-radius: 8px; font-size: 1rem; box-sizing: border-box; background: transparent; color: inherit; }
button { width: 100%; padding: 12px; margin-top: 16px; border: 0; border-radius: 8px; font-size: 1rem; font-weight: 600; cursor: pointer; background: #2563eb; color: #fff; }
button:hover { background: #1d4ed8; }
.error { color: #dc2626; font-size: 0.85rem; margin-top: 12px; }
</style>
</head><body>
<form class="card" method="post" action="{{.Action}}">
<h1>This share is PIN-protected</h1>
<p>Enter the PIN to access the file.</p>
<input type="password" name="pin" autocomplete="one-time-code" autofocus required>
{{if .Error}}<div class="error">{{.Error}}</div>{{end}}
<button type="submit">Unlock</button>
</form>
</body></html>`))

func (h *Share) renderPINForm(w http.ResponseWriter, token, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if errMsg != "" {
		w.WriteHeader(http.StatusUnauthorized)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_ = pinFormTemplate.Execute(w, map[string]any{
		"Action": shareURLPath(token),
		"Error":  errMsg,
	})
}
