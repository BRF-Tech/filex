package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/mailer"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/share"
)

// Drop serves the public file-drop (upload link) endpoints — the inverse of
// the Share download link. A visitor with a /d/{token} link can drop one or
// more files INTO the linked folder without an account and, critically,
// without ever seeing, listing or downloading the folder's existing contents
// ("blind drop"). The target folder is resolved server-side from the token;
// the anonymous client can never influence the destination path.
type Drop struct {
	Store     db.Store
	Manager   *Manager
	Service   *share.Service
	Notify    notify.Service
	Mailer    *mailer.Service
	PublicURL string
	limiter   *ipLimiter
	// Branding resolves the public pages' identity (wiring:e1). Nil-safe.
	Branding *BrandingSource
	// DefaultLocale is the fallback language for the drop pages — used when
	// the visitor's browser asks for neither of the two we ship. Same
	// contract as Share.DefaultLocale; see publicLocale.
	DefaultLocale string
}

// AttachBranding wires the shared branding source (wiring:e1).
func (h *Drop) AttachBranding(b *BrandingSource) { h.Branding = b }

// AttachLocale sets the fallback language for the drop pages.
func (h *Drop) AttachLocale(def string) { h.DefaultLocale = def }

// chrome computes the branded page fragments for one request (wiring:e1).
func (h *Drop) chrome(r *http.Request) publicChrome { return publicChromeFor(h.Branding, r) }

// pub resolves the language, string table, chrome and footer for one public
// drop request — the same single-language contract Share.pub keeps.
func (h *Drop) pub(r *http.Request) (string, map[string]string, publicChrome, template.HTML) {
	return publicPageLang(h.Branding, r, h.DefaultLocale)
}

// NewDrop constructs the file-drop handler. mgr provides the shared ingest
// path (IngestFile / EnsureDir); notify + mailer are optional (nil disables
// that channel).
func NewDrop(store db.Store, mgr *Manager, svc *share.Service, nf notify.Service, ml *mailer.Service, publicURL string) *Drop {
	return &Drop{
		Store:     store,
		Manager:   mgr,
		Service:   svc,
		Notify:    nf,
		Mailer:    ml,
		PublicURL: strings.TrimRight(publicURL, "/"),
		limiter:   newIPLimiter(40, time.Hour),
	}
}

// ─────────────────── limits / settings ───────────────────

// dropSettings mirrors the JSON blob persisted on a drop share (the "Gelişmiş"
// options from the create modal). Zero fields fall back to the defaults below.
type dropSettings struct {
	MaxFiles      int      `json:"max_files"`
	MaxFileSizeMB int      `json:"max_file_size_mb"`
	AllowedExt    []string `json:"allowed_ext"`
	AskName       bool     `json:"ask_name"`
}

const (
	dropDefaultMaxFiles      = 20
	dropDefaultMaxFileSizeMB = 500
)

// parseDropSettings decodes the persisted blob, applying defaults for any
// unset field. A nil/empty blob means "no advanced options" → ask for a name
// and use the default caps.
func parseDropSettings(raw *string) dropSettings {
	ds := dropSettings{MaxFiles: dropDefaultMaxFiles, MaxFileSizeMB: dropDefaultMaxFileSizeMB, AskName: true}
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return ds
	}
	var parsed dropSettings
	if err := json.Unmarshal([]byte(*raw), &parsed); err != nil {
		return ds
	}
	if parsed.MaxFiles > 0 {
		ds.MaxFiles = parsed.MaxFiles
	}
	if parsed.MaxFileSizeMB > 0 {
		ds.MaxFileSizeMB = parsed.MaxFileSizeMB
	}
	ds.AllowedExt = normalizeExts(parsed.AllowedExt)
	ds.AskName = parsed.AskName
	return ds
}

// normalizeExts lowercases, strips a leading dot and drops blanks so the
// allowlist compares cleanly against filepath extensions.
func normalizeExts(in []string) []string {
	var out []string
	for _, e := range in {
		e = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(e), ".")))
		if e != "" {
			out = append(out, e)
		}
	}
	return out
}

