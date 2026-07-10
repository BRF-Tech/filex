package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// AI is the token-authenticated REST surface consumed by AI agents and the
// work.example.com FilexClient. It is a thin HTTP adapter over aiOps; the routes
// are mounted under /api/ai behind auth.APITokenMiddleware + RequireScope.
//
// Contract (all JSON unless noted):
//
//	GET    /api/ai/files?path=<adapter://dir>     → {entries:[…]}
//	GET    /api/ai/info?path=<adapter://file>     → {entry:{…}}
//	GET    /api/ai/download?path=<adapter://file> → raw bytes (stream)
//	POST   /api/ai/upload                         → {entry:{…}}  (see body below)
//	POST   /api/ai/delete  {"path":"…"}           → {ok:true}
//	POST   /api/ai/mkdir   {"path":"…"}           → {entry:{…}}
//	POST   /api/ai/move    {"src":"…","dst":"…"}  → {entry:{…}}
//	GET    /api/ai/search?path=<adapter://>&q=…   → {entries:[…]}
//	POST   /api/ai/zip     {"sources":[…],"dest":"…"} → {entry:{…}}  (server-side)
//	POST   /api/ai/unzip   {"src":"…","dest":"…"}     → {ok,extracted}
type AI struct {
	ops *aiOps
}

// NewAI constructs the AI REST handler. shareSvc + publicURL power the share
// endpoints (pass nil shareSvc to disable sharing); convertURL is surfaced via
// /api/ai/root so agents learn conversion is an external (non-API) operation.
func NewAI(store db.Store, resolver func(int64) (storage.Driver, error), shareSvc *share.Service, publicURL, convertURL string) *AI {
	return &AI{ops: newAIOps(store, resolver, shareSvc, publicURL, convertURL)}
}

// AttachACL wires the RBAC resolver into the AI REST surface's ops core so
// every /api/ai file op is gated by the bound user's grants + role ceiling.
func (h *AI) AttachACL(r *acl.Resolver) { h.ops.acl = r }

// AttachThumbs wires the thumbnail pipeline so AI-surface writes dispatch
// generation like manager uploads (nil = thumbnails skipped).
func (h *AI) AttachThumbs(p *thumb.Pipeline) { h.ops.thumbs = p }

