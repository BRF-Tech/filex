// Package handlers — app_ui.go
//
// The server half of an app's OWN interface (docs/APP-PLUGINS-API.md → An
// app's own interface). The interface runs in a sandboxed frame with no
// network; what it asks for goes through filex's frame (AppFrame, the
// explorer), which calls these with the PERSON's session:
//
//	PUT  /api/files/plugins/ui/{plugin}/{view}/save?path=<qualified>        — a new version of an opened file
//	PUT  /api/files/plugins/ui/{plugin}/{view}/save?dir=<qualified>&name=   — a NEW file there ("save as")
//	POST /api/files/plugins/ui/{plugin}/{view}/call                        — the app's module (`ui_call`)
//
// ⚠⚠ Every check is made again HERE, whatever the frame already decided: the
// app is running, the view is its interface, the app was granted what the
// call needs (files:write for a save), the file is one the view applies to,
// and the person may write it (confinement, ACL editor, the write gate —
// locks, filex's own folders, a draft only its owner's). The frame is the
// person's own page, but a check that lives only in a page is a check a
// crafted request skips.
package handlers

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/e2e"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// uiView resolves {plugin}/{view} to a running app and its interface view;
// on failure the answer is written.
func (h *AppPlugins) uiView(w http.ResponseWriter, r *http.Request) (*wasmplugin.Installed, *wire.View, bool) {
	p, ok := h.Registry.ByName(chi.URLParam(r, "plugin"))
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return nil, nil, false
	}
	if state, _ := p.State(); state != wasmplugin.StateRunning {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unsupported", "message": "the app is not running"})
		return nil, nil, false
	}
	v, ok := p.Manifest.View(chi.URLParam(r, "view"))
	if !ok || v.UI == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "message": "no such interface"})
		return nil, nil, false
	}
	return p, v, true
}

// UISave writes what an app's interface saved.
func (h *AppPlugins) UISave(w http.ResponseWriter, r *http.Request) {
	if h.off(w) {
		return
	}
	p, v, ok := h.uiView(w, r)
	if !ok {
		return
	}
	if !p.Grants.Has(wasmplugin.PermFilesWrite) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not_granted", "message": "this app was not granted files:write"})
		return
	}
	u := auth.UserFrom(r.Context())
	if u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	limit := h.Registry.MaxOutputBytes()
	if r.ContentLength > limit {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
		return
	}
	q := r.URL.Query()
	// A save in chunks (app_ui_chunks.go): each chunk is one request under
	// the proxy's body limit, and the file is written once, at the end.
	if q.Get("chunk") == "start" || q.Get("session") != "" {
		h.uiSaveChunk(w, r, p, v, u.ID, limit)
		return
	}
	body, size, cleanup, err := sizedBody(r, limit)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unreadable body"})
		return
	}
	defer cleanup()
	uid := u.ID
	if target := strings.TrimSpace(q.Get("path")); target != "" {
		h.uiSaveOver(w, r, p, v, target, body, size, uid)
		return
	}
	h.uiSaveNew(w, r, p, v, strings.TrimSpace(q.Get("dir")), strings.TrimSpace(q.Get("name")), body, size, uid)
}

// uiSaveOver writes a new version of a file the interface was opened with —
// or of the draft that file is (issue #71: a draft is written only by its
// owner, syspath.OwnDraft, and CommitVersion holds it to that).
func (h *AppPlugins) uiSaveOver(w http.ResponseWriter, r *http.Request, p *wasmplugin.Installed, v *wire.View, target string, body io.Reader, size int64, uid int64) {
	storageID, rels, err := h.resolvePaths(r.Context(), 0, []string{target})
	if err != nil || len(rels) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return
	}
	rel := rels[0]
	if !ownsStorage(w, r, storageID, "storage") {
		return
	}
	st, err := h.Store.GetStorage(r.Context(), storageID)
	if err != nil || st == nil || !st.Enabled {
		notFound(w, "storage")
		return
	}
	if st.ReadOnly {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "read_only", "message": "this storage is read-only"})
		return
	}
	if !rootAllows(r.Context(), h.Store, storageID, rel) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "outside this token's root"})
		return
	}
	// A draft of the caller's own is theirs to write; anything else needs
	// editor, like every other save.
	if !syspath.IsDraftOf(rel, uid) && !aclAllowID(r.Context(), h.ACL, h.Store, storageID, rel, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "permission_denied", "message": "insufficient permission"})
		return
	}
	if lk, ok := h.Store.(e2e.NodeByPathLookup); ok && e2e.UnderEncrypted(r.Context(), lk, storageID, "/"+rel) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "encrypted", "message": "an app cannot write into an encrypted folder"})
		return
	}
	drv, err := h.StorageResolver(storageID)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage unavailable"})
		return
	}
	obj, err := drv.Stat(r.Context(), rel)
	if err != nil || obj.Kind == storage.KindDirectory {
		notFound(w, "file")
		return
	}
	if !viewSaves(w, v, rel, obj.Mime) {
		return
	}
	if !h.roomFor(w, r, size) {
		return
	}
	actor := uid
	if err := h.CommitVersion(r.Context(), storageID, rel, body, size, &actor); err != nil {
		writeSaveFailure(w, err)
		return
	}
	h.auditUISave(r, p, storageID, rel, size, "version")
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "path": st.Name + "://" + rel, "name": path.Base(rel), "size": size})
}