// dropTokenFromURL extracts the {token} from a /d/{token} link (strips any
// query/fragment + trailing slash). Used by share-mail to look a drop link's
// configured limits back up for the invite body.
func dropTokenFromURL(link string) string {
	s := strings.TrimSpace(link)
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// extAllowed reports whether name's extension is in the (already-normalized)
// allowlist. An empty allowlist allows everything.
func extAllowed(name string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	for _, a := range allow {
		if a == ext {
			return true
		}
	}
	return false
}

// ─────────────────── GET /d/{token} — render page ───────────────────

// Page renders the public upload UI (or a PIN gate). Mirrors the download
// side's /s/{token}: a PIN-protected drop shows the PIN form first; once the
// PIN is accepted (via POST) the uploader page is rendered with the PIN
// embedded so the browser's upload carries it.
func (h *Drop) Page(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	sh, ok := h.resolveKind(w, r, tok)
	if !ok {
		return
	}
	if sh.PinHash != "" {
		// GET always shows the PIN form; the accepted uploader page is only
		// reachable via a successful POST (see Upload).
		h.renderDropPinForm(w, r, tok, "")
		return
	}
	h.renderUploader(w, r, tok, sh, "")
}

// ─────────────────── POST /d/{token} — PIN submit or upload ───────────────────

// Upload handles two POST shapes on the same URL:
//   - multipart/form-data (files present) → the actual drop.
//   - urlencoded PIN form submit → validate the PIN and render the uploader.
func (h *Drop) Upload(w http.ResponseWriter, r *http.Request) {
	tok := chi.URLParam(r, "token")
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		h.handleDrop(w, r, tok)
		return
	}
	// PIN form submit.
	sh, ok := h.resolveKind(w, r, tok)
	if !ok {
		return
	}
	_ = r.ParseForm()
	pin := r.PostForm.Get("pin")
	if _, err := h.Service.Resolve(r.Context(), tok, pin); err != nil {
		h.renderDropPinForm(w, r, tok, "pin_wrong")
		return
	}
	h.renderUploader(w, r, tok, sh, pin)
}

