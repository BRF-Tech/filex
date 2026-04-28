package handlers

import (
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

type shareCreateReq struct {
	NodeID       int64  `json:"node_id"`
	PIN          string `json:"pin,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`    // seconds from now
	ExpiresAt    string `json:"expires_at,omitempty"`    // RFC3339 — overrides expires_in
	MaxDownloads int    `json:"max_downloads,omitempty"`
}

// shareCreateResp is the JSON envelope returned on POST /api/files/share.
//
// We add the absolute share URL so the UI doesn't have to know the public
// hostname — Phoenix LiveView style.
type shareCreateResp struct {
	ID           int64      `json:"id"`
	Token        string     `json:"token"`
	URL          string     `json:"url"`
	HasPin       bool       `json:"has_pin"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	MaxDownloads *int       `json:"max_downloads,omitempty"`
}

// HandleCreate mints a new share token.
func (h *Share) HandleCreate(w http.ResponseWriter, r *http.Request) {
	var req shareCreateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.NodeID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing node_id"})
		return
	}
	user := auth.UserFrom(r.Context())
	var userID *int64
	if user != nil {
		uid := user.ID
		userID = &uid
	}
	opts := share.CreateOpts{
		NodeID:    req.NodeID,
		PIN:       req.PIN,
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
	writeJSON(w, http.StatusOK, shareCreateResp{
		ID:           sh.ID,
		Token:        sh.Token,
		URL:          h.shareURL(sh.Token),
		HasPin:       sh.PinHash != "",
		ExpiresAt:    sh.ExpiresAt,
		MaxDownloads: sh.MaxDownloads,
	})
}

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