// List → GET /api/ai/files?path=<adapter://dir>
func (h *AI) List(w http.ResponseWriter, r *http.Request) {
	entries, err := h.ops.List(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// Info → GET /api/ai/info?path=<adapter://file>
func (h *AI) Info(w http.ResponseWriter, r *http.Request) {
	e, err := h.ops.Info(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

// Download → GET /api/ai/download?path=<adapter://file> (streams bytes).
func (h *AI) Download(w http.ResponseWriter, r *http.Request) {
	rc, mime, size, err := h.ops.Read(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", mime)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, rc)
}

// aiUploadBody is the JSON body for POST /api/ai/upload. Exactly one of
// `content_base64` or `content` (UTF-8 text) must be set. multipart form is
// also accepted (field `file`) for large binaries.
type aiUploadBody struct {
	Path          string `json:"path"`
	Content       string `json:"content,omitempty"`        // UTF-8 text
	ContentBase64 string `json:"content_base64,omitempty"` // binary
}

// Upload → POST /api/ai/upload. Accepts JSON (base64/text) or multipart.
func (h *AI) Upload(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	var (
		dest string
		data []byte
	)

	if hasPrefix(ct, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad multipart: " + err.Error()})
			return
		}
		dest = r.FormValue("path")
		f, _, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file field"})
			return
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, 512<<20))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		data = b
	} else {
		var body aiUploadBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		dest = body.Path
		switch {
		case body.ContentBase64 != "":
			b, err := base64.StdEncoding.DecodeString(body.ContentBase64)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad base64: " + err.Error()})
				return
			}
			data = b
		default:
			data = []byte(body.Content)
		}
	}

	e, err := h.ops.Write(r.Context(), dest, data)
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

// aiPathBody is the shared {"path":"…"} body.
type aiPathBody struct {
	Path string `json:"path"`
}

// Delete → POST /api/ai/delete {"path":"…"}.
func (h *AI) Delete(w http.ResponseWriter, r *http.Request) {
	var body aiPathBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := h.ops.Delete(r.Context(), body.Path); err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Mkdir → POST /api/ai/mkdir {"path":"…"}.
func (h *AI) Mkdir(w http.ResponseWriter, r *http.Request) {
	var body aiPathBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	e, err := h.ops.Mkdir(r.Context(), body.Path)
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

// aiMoveBody is the body for POST /api/ai/move.
type aiMoveBody struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

// Move → POST /api/ai/move {"src":"…","dst":"…"}.
func (h *AI) Move(w http.ResponseWriter, r *http.Request) {
	var body aiMoveBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	e, err := h.ops.Move(r.Context(), body.Src, body.Dst)
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

// Search → GET /api/ai/search?path=<adapter://>&q=…
func (h *AI) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	entries, err := h.ops.Search(r.Context(), q.Get("path"), q.Get("q"))
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// Root → GET /api/ai/root. Reports the caller's confinement root + reachable
// storages so a confined agent knows how to address paths instead of guessing.
func (h *AI) Root(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.ops.RootInfo(r.Context()))
}

// aiShareBody is the body for POST /api/ai/share.
type aiShareBody struct {
	Path          string `json:"path"`
	Pin           bool   `json:"pin,omitempty"`
	ExpiresInDays int    `json:"expires_in_days,omitempty"`
	MaxDownloads  int    `json:"max_downloads,omitempty"`
}

// Share → POST /api/ai/share. Mints a public /s/<token> link for a file/folder
// (folders download as a ZIP). Returns the URL + a one-time PIN if requested.
func (h *AI) Share(w http.ResponseWriter, r *http.Request) {
	var body aiShareBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	res, err := h.ops.CreateShare(r.Context(), body.Path, body.Pin, body.ExpiresInDays, body.MaxDownloads)
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// Unshare → POST /api/ai/unshare {"token":"…"}. Revokes a share by token.
func (h *AI) Unshare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if err := h.ops.RevokeShare(r.Context(), body.Token); err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// aiZipBody is the body for POST /api/ai/zip.
type aiZipBody struct {
	Sources []string `json:"sources"`
	Dest    string   `json:"dest"`
}

// Zip → POST /api/ai/zip {"sources":[…],"dest":"…"}. Packs the sources into a
// .zip ON THE SERVER (folders recurse); the bytes never travel over the wire.
// To download the result, mint a share link for `dest`.
func (h *AI) Zip(w http.ResponseWriter, r *http.Request) {
	var body aiZipBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	e, err := h.ops.Zip(r.Context(), body.Sources, body.Dest)
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

// aiUnzipBody is the body for POST /api/ai/unzip.
type aiUnzipBody struct {
	Src  string `json:"src"`
	Dest string `json:"dest"`
}

// Unzip → POST /api/ai/unzip {"src":"…","dest":"…"}. Extracts a stored zip into
// the dest dir ON THE SERVER (zip-slip protected, confined to the token root).
func (h *AI) Unzip(w http.ResponseWriter, r *http.Request) {
	var body aiUnzipBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	n, err := h.ops.Unzip(r.Context(), body.Src, body.Dest)
	if err != nil {
		writeJSON(w, aiStatus(err), map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "extracted": n})
}

// aiStatus maps an aiOps error to an HTTP status code, reusing the driver
// error mapping for storage-level failures.
func aiStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if errors.Is(err, errAINoStorage) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, storage.ErrReadOnly) {
		return http.StatusForbidden
	}
	if errors.Is(err, storage.ErrUnsupported) {
		return http.StatusNotImplemented
	}
	return mapDriverErr(err)
}

// hasPrefix is a tiny case-tolerant Content-Type prefix check.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && equalFold(s[:len(prefix)], prefix)
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