// handleDrop processes the actual multipart upload: enforce limits, write each
// file into a fresh per-submission subfolder, notify the owner. Returns JSON
// so the page's uploader script can show progress/success. It NEVER lists or
// returns existing folder contents.
func (h *Drop) handleDrop(w http.ResponseWriter, r *http.Request, tok string) {
	// Rate-limit anonymous writers per source IP.
	if !h.limiter.allow(clientIP(r)) {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited"})
		return
	}
	// Spilled multipart temp files outlive the response unless dropped here —
	// see the note in AI.Upload. Anonymous drops make this the easiest surface
	// to fill a disk from, so the cleanup runs even for rejected bodies.
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad multipart"})
		return
	}
	pin := r.FormValue("pin")
	sh, err := h.Service.Resolve(r.Context(), tok, pin)
	switch {
	case err == share.ErrBadPIN:
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "bad_pin"})
		return
	case err == share.ErrExpired:
		writeJSON(w, http.StatusGone, map[string]any{"error": "expired"})
		return
	case err != nil || sh == nil:
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		return
	}
	if !sh.IsDrop() {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_a_drop_link"})
		return
	}

	// Bill the bytes to the person whose link this is. The uploader is
	// anonymous by design, but the files land in the link creator's storage,
	// so they are the link creator's bytes. Without this, the public drop
	// would be the one write surface with no owner and therefore no quota —
	// a way to fill somebody's disk for free. Set on the request context so
	// EnsureDir and every IngestFile below inherit it.
	if sh.CreatedBy != nil && *sh.CreatedBy > 0 {
		r = r.WithContext(quotastore.WithOwner(r.Context(), *sh.CreatedBy))
	}

	// Resolve the target folder + storage server-side. The uploader supplies
	// NONE of this — the destination is fixed by the token.
	node, err := h.Store.GetNode(r.Context(), sh.NodeID)
	if err != nil || node == nil || node.Type != model.NodeTypeDirectory {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "folder_missing"})
		return
	}
	st, err := h.Store.GetStorage(r.Context(), node.StorageID)
	if err != nil || st == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "storage_error"})
		return
	}
	if st.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "read_only"})
		return
	}

	ds := parseDropSettings(sh.DropSettings)
	files := r.MultipartForm.File["file[]"]
	if len(files) == 0 {
		files = r.MultipartForm.File["file"]
	}
	if len(files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "no_files"})
		return
	}
	if len(files) > ds.MaxFiles {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "too_many_files", "max_files": ds.MaxFiles})
		return
	}
	maxBytes := int64(ds.MaxFileSizeMB) << 20
	for _, fh := range files {
		if fh.Size > maxBytes {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "file_too_large", "max_file_size_mb": ds.MaxFileSizeMB})
			return
		}
		if !extAllowed(fh.Filename, ds.AllowedExt) {
			writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": "ext_not_allowed", "allowed_ext": ds.AllowedExt})
			return
		}
	}
	// Total-files cap across the link's lifetime.
	if sh.MaxUploads != nil {
		remaining := *sh.MaxUploads - sh.UploadCount
		if remaining <= 0 {
			writeJSON(w, http.StatusGone, map[string]any{"error": "expired"})
			return
		}
		if len(files) > remaining {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "exceeds_remaining", "remaining": remaining})
			return
		}
	}

	// One subfolder per submission: <YYYY-MM-DD_HHMMSS>_<name|anon>, with a
	// random suffix on the rare same-second collision. Keeps submissions from
	// overwriting each other and shows the owner who sent what.
	uploaderName := sanitizeSubName(r.FormValue("uploader_name"))
	stamp := time.Now().Format("2006-01-02_150405")
	who := uploaderName
	if who == "" {
		who = "anon"
	}
	sub := stamp + "_" + who
	subRel := path.Join(node.Path, sub)
	if existing, _ := h.Store.GetNodeByPath(r.Context(), node.StorageID, pathkey.Hash(node.StorageID, normalizeDBPath(subRel))); existing != nil {
		sub = sub + "-" + randHex6()
		subRel = path.Join(node.Path, sub)
	}
	if _, err := h.Manager.EnsureDir(r.Context(), st, subRel); err != nil {
		h.failWrite(w, err, "mkdir", st, subRel, 0)
		return
	}

	saved := 0
	// Keep the LAST write error: with `saved == 0` it is the only account of
	// why nothing landed, and it decides what the visitor is told.
	var lastErr error
	for _, fh := range files {
		src, err := fh.Open()
		if err != nil {
			lastErr = err
			continue
		}
		ingested, err := h.Manager.IngestFile(r.Context(), st, subRel, fh.Filename, src, fh.Size)
		_ = src.Close()
		if err != nil {
			lastErr = err
			continue
		}
		enqueueAntivirusScan(r.Context(), ingested) /* koru:k2 av */
		saved++
	}
	if saved == 0 {
		h.failWrite(w, lastErr, "ingest", st, subRel, len(files))
		return
	}

	// Persist the optional note alongside the files so the owner sees the
	// message next to the drop.
	if note := strings.TrimSpace(r.FormValue("note")); note != "" {
		var b strings.Builder
		if uploaderName != "" {
			b.WriteString("Gönderen: " + uploaderName + "\n\n")
		}
		b.WriteString(note + "\n")
		body := b.String()
		_, _ = h.Manager.IngestFile(r.Context(), st, subRel, "NOT.txt", strings.NewReader(body), int64(len(body)))
	}

	_ = h.Service.IncrementUpload(r.Context(), sh.ID, saved)
	h.notifyOwner(r.Context(), sh, node, saved, uploaderName, sub)

	// Live: a public drop created a new submission subfolder — refresh anyone
	// viewing the drop target folder.
	emitFolderChange(node.StorageID, node.Path, realtime.ChangeEvent{Action: "create", Name: sub})

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": saved, "folder": sub})
}