// uiSaveNew writes a NEW file into a folder the person picked in filex's own
// dialog ("save as"), under a free name beside anything already there.
func (h *AppPlugins) uiSaveNew(w http.ResponseWriter, r *http.Request, p *wasmplugin.Installed, v *wire.View, dir, name string, body io.Reader, size int64, uid int64) {
	if dir == "" || name == "" || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." || len(name) > 255 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad name", "message": "give path, or dir and a file name"})
		return
	}
	folder, ok := h.checkOutputFolder(w, r, dir, p.Row.ID)
	if !ok {
		return
	}
	if gate(w, r, h.ACL, folder.storage.ID, writegate.Writes(strings.Trim(path.Join(folder.rel, name), "/"))) {
		return
	}
	// "Save as" is held to what a save over a file is held to (security
	// review UI-9): the kind of file the view opens — a diagram editor never
	// saves an .html beside the diagram — and the person's quota.
	if !viewSaves(w, v, name, mime.TypeByExtension(path.Ext(name))) {
		return
	}
	if !h.roomFor(w, r, size) {
		return
	}
	actor := uid
	rel, err := h.CommitSibling(r.Context(), folder.storage.ID, folder.rel, name, body, size, &actor)
	if err != nil {
		writeSaveFailure(w, err)
		return
	}
	h.auditUISave(r, p, folder.storage.ID, rel, size, "new")
	writeJSON(w, http.StatusCreated, map[string]any{"saved": true, "path": folder.storage.Name + "://" + rel, "name": path.Base(rel), "size": size})
}

// viewSaves holds a save to the kind of file its view opens — a diagram
// editor writes diagrams, never the spreadsheet beside them. On refusal the
// answer is written.
func viewSaves(w http.ResponseWriter, v *wire.View, name, mimeType string) bool {
	if len(v.Applies.Ext) == 0 && len(v.Applies.Mime) == 0 {
		return true
	}
	if i := strings.IndexByte(mimeType, ';'); i >= 0 {
		mimeType = strings.TrimSpace(mimeType[:i])
	}
	a := v.Applies
	a.Multi = true
	it := wasmplugin.Item{Kind: "file", Mime: mimeType, Ext: strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")}
	if !wasmplugin.Matches(a, []wasmplugin.Item{it}) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "not_applicable", "message": "this app does not save this kind of file"})
		return false
	}
	return true
}

// roomFor refuses a save that would put the person over their quota, before
// a byte is written (security review UI-7) — the same ceiling, the same
// answer (507 quota_exceeded) as an upload.
func (h *AppPlugins) roomFor(w http.ResponseWriter, r *http.Request, size int64) bool {
	if h.Quota == nil {
		return true
	}
	if err := h.Quota.CheckCanWrite(r.Context(), quotastore.OwnerFrom(r.Context()), size); err != nil {
		if errors.Is(err, quota.ErrQuotaExceeded) {
			writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "quota_exceeded", "message": "there is not enough room left in your quota"})
			return false
		}
		slog.Warn("app ui save: quota check failed", slog.String("err", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save_failed", "message": "filex could not save the file"})
		return false
	}
	return true
}

// writeSaveFailure answers a save that failed while writing.
//
// ⚠ Security review UI-15: what reaches the interface is a CODE and a
// sentence of filex's own. The error itself — a path on the server's disk, a
// driver's or a database's words — goes to the log, never to the app.
func writeSaveFailure(w http.ResponseWriter, err error) {
	// A lock, a name filex keeps, somebody else's draft: the gate's own
	// answers (answerGate), as every other write says them.
	if answerGate(w, err) {
		return
	}
	if strings.HasPrefix(err.Error(), "read_only") {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "read_only", "message": "this storage is read-only"})
		return
	}
	if errors.Is(err, quota.ErrQuotaExceeded) {
		writeJSON(w, http.StatusInsufficientStorage, map[string]string{"error": "quota_exceeded", "message": "there is not enough room left in your quota"})
		return
	}
	slog.Warn("app ui save failed", slog.String("err", err.Error()))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save_failed", "message": "filex could not save the file"})
}

func (h *AppPlugins) auditUISave(r *http.Request, p *wasmplugin.Installed, storageID int64, rel string, size int64, how string) {
	var uid *int64
	if u := auth.UserFrom(r.Context()); u != nil {
		id := u.ID
		uid = &id
	}
	_ = h.Store.InsertAuditEntry(r.Context(), &model.AuditEntry{
		UserID: uid, Action: "app_plugin.ui_save", TargetType: "app_plugin", TargetID: p.Row.Name,
		Metadata: map[string]any{"storage_id": storageID, "path": rel, "size": size, "how": how}, IP: clientIP(r),
	})
}

// sizedBody hands back the request body with its size known: the body itself
// when Content-Length says it, else a spooled copy (a chunked request). Both
// are capped at limit.
func sizedBody(r *http.Request, limit int64) (io.Reader, int64, func(), error) {
	if r.ContentLength >= 0 {
		return http.MaxBytesReader(nil, r.Body, limit), r.ContentLength, func() {}, nil
	}
	f, err := os.CreateTemp("", "filex-ui-save-*")
	if err != nil {
		return nil, 0, func() {}, err
	}
	cleanup := func() { _ = f.Close(); _ = os.Remove(f.Name()) }
	n, err := io.Copy(f, http.MaxBytesReader(nil, r.Body, limit))
	if err != nil {
		cleanup()
		return nil, 0, func() {}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, 0, func() {}, err
	}
	return f, n, cleanup, nil
}
