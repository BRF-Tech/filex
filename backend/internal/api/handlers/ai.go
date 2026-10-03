package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/httpx"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/share"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/tenanturl"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writegate"
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
//	POST   /api/ai/upload/ticket {"path":"…"}     → {url,curl,…} (credential-free
//	                                                 upload URL; upload_ticket.go)
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
// endpoints (pass nil shareSvc to disable sharing).
func NewAI(store db.Store, resolver func(int64) (storage.Driver, error), shareSvc *share.Service, publicURL string) *AI {
	return &AI{ops: newAIOps(store, resolver, shareSvc, publicURL)}
}

// AttachSearchIndex wires the search index into the ops core, so a file an
// agent writes is searchable at once rather than when the next storage sync
// walks the folder.
func (h *AI) AttachSearchIndex(idx *search.Index) { h.ops.attachSearchIndex(idx) }

// AttachACL wires the RBAC resolver into the AI REST surface's ops core so
// every /api/ai file op is gated by the bound user's grants + role ceiling.
func (h *AI) AttachACL(r *acl.Resolver) { h.ops.acl = r }

// AttachTenants wires the shared origin resolver (internal/tenanturl).
func (h *AI) AttachTenants(rv tenanturl.Resolver) { h.ops.tenants = rv }

// AttachThumbs wires the thumbnail pipeline so AI-surface writes dispatch
// generation like manager uploads (nil = thumbnails skipped).
func (h *AI) AttachThumbs(p *thumb.Pipeline) { h.ops.thumbs = p }

// AttachStaged routes writes above the chunk threshold through filex's staging
// area, exactly as the browser and the CLI do (nil = synchronous writes).
func (h *AI) AttachStaged(s *StagedUpload) { h.ops.staged = s }

// AttachBody wires the byte-source resolver so /api/ai reads serve a file that
// is still being transferred out of staging.
func (h *AI) AttachBody(b *filebody.Resolver) { h.ops.attachBody(b) }