// failWrite answers a drop whose bytes never landed, and — this is the point —
// says WHY, in three places at once.
//
// It used to answer a bare 500 `{"error":"mkdir_failed"}` with nothing logged.
// When Hetzner's object storage went 503 on 2026-08-25, every drop into an S3
// folder hung for over a minute and then returned that 500: the uploader was
// told "could not send, please try again" (so they retried, into the same dead
// storage), the owner saw nothing, and the server log carried no trace of the
// failure at all — the cause had to be inferred from the *sync worker's*
// unrelated warnings. So:
//
//   - the visitor gets a message that distinguishes "the storage is down, your
//     link is fine, come back shortly" from "you did something wrong",
//   - a 503 (not a 500) says the request was fine and the dependency was not,
//     which is also what stops a retry loop from looking like success,
//   - slog.Error carries it to the error tracker with the storage and path
//     attached, so the outage shows up where outages are looked for.
func (h *Drop) failWrite(w http.ResponseWriter, err error, stage string, st *model.Storage, dest string, files int) {
	code := "storage_unavailable"
	status := http.StatusServiceUnavailable
	if errors.Is(err, quota.ErrQuotaExceeded) {
		// Not an outage: the owner is out of room. Different message, and a
		// 507 so a client can tell the two apart without parsing prose.
		code = "quota_exceeded"
		status = http.StatusInsufficientStorage
	}
	msg := "no error" // saved==0 with every file open+ingest succeeding is impossible, but never log a nil deref
	if err != nil {
		msg = err.Error()
	}
	attrs := []any{
		slog.String("stage", stage),
		slog.String("dest", dest),
		slog.String("code", code),
		slog.String("err", msg),
	}
	if st != nil {
		attrs = append(attrs, slog.String("storage", st.Name), slog.String("driver", st.Driver))
	}
	if files > 0 {
		attrs = append(attrs, slog.Int("files", files))
	}
	slog.Error("drop: upload could not be written", attrs...)
	writeJSON(w, status, map[string]any{"error": code})
}

// notifyOwner fires the in-app notification + owner email after a successful
// drop. Both channels are best-effort.
func (h *Drop) notifyOwner(ctx context.Context, sh *model.Share, node *model.Node, count int, uploaderName, sub string) {
	who := uploaderName
	if who == "" {
		who = "Birisi"
	}
	title := "Yeni dosya yüklemesi"
	body := fmt.Sprintf("%s, \"%s\" klasörüne %d dosya bıraktı (%s).", who, node.Name, count, sub)

	if h.Notify != nil {
		/* bag:b3 event */
		// Canonical webhook-v2 event name (was the ad-hoc "file_dropped")
		// with the structured node/share payload. Delivered off the request
		// path: the anonymous uploader's response must not wait on (or
		// cancel) the owner notification + webhook fan-out.
		ev := notify.Event{
			Event:    notify.EventDropReceived,
			Severity: notify.SeverityInfo,
			Title:    title,
			Body:     body,
			Meta:     map[string]any{"folder": node.Name, "count": count, "submission": sub, "uploader": uploaderName},
			TS:       time.Now(),
			Node:     &notify.NodeRef{StorageID: node.StorageID, Path: node.Path, Name: node.Name},
			Share:    &notify.ShareRef{Token: sh.Token, Path: node.Path},
			UserID:   sh.CreatedBy,
		}
		c := context.WithoutCancel(ctx)
		go func() { _, _ = h.Notify.Send(c, ev) }()
	}
	if h.Mailer != nil && sh.CreatedBy != nil {
		if u, err := h.Store.GetUser(ctx, *sh.CreatedBy); err == nil && u != nil && strings.TrimSpace(u.Email) != "" {
			mailBody := body
			if h.PublicURL != "" {
				mailBody += "\n\n" + h.PublicURL + "/admin/"
			}
			_ = h.Mailer.Send(ctx, u.Email, title, mailBody)
		}
	}
}

// resolveKind loads a token, verifies it's a live drop link, and renders the
// appropriate HTML error page otherwise. Returns ok=false when it has already
// written a response.
func (h *Drop) resolveKind(w http.ResponseWriter, r *http.Request, tok string) (*model.Share, bool) {
	sh, err := h.Store.GetShareByToken(r.Context(), tok)
	if err != nil {
		h.renderDropError(w, r, http.StatusNotFound, "drop_notfound")
		return nil, false
	}
	if !sh.IsDrop() {
		h.renderDropError(w, r, http.StatusNotFound, "drop_notdrop")
		return nil, false
	}
	if sh.IsExpired(time.Now()) {
		h.renderDropError(w, r, http.StatusGone, "drop_expired")
		return nil, false
	}
	return sh, true
}

// ─────────────────── rendering ───────────────────

func (h *Drop) renderUploader(w http.ResponseWriter, r *http.Request, tok string, sh *model.Share, pin string) {
	lang, t, chrome, footer := h.pub(r)
	ds := parseDropSettings(sh.DropSettings)
	folderName := ""
	if node, err := h.Store.GetNode(r.Context(), sh.NodeID); err == nil && node != nil {
		folderName = node.Name
	}
	cfg := map[string]any{
		"token":         tok,
		"action":        "/d/" + tok,
		"askName":       ds.AskName,
		"maxFiles":      ds.MaxFiles,
		"maxFileSizeMB": ds.MaxFileSizeMB,
		"allowedExt":    ds.AllowedExt,
		"pin":           pin,
		"folder":        folderName,
		"requiresPin":   sh.PinHash != "",
		// The script's own strings travel WITH the page, in the page's
		// language. Hard-coding them in the script is how this page stayed
		// Turkish for every visitor while its PIN gate rendered blank.
		"t": dropScriptStrings(t),
	}
	cfgJSON, _ := json.Marshal(cfg)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := dropUploaderTemplate.Execute(w, map[string]any{
		"Lang":      lang,
		"T":         t,
		"Folder":    folderName,
		"Config":    template.JS(cfgJSON),
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    footer,
	}); err != nil {
		slog.Error("drop: uploader render failed", slog.String("err", err.Error()))
	}
}

// dropScriptStrings is the subset of the string table the uploader script
// needs at runtime (client-side validation, progress, the result screen).
func dropScriptStrings(t map[string]string) map[string]string {
	keys := []string{
		"drop_send", "drop_sending", "drop_remove_aria",
		"drop_limit_files", "drop_limit_size", "drop_limit_ext",
		"drop_done_heading", "drop_done_sub",
		"drop_err_too_many", "drop_err_too_large", "drop_err_ext",
		"drop_err_ext_any", "drop_err_large_any",
		"drop_err_bad_pin", "drop_err_expired", "drop_err_rate",
		"drop_err_no_files", "drop_err_storage", "drop_err_quota",
		"drop_err_generic",
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = t[k]
	}
	return out
}

// renderDropPinForm renders the gate on a PIN-protected drop link. errKey is a
// string-table key ("pin_wrong") or empty — never a sentence, so the gate and
// the uploader behind it cannot end up in different languages.
func (h *Drop) renderDropPinForm(w http.ResponseWriter, r *http.Request, token, errKey string) {
	lang, t, chrome, footer := h.pub(r)
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
	// Reuse the shared PIN form template; Action posts back to /d/{token}.
	// Lang + T are NOT optional here: the template asks for {{.T.pin_heading}}
	// and friends, and html/template renders a missing key as "" — which is
	// how this page shipped with an empty title, heading and button.
	if err := pinFormTemplate.Execute(w, map[string]any{
		"Lang":      lang,
		"T":         t,
		"Action":    "/d/" + path.Clean(token),
		"Error":     errMsg,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    footer,
	}); err != nil {
		slog.Error("drop: pin form render failed", slog.String("err", err.Error()))
	}
}

// renderDropError shows a styled error page. It takes string-table KEYS, not
// sentences: the caller is a code path and the language belongs to the visitor.
func (h *Drop) renderDropError(w http.ResponseWriter, r *http.Request, status int, key string) {
	lang, t, chrome, footer := h.pub(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	// Reuse the shared error page template.
	if err := errorPageTemplate.Execute(w, map[string]any{
		"Lang":      lang,
		"T":         t,
		"Title":     t["err_"+key+"_title"],
		"Body":      t["err_"+key+"_body"],
		"Code":      status,
		"BrandCSS":  chrome.BrandCSS,
		"BrandHead": chrome.BrandHead,
		"Footer":    footer,
	}); err != nil {
		slog.Error("drop: error page render failed", slog.String("err", err.Error()))
	}
}

// sanitizeSubName reduces a free-text uploader name to a safe, short folder
// segment: letters/digits/dash/underscore/space only, collapsed, capped.
func sanitizeSubName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == ' ':
			b.WriteRune(r)
		default:
			// Keep common Turkish letters readable; drop everything else.
			if strings.ContainsRune("çÇğĞıİöÖşŞüÜ", r) {
				b.WriteRune(r)
			}
		}
	}
	out := strings.TrimSpace(b.String())
	out = strings.Trim(out, "-_ ")
	if len(out) > 40 {
		out = strings.TrimSpace(out[:40])
	}
	return out
}