// List → GET /api/ai/files?path=<adapter://dir>
func (h *AI) List(w http.ResponseWriter, r *http.Request) {
	entries, err := h.ops.List(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// Info → GET /api/ai/info?path=<adapter://file>
func (h *AI) Info(w http.ResponseWriter, r *http.Request) {
	e, err := h.ops.Info(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

// Download → GET /api/ai/download?path=<adapter://file> (streams bytes).
func (h *AI) Download(w http.ResponseWriter, r *http.Request) {
	rc, mime, size, err := h.ops.Read(r.Context(), r.URL.Query().Get("path"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	defer rc.Close()
	httpx.ProtectServedFile(w.Header(), httpx.ServedType(mime, r.URL.Query().Get("path")))
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	_, _ = io.Copy(w, rc)
}

// aiUploadBody is the JSON body for POST /api/ai/upload. Exactly one of
// `content_base64` or `content` (UTF-8 text) must be set. multipart form is
// also accepted (field `file`) for large binaries.
type aiUploadBody struct {
	Path          string `json:"path"`
	Content       string `json:"content,omitempty"`        // UTF-8 text
	ContentBase64 string `json:"content_base64,omitempty"` // binary
	// AllowPlaintext: the caller knows `path` is in an end-to-end encrypted
	// folder and stores the bytes there UNENCRYPTED on purpose. Without it
	// such a write answers 409 E2E_PLAINTEXT_REFUSED (ai_e2e.go).
	AllowPlaintext bool `json:"allow_plaintext,omitempty"`
}

// formFlag reads a boolean multipart/form field: "true", "1" or "yes".
func formFlag(r *http.Request, name string) bool {
	switch r.FormValue(name) {
	case "true", "1", "yes":
		return true
	}
	return false
}

// Upload → POST /api/ai/upload. Accepts JSON (base64/text) or multipart.
//
// A multipart body is STREAMED: the part is handed to WriteStream as a reader,
// so a large capture never has to exist in memory, and above the chunk
// threshold it goes through filex's staging area like every other client.
func (h *AI) Upload(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")

	if hasPrefix(ct, "multipart/form-data") {
		// Parts above the in-memory limit are spilled to $TMPDIR as
		// multipart-*. net/http clears those only for the request struct it
		// holds itself, which is not the one the router hands us, so without
		// this the files survive a 200 response and the disk fills silently
		// (fm.example.com: 74 files / 29 GB in two hours, 2026-08-09). Deferred
		// before the parse so a rejected body is cleaned up too.
		defer func() {
			if r.MultipartForm != nil {
				_ = r.MultipartForm.RemoveAll()
			}
		}()
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad multipart: " + err.Error()})
			return
		}
		dest := r.FormValue("path")
		f, fh, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file field"})
			return
		}
		defer f.Close()
		if fh.Size > aiMaxUploadBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error": fmt.Sprintf("file too large for this endpoint (> %d bytes); use /api/files/upload/begin", aiMaxUploadBytes),
			})
			return
		}
		// multipart.File is an io.Seeker; passed straight through it stays one,
		// which is what keeps the S3 SDK able to measure and replay the body.
		ctx := withPlaintextConsent(r.Context(), formFlag(r, "allow_plaintext"))
		e, err := h.ops.WriteStream(ctx, dest, f, fh.Size)
		if err != nil {
			writeAIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entry": e})
		return
	}

	var body aiUploadBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	var data []byte
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

	e, err := h.ops.Write(withPlaintextConsent(r.Context(), body.AllowPlaintext), body.Path, data)
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

// aiMaxUploadBytes caps a single /api/ai/upload multipart body. The bytes no
// longer sit in memory, but an unbounded ceiling here would let one request
// fill the staging filesystem; a client with more than this uses the chunked
// protocol, which has a disk guard and a resume point.
const aiMaxUploadBytes = 512 << 20

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
		writeAIError(w, err)
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
		writeAIError(w, err)
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
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

// Search → GET /api/ai/search?path=<adapter://>&q=…
//
// Goes through aiNameSearch rather than aiOps.Search so this speaks the
// same query language as the MCP file_search tool and the web endpoints:
// separator-blind text and `tag:` filters. The response shape is
// unchanged — entries, no snippets; content search stays on the MCP tool.
func (h *AI) Search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := q.Get("path")
	parsed := search.ParseQuery(q.Get("q"))
	s, _, err := h.ops.resolveStorage(r.Context(), p)
	if err != nil {
		writeAIError(w, err)
		return
	}
	tags, err := aiTagFilter(r.Context(), h.ops, s.Name, parsed)
	if err != nil {
		writeAIError(w, err)
		return
	}
	entries, err := aiNameSearch(r.Context(), h.ops, p, parsed, tags)
	if err != nil {
		writeAIError(w, err)
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
		writeAIError(w, err)
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
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// aiZipBody is the body for POST /api/ai/zip.
type aiZipBody struct {
	Sources []string `json:"sources"`
	Dest    string   `json:"dest"`
	// AllowPlaintext: write the archive into an encrypted folder anyway
	// (aiUploadBody.AllowPlaintext).
	AllowPlaintext bool `json:"allow_plaintext,omitempty"`
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
	e, err := h.ops.Zip(withPlaintextConsent(r.Context(), body.AllowPlaintext), body.Sources, body.Dest)
	if err != nil {
		writeAIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

// aiUnzipBody is the body for POST /api/ai/unzip.
type aiUnzipBody struct {
	Src  string `json:"src"`
	Dest string `json:"dest"`
	// AllowPlaintext: extract into an encrypted folder anyway
	// (aiUploadBody.AllowPlaintext).
	AllowPlaintext bool `json:"allow_plaintext,omitempty"`
}

// Unzip → POST /api/ai/unzip {"src":"…","dest":"…"}. Extracts a stored zip into
// the dest dir ON THE SERVER (zip-slip protected, confined to the token root).
func (h *AI) Unzip(w http.ResponseWriter, r *http.Request) {
	var body aiUnzipBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	n, refused, err := h.ops.Unzip(withPlaintextConsent(r.Context(), body.AllowPlaintext), body.Src, body.Dest)
	if err != nil {
		resp := aiErrorBody(err)
		if errors.Is(err, errAISnapshotRefused) {
			// The all-refused case: say so the way every other pre-write
			// guard refusal does, with the count too -- an error string alone
			// leaves "how many, of how many" unanswered.
			resp["code"] = "SNAPSHOT_FAILED"
			resp["refused"] = refused
		}
		writeJSON(w, aiStatus(err), resp)
		return
	}
	// refused: members the pre-write guard turned away, distinct from a
	// permanent skip (zip-slip, kind conflict). Surfaced on an otherwise-200
	// partial batch too, not only on the all-refused case above.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "extracted": n, "refused": refused})
}

// writeAIError is the one shape of a refused AI REST call: aiStatus for the
// status, the message in `error`, and the wire code an agent matches on in
// `code` when the refusal has one (aiErrCode: E2E_ENCRYPTED,
// E2E_PLAINTEXT_REFUSED, E2E_BOUNDARY, RESERVED_NAME, NO_FREE_NAME,
// ENTRY_UNAVAILABLE). An unavailable entry (entry_unavailable.go, #104) also
// names the entry and the storage's answer, as the explorer's 409 does.
func writeAIError(w http.ResponseWriter, err error) {
	var ue *entryUnavailableError
	if errors.As(err, &ue) {
		writeJSON(w, http.StatusConflict, ue.body())
		return
	}
	writeJSON(w, aiStatus(err), aiErrorBody(err))
}

func aiErrorBody(err error) map[string]any {
	body := map[string]any{"error": err.Error()}
	if code := aiErrCode(err); code != "" {
		body["code"] = code
	}
	return body
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
	// A call that cannot be acted on as asked (a field missing): fix the call,
	// not retry it - mapDriverErr's default 500 reads as "retry".
	if errors.Is(err, errAIBadInput) {
		return http.StatusBadRequest
	}
	// End-to-end encryption (ai_e2e.go): a conflict with where the bytes are,
	// which retrying never changes. The boundary refusal used to fall through
	// to mapDriverErr's 500, so an agent was told "server fault, retry" for a
	// move the explorer answers 409.
	switch aiErrCode(err) {
	case codeE2EEncrypted, codeE2EPlaintextRefused, codeE2EBoundary:
		return http.StatusConflict
	}
	// An entry the storage could not answer for (issue #104): a state the
	// caller can see and wait out, not a fault (writeAIError adds the code).
	if errors.Is(err, errEntryUnavailable) {
		return http.StatusConflict
	}
	// writegate: an app has frozen the path (423, the message names the app),
	// or it is one of filex's own names (403).
	if errors.Is(err, writegate.ErrLocked) {
		return http.StatusLocked
	}
	if errors.Is(err, syspath.ErrReserved) {
		return http.StatusForbidden
	}
	// A transient, system-caused refusal: the snapshot guard could not
	// preserve a file this write would have replaced. 503, not mapDriverErr's
	// default 500 and not the 404/409 its substring match on the error text
	// would otherwise produce.
	if errors.Is(err, errAISnapshotRefused) {
		return http.StatusServiceUnavailable
	}
	// Same shape: the backend could not say whether a move's destination is
	// free, so the move was refused. Transient, not a conflict.
	if errors.Is(err, errNameCheckFailed) {
		return http.StatusServiceUnavailable
	}
	// A move whose destination is taken lands beside it (ops.MoveDest,
	// ops.UniqueDest), and only when every name beside it is taken too is the
	// move refused, with nothing moved. A conflict the caller can resolve, as
	// the explorer's move answers it (manager_mutate.go). It fell through to
	// mapDriverErr, whose text match finds neither "exists" nor "not found" in
	// "no free name left", and answered 500, which reads as "retry" (task #116).
	if errors.Is(err, ops.ErrNoFreeName) {
		return http.StatusConflict
	}
	// Permanent refusals, not server faults: a confined token reaching outside
	// its root, or the bound user lacking the grant level. These must NOT fall
	// through to mapDriverErr's 500 — a 5xx reads as "retry" to any client.
	if errors.Is(err, confine.ErrOutOfRoot) || errors.Is(err, errAIForbidden) {
		return http.StatusForbidden
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