// ─────────────────── per-IP rate limiter ───────────────────

type ipWindow struct {
	count int
	reset time.Time
}

type ipLimiter struct {
	mu     sync.Mutex
	hits   map[string]*ipWindow
	limit  int
	window time.Duration
}

func newIPLimiter(limit int, window time.Duration) *ipLimiter {
	return &ipLimiter{hits: make(map[string]*ipWindow), limit: limit, window: window}
}

// allow reports whether ip may perform another action within the window.
func (l *ipLimiter) allow(ip string) bool {
	if ip == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	w := l.hits[ip]
	if w == nil || now.After(w.reset) {
		l.hits[ip] = &ipWindow{count: 1, reset: now.Add(l.window)}
		// Opportunistic prune so the map can't grow unbounded.
		if len(l.hits) > 4096 {
			for k, v := range l.hits {
				if now.After(v.reset) {
					delete(l.hits, k)
				}
			}
		}
		return true
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	return true
}

// clientIP extracts the best-effort source IP, honoring X-Forwarded-For's
// first hop (filex sits behind Caddy) then falling back to RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// dropUploaderTemplate is a dependency-free upload page: drag-and-drop or pick,
// one or many files, optional name/note, live progress. All limits are echoed
// so the visitor sees them; the actual enforcement is server-side.
var dropUploaderTemplate = template.Must(template.New("drop").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.T.drop_title}}{{if .Folder}} — {{.Folder}}{{end}}</title>
` + publicPageStyle + `
{{.BrandCSS}}
<style>
.card { width: 520px; text-align: left; }
.drop { border: 2px dashed var(--px-line); border-radius: 12px; padding: 28px 16px; text-align: center; cursor: pointer; color: var(--px-muted); transition: border-color 0.15s ease, background 0.15s ease, color 0.15s ease; }
.drop:hover, .drop.over { border-color: var(--px-accent); background: var(--px-accent-soft); color: var(--px-accent); }
.drop:focus-visible { outline: 2px solid var(--px-accent); outline-offset: 2px; }
.drop svg { width: 38px; height: 38px; margin-bottom: 6px; }
.drop .big { font-size: 1rem; font-weight: 600; color: var(--px-fg); }
.drop .hint { font-size: 0.82rem; margin-top: 6px; }
input[type=file] { display: none; }
.field { margin-top: 14px; }
label { display: block; font-size: 0.82rem; color: var(--px-muted); margin-bottom: 5px; }
input[type=text], textarea { width: 100%; padding: 11px; border: 1px solid var(--px-line); border-radius: 9px; font-size: 0.95rem; background: transparent; color: inherit; font-family: inherit; }
input[type=text]:focus, textarea:focus { outline: none; border-color: var(--px-accent); box-shadow: 0 0 0 3px var(--px-accent-soft); }
textarea { resize: vertical; min-height: 64px; }
.files { margin-top: 14px; display: flex; flex-direction: column; gap: 8px; }
.f { display: flex; align-items: center; gap: 10px; font-size: 0.86rem; padding: 8px 10px; border: 1px solid var(--px-line); border-radius: 9px; }
.f .nm { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.f .sz { color: var(--px-muted); font-variant-numeric: tabular-nums; }
.f .x { cursor: pointer; color: var(--px-muted); border: 0; background: none; font-size: 1rem; border-radius: 6px; padding: 2px 7px; }
.f .x:hover { color: var(--px-err); background: var(--px-err-soft); }
.f .x:focus-visible { outline: 2px solid var(--px-accent); outline-offset: 1px; }
.bar { height: 6px; border-radius: 999px; background: var(--px-line); overflow: hidden; margin-top: 16px; display: none; }
.bar > i { display: block; height: 100%; width: 0; background: linear-gradient(90deg, var(--px-accent), var(--px-accent-hover)); transition: width 0.2s ease; }
.msg { margin-top: 14px; font-size: 0.88rem; }
.msg.err { color: var(--px-err); }
.done { text-align: center; }
.foot { margin-top: 16px; text-align: center; color: var(--px-muted); font-size: 0.72rem; }
@media (prefers-reduced-motion: reduce) { .drop, .bar > i { transition: none; } }
</style>
</head><body>
<main class="wrap">
{{.BrandHead}}
<div class="card" id="card">
  <h1>{{.T.drop_heading}}{{if .Folder}} · {{.Folder}}{{end}}</h1>
  <p class="sub" id="sub">{{.T.drop_sub}}</p>

  <div class="drop" id="drop" role="button" tabindex="0" aria-label="{{.T.drop_zone_aria}}">
    <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 14.9A7 7 0 1 1 15.7 8h1.8a4.5 4.5 0 0 1 2.5 8.2"/><path d="M12 12v9"/><path d="m16 16-4-4-4 4"/></svg>
    <div class="big">{{.T.drop_zone_big}}</div>
    <div class="hint" id="hint">{{.T.drop_zone_hint}}</div>
  </div>
  <input type="file" id="file" multiple>

  <div class="field" id="nameField" style="display:none">
    <label for="uploaderName">{{.T.drop_name_label}}</label>
    <input type="text" id="uploaderName" maxlength="60" placeholder="{{.T.drop_name_ph}}">
  </div>
  <div class="field">
    <label for="note">{{.T.drop_note_label}}</label>
    <textarea id="note" maxlength="2000" placeholder="{{.T.drop_note_ph}}"></textarea>
  </div>

  <div class="files" id="files"></div>
  <div class="bar" id="bar"><i id="barfill"></i></div>
  <div class="msg" id="msg" role="status"></div>
  <button class="btn" id="send" disabled>{{.T.drop_send}}</button>
  <div class="foot" id="foot"></div>
</div>
{{.Footer}}
</main>

<script>
var CFG = {{.Config}};
var picked = [];
var el = function(id){ return document.getElementById(id); };
var drop = el('drop'), fileInput = el('file'), filesBox = el('files'), sendBtn = el('send'), msg = el('msg');

if (CFG.askName) el('nameField').style.display = 'block';
// sub() fills the %s placeholders in a table string, left to right, so the
// script's messages come from the same table as the page around it.
function sub(tpl){ var a=[].slice.call(arguments,1), i=0; return String(tpl||'').replace(/%s/g, function(){ return i<a.length ? a[i++] : ''; }); }
var T = CFG.t || {};
var limitBits = [sub(T.drop_limit_files, CFG.maxFiles), sub(T.drop_limit_size, CFG.maxFileSizeMB)];
if (CFG.allowedExt && CFG.allowedExt.length) limitBits.push(sub(T.drop_limit_ext, CFG.allowedExt.join(', ')));
el('foot').textContent = limitBits.join(' · ');
// Restrict the native file picker to the allowed extensions when set.
if (CFG.allowedExt && CFG.allowedExt.length) { fileInput.setAttribute('accept', CFG.allowedExt.map(function(e){ return '.' + e; }).join(',')); }

function human(b){ if(b<1024) return b+' B'; var u=['KB','MB','GB','TB'],i=-1; do{ b/=1024; i++; }while(b>=1024&&i<u.length-1); return b.toFixed(1)+' '+u[i]; }

function extOk(name){ if(!CFG.allowedExt||!CFG.allowedExt.length) return true; var m=name.toLowerCase().match(/\.([^.]+)$/); var e=m?m[1]:''; return CFG.allowedExt.indexOf(e)>=0; }

function render(){
  filesBox.innerHTML='';
  picked.forEach(function(f,idx){
    var row=document.createElement('div'); row.className='f';
    var nm=document.createElement('span'); nm.className='nm'; nm.textContent=f.name;
    var sz=document.createElement('span'); sz.className='sz'; sz.textContent=human(f.size);
    var x=document.createElement('button'); x.className='x'; x.type='button'; x.textContent='✕';
    x.setAttribute('aria-label', sub(T.drop_remove_aria, f.name));
    x.onclick=function(){ picked.splice(idx,1); render(); };
    row.appendChild(nm); row.appendChild(sz); row.appendChild(x); filesBox.appendChild(row);
  });
  sendBtn.disabled = picked.length===0;
}

function add(list){
  msg.className='msg'; msg.textContent='';
  for(var i=0;i<list.length;i++){
    var f=list[i];
    if(picked.length>=CFG.maxFiles){ msg.className='msg err'; msg.textContent=sub(T.drop_err_too_many, CFG.maxFiles); break; }
    if(f.size > CFG.maxFileSizeMB*1024*1024){ msg.className='msg err'; msg.textContent=sub(T.drop_err_too_large, f.name, CFG.maxFileSizeMB); continue; }
    if(!extOk(f.name)){ msg.className='msg err'; msg.textContent=sub(T.drop_err_ext, f.name); continue; }
    picked.push(f);
  }
  render();
}

drop.onclick=function(){ fileInput.click(); };
drop.onkeydown=function(e){ if(e.key==='Enter'||e.key===' '){ e.preventDefault(); fileInput.click(); } };
fileInput.onchange=function(){ add(fileInput.files); fileInput.value=''; };
['dragenter','dragover'].forEach(function(ev){ drop.addEventListener(ev,function(e){ e.preventDefault(); drop.classList.add('over'); }); });
['dragleave','drop'].forEach(function(ev){ drop.addEventListener(ev,function(e){ e.preventDefault(); drop.classList.remove('over'); }); });
drop.addEventListener('drop',function(e){ if(e.dataTransfer&&e.dataTransfer.files) add(e.dataTransfer.files); });

sendBtn.onclick=function(){
  if(!picked.length) return;
  var fd=new FormData();
  picked.forEach(function(f){ fd.append('file[]', f, f.name); });
  if(CFG.pin) fd.append('pin', CFG.pin);
  var nm=el('uploaderName'); if(nm) fd.append('uploader_name', nm.value||'');
  fd.append('note', el('note').value||'');
  sendBtn.disabled=true; sendBtn.textContent=T.drop_sending;
  el('bar').style.display='block'; msg.className='msg'; msg.textContent='';
  var xhr=new XMLHttpRequest();
  xhr.open('POST', CFG.action, true);
  xhr.upload.onprogress=function(e){ if(e.lengthComputable){ el('barfill').style.width=(e.loaded/e.total*100).toFixed(0)+'%'; } };
  xhr.onload=function(){
    var ok = xhr.status>=200 && xhr.status<300;
    var res={}; try{ res=JSON.parse(xhr.responseText); }catch(_){}
    if(ok && res.ok){ success(res.count); }
    else { fail(res.error, res); }
  };
  xhr.onerror=function(){ fail('network'); };
  xhr.send(fd);
};

function success(n){
  var done=document.createElement('div'); done.className='done';
  var badge=document.createElement('div'); badge.className='icon-badge ok';
  badge.innerHTML='<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4.5 12.5l5 5 10-11"/></svg>';
  var h=document.createElement('h1'); h.textContent=T.drop_done_heading;
  var p=document.createElement('p'); p.className='sub'; p.style.marginBottom='0'; p.textContent=sub(T.drop_done_sub, n);
  done.appendChild(badge); done.appendChild(h); done.appendChild(p);
  var card=el('card'); card.innerHTML=''; card.appendChild(done);
}
function fail(code, res){
  sendBtn.disabled=false; sendBtn.textContent=T.drop_send; el('bar').style.display='none'; el('barfill').style.width='0';
  // storage_unavailable is deliberately NOT the generic "try again" line: the
  // link and the files are fine, the backing storage is down, and telling the
  // visitor that is the difference between waiting and retrying into a wall.
  var m={ too_many_files:sub(T.drop_err_too_many, CFG.maxFiles), file_too_large:sub(T.drop_err_large_any, CFG.maxFileSizeMB), ext_not_allowed:T.drop_err_ext_any, bad_pin:T.drop_err_bad_pin, expired:T.drop_err_expired, rate_limited:T.drop_err_rate, no_files:T.drop_err_no_files, storage_unavailable:T.drop_err_storage, quota_exceeded:T.drop_err_quota };
  msg.className='msg err'; msg.textContent = (m[code]||T.drop_err_generic);
}
render();
</script>
</body></html>`))
