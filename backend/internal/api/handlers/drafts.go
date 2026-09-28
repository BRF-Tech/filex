package handlers

// Drafts (issue #71): a new document is a DRAFT until its first save.
//
//	POST   /api/files/drafts              New document → a draft (body = action=newfile's)
//	GET    /api/files/drafts              the caller's drafts, across every storage
//	GET    /api/files/drafts/count        {count, limit} — the navigation panel's badge
//	GET    /api/files/drafts/{key}        one draft — what an editor on its path asks
//	POST   /api/files/drafts/{key}/save   move it to where it is meant to go
//	DELETE /api/files/drafts/{key}        discard: the draft goes to the trash
//
// A draft is a real file in the storage the person chose, in THEIR drafts
// area: `.filex-drafts/<user id>/<key>/<name>` (syspath.Drafts). Every editor
// opens it as the file it is — the text editor, draw.io, ONLYOFFICE, an app's
// own — and saves into it through its ordinary door (save-text, the document
// server's callback, an app's new version), each of which claims
// syspath.OwnDraft for the person it acts for. The drafts table remembers
// what the file cannot say: whose it is and where it is meant to go.
//
// ⚠⚠ One person's, and only while there IS a person. A drafts area is seen by
// nobody but its owner (acl.Set, syspath.SealedFor) and offered only to a
// caller that is a person acting for themselves: not to an app token, and not
// to a caller confined to one folder (a `root:` token, an X-Filex-Root
// header). The embeds that run under one shared, confined proxy token are both
// — every visitor of every project there is the SAME filex account — so a
// drafts area there would be one drawer for all of them, outside the folder
// each is confined to. Those callers keep the direct create (action=newfile),
// and capabilities says so by leaving `drafts` out (draftsFor).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/confine"
	"github.com/brf-tech/filex/backend/internal/drafts"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/syspath"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Why a draft request was refused, as the response's `code`.
const (
	// draftsUnavailable: the caller is not a person acting for themselves
	// (an app token, a confined caller) — see the package note above.
	draftsUnavailable = "DRAFTS_UNAVAILABLE"
	// draftLimitReached: the caller already keeps as many drafts as the
	// instance allows (drafts.LimitSetting).
	draftLimitReached = "DRAFT_LIMIT"
	// draftTargetTaken: something already has the draft's name where it is
	// meant to go. The answer carries `suggested`, the free `name (2).ext`
	// beside it; the client asks the person and saves again with `as`.
	draftTargetTaken = "TARGET_TAKEN"
	// draftFolderGone: the folder the draft is meant for is no longer there.
	draftFolderGone = "FOLDER_GONE"
)

// draftsFor reports whether the caller may keep drafts: a signed-in person,
// not an app token, not confined to a folder. The ONE rule — capabilities
// publishes `drafts` by it and every draft endpoint refuses by it.
//
// ⚠ "A person" is a signed-in user OR a person's own token: capabilities is
// a public route that annotates a token without resolving its account
// (auth.AnnotateToken), so the account is not on the context there — the
// token's kind is what says it is a person's.
func draftsFor(r *http.Request) bool {
	ctx := r.Context()
	tok := auth.TokenFrom(ctx)
	if tok.IsApp() || (auth.UserFrom(ctx) == nil && tok == nil) {
		return false
	}
	if _, confined, err := confine.FromRequest(r); confined || err != nil {
		return false
	}
	if _, confined := confine.RootFrom(ctx); confined {
		return false
	}
	return true
}

// sealedFor is syspath.SealedFor for the caller of ctx: filex's sealed trees
// are never served by path, except the caller's own draft files, which their
// editor previews, downloads and hands to the document server by path.
func sealedFor(ctx context.Context, rel string) bool {
	return syspath.SealedFor(rel, currentUserID(ctx))
}

// draftView is one draft as the API answers it.
type draftView struct {
	Key string `json:"key"`
	// Name is the file's name — the one it is meant to have.
	Name string `json:"name"`
	// Path is where the draft's bytes are, adapter-qualified: what an editor
	// opens and saves. Only ever sent to the draft's owner.
	Path    string `json:"path"`
	Storage string `json:"storage"`
	// TargetDir and Target are where it is meant to go, adapter-qualified.
	TargetDir string `json:"target_dir"`
	Target    string `json:"target"`
	// Type is the New-document type it was made as (the editor opens it as
	// this, so `LICENSE` made as Plain text opens in the text editor).
	Type       string     `json:"type"`
	Size       int64      `json:"size"`
	Mime       string     `json:"mime,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ModifiedAt *time.Time `json:"modified_at,omitempty"`
}

func viewOfDraft(d *model.Draft, storageName string) draftView {
	target := strings.Trim(path.Join(d.TargetDir, d.TargetName), "/")
	v := draftView{
		Key:       d.Key,
		Name:      d.TargetName,
		Path:      joinAdapterPath(storageName, d.Path),
		Storage:   storageName,
		TargetDir: joinAdapterPath(storageName, d.TargetDir),
		Target:    joinAdapterPath(storageName, target),
		Type:      d.DocType,
		Size:      d.Size,
		Mime:      d.Mime,
		CreatedAt: d.CreatedAt,
	}
	if d.Mtime != nil && !d.Mtime.IsZero() {
		m := *d.Mtime
		v.ModifiedAt = &m
	}
	return v
}

// refuseNoDrafts answers a caller draftsFor turns away.
func refuseNoDrafts(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": "drafts are kept for a person acting for themselves",
		"code":  draftsUnavailable,
	})
}

// storagesByID is the caller's visible storages (tenant-scoped), by id.
func (h *Manager) storagesByID(ctx context.Context) (map[int64]*model.Storage, error) {
	list, err := h.Store.ListEnabledStorages(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]*model.Storage, len(list))
	for _, s := range list {
		out[s.ID] = s
	}
	return out, nil
}

// visibleDrafts is the caller's live drafts on storages they can still see.
func (h *Manager) visibleDrafts(ctx context.Context, uid int64) ([]draftView, error) {
	rows, err := h.Store.ListLiveDrafts(ctx, uid)
	if err != nil {
		return nil, err
	}
	sts, err := h.storagesByID(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]draftView, 0, len(rows))
	for _, d := range rows {
		st := sts[d.StorageID]
		// A draft whose file no longer sits in its owner's area (a save that
		// moved the bytes and then failed to drop the row) is not a draft.
		if st == nil || !syspath.IsDraftOf(d.Path, uid) {
			continue
		}
		out = append(out, viewOfDraft(d, st.Name))
	}
	return out, nil
}

// ListDrafts answers GET /api/files/drafts.
func (h *Manager) ListDrafts(w http.ResponseWriter, r *http.Request) {
	if !draftsFor(r) {
		refuseNoDrafts(w)
		return
	}
	ctx := r.Context()
	list, err := h.visibleDrafts(ctx, currentUserID(ctx))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"drafts": list,
		"count":  len(list),
		"limit":  drafts.Limit(ctx, h.Store),
	})
}

// CountDrafts answers GET /api/files/drafts/count — the navigation panel's
// badge, asked after every change the explorer makes to a draft.
func (h *Manager) CountDrafts(w http.ResponseWriter, r *http.Request) {
	if !draftsFor(r) {
		refuseNoDrafts(w)
		return
	}
	ctx := r.Context()
	list, err := h.visibleDrafts(ctx, currentUserID(ctx))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(list), "limit": drafts.Limit(ctx, h.Store)})
}

// ownDraft loads the draft {key} of the caller. Anybody else's draft — and a
// key that names none — is the same 404, so a key cannot be probed.
// live=true also refuses a draft that is in the trash (discarded).
func (h *Manager) ownDraft(w http.ResponseWriter, r *http.Request, live bool) (*model.Draft, *model.Storage, bool) {
	if !draftsFor(r) {
		refuseNoDrafts(w)
		return nil, nil, false
	}
	ctx := r.Context()
	uid := currentUserID(ctx)
	d, err := h.Store.GetDraftByKey(ctx, chi.URLParam(r, "key"))
	if err != nil || d == nil || d.UserID != uid || (live && !d.NodeLive) || (live && !syspath.IsDraftOf(d.Path, uid)) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "draft not found"})
		return nil, nil, false
	}
	sts, err := h.storagesByID(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return nil, nil, false
	}
	st := sts[d.StorageID]
	if st == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "draft not found"})
		return nil, nil, false
	}
	return d, st, true
}

// GetDraft answers GET /api/files/drafts/{key}.
func (h *Manager) GetDraft(w http.ResponseWriter, r *http.Request) {
	d, st, ok := h.ownDraft(w, r, true)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, viewOfDraft(d, st.Name))
}

// CreateDraft answers POST /api/files/drafts: New document, as a draft.
//
// The request is action=newfile's (vfNewFileBody) and is judged by the same
// half (planNewDoc): the type, the name, the folder it is meant for — the
// caller must be able to write THERE, because that is where it will go — the
// storage's read-only switch and the caller's quota. What differs is where the
// bytes land: the caller's drafts area, and nothing at the destination. A name
// already taken there is not refused: whether the draft goes in beside it as
// `name (2).ext` is asked when it is saved (SaveDraft), which is when it
// matters.
func (h *Manager) CreateDraft(w http.ResponseWriter, r *http.Request) {
	if !draftsFor(r) {
		refuseNoDrafts(w)
		return
	}
	var body vfNewFileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	plan, ok := h.planNewDoc(w, r, body)
	if !ok {
		return
	}
	ctx := r.Context()
	uid := currentUserID(ctx)
	st := plan.storage
	target := path.Join(plan.destRel, plan.name)
	// The destination is NAMED, not written, today: a name that is filex's own
	// (`.keepdir`) or a folder among filex's own is refused now rather than at
	// the save.
	if gate(w, r, h.ACL, st.ID, writegate.Names(plan.destRel), writegate.Names(target)) {
		return
	}

	limit := drafts.Limit(ctx, h.Store)
	n, err := h.Store.CountLiveDrafts(ctx, uid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if n >= limit {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "you already keep as many drafts as this server allows; save or delete some",
			"code":  draftLimitReached,
			"limit": limit,
			"count": n,
		})
		return
	}

	key := drafts.NewKey()
	dir := syspath.DraftDir(uid, key)
	rel := dir + "/" + plan.name
	// A folder at a time, for the drivers that need one before a write into
	// it (WebDAV answers 409 to a PUT into a missing collection). Best effort:
	// a driver with no folders has nothing to make, and the write below says
	// whether it worked.
	if mk, ok := plan.drv.(storage.Mkdirer); ok {
		built := ""
		for _, seg := range strings.Split(dir, "/") {
			built = path.Join(built, seg)
			_ = mk.Mkdir(ctx, built)
		}
	}
	size := int64(len(plan.blob))
	if err := plan.wr.Write(ctx, rel, bytes.NewReader(plan.blob), size); err != nil {
		slog.Warn("draft create failed",
			slog.Int64("storage", st.ID), slog.String("type", plan.ty.Ext), slog.String("reason", err.Error()))
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + clientErrText(err)})
		return
	}
	sy := protocolsync.New(h.Store, h.Index, nil, writehook.OriginManager)
	node, _, ok := sy.WriteRows(ctx, st, rel, size, plan.ty.MIME)
	var d *model.Draft
	if ok {
		d, err = h.Store.CreateDraft(ctx, &model.Draft{
			Key: key, UserID: uid, StorageID: st.ID, NodeID: node.ID,
			TargetDir: strings.Trim(plan.destRel, "/"), TargetName: plan.name, DocType: plan.ty.Ext,
		})
	}
	if !ok || err != nil {
		// A file with no draft row is a file nobody can reach: take it back.
		h.dropDraftFiles(ctx, st, plan.drv, dir)
		msg := "could not record the draft"
		if err != nil {
			msg += ": " + err.Error()
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
		return
	}
	view := viewOfDraft(d, st.Name)
	// The same fields action=newfile answers with (NewFileResponse), so the
	// explorer opens a draft exactly as it opened the file it used to create
	// — plus the draft itself.
	writeJSON(w, http.StatusCreated, map[string]any{
		"path":  view.Path,
		"name":  plan.name,
		"ext":   plan.ty.Ext,
		"size":  size,
		"mime":  plan.ty.MIME,
		"draft": view,
	})
}

// dropDraftFiles removes a draft's folder from the storage and its rows from
// the catalogue — after its file was moved out of it (a save), or when the
// draft could not be recorded at all. Best effort: what is left behind is an
// empty folder in a sealed area, which is untidy and harms nobody.
func (h *Manager) dropDraftFiles(ctx context.Context, st *model.Storage, drv storage.Driver, dir string) {
	if del, ok := drv.(storage.Deleter); ok {
		if err := del.Delete(ctx, dir); err != nil && !errors.Is(err, storage.ErrNotFound) {
			slog.Warn("draft folder left behind", slog.Int64("storage", st.ID), slog.String("err", err.Error()))
		}
	}
	protocolsync.New(h.Store, h.Index, nil, writehook.OriginManager).DeleteRows(ctx, st, dir)
}

// vfDraftSaveBody is POST /api/files/drafts/{key}/save.
type vfDraftSaveBody struct {
	// As is the name to save under instead of the draft's own — only ever the
	// `suggested` name a TARGET_TAKEN answer offered, which the person agreed
	// to. It must be free too, or the answer is TARGET_TAKEN again with a new
	// suggestion: what the person agreed to is exactly what they get.
	As string `json:"as"`
}

// SaveDraft answers POST /api/files/drafts/{key}/save: the draft goes where it
// is meant to go, and stops being a draft.
//
// Nothing is ever replaced. When the name is free the file lands there; when
// something already has it the answer is 409 TARGET_TAKEN with `suggested`,
// the `name (2).ext` beside it (ops.UniqueDestNumbered — the numbering the
// New document dialog suggests), and nothing moves until the person agrees
// and the client asks again with `as`.
//
// The file is MOVED, not copied: its catalogue row keeps its id, so an editor
// still open on it — the document server's session above all, which posts
// its final save by node id seconds after the editor closes — writes into the
// saved document rather than into a draft that is gone.
func (h *Manager) SaveDraft(w http.ResponseWriter, r *http.Request) {
	d, st, ok := h.ownDraft(w, r, true)
	if !ok {
		return
	}
	var body vfDraftSaveBody
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
	}
	ctx := r.Context()
	if st.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}
	targetDir := strings.Trim(d.TargetDir, "/")
	// The caller writes into the folder now, whatever they could do when the
	// draft was made.
	if !h.allowed(ctx, st, targetDir, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	name := d.TargetName
	if as := strings.TrimSpace(body.As); as != "" {
		clean, ok := sanitizeUploadName(as)
		if !ok || clean != as {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad file name", "code": newDocBadName})
			return
		}
		name = clean
	}
	want := path.Join(targetDir, name)
	if gate(w, r, h.ACL, st.ID, writegate.Names(targetDir), writegate.Writes(want)) {
		return
	}
	drv, err := h.StorageResolver(st.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	if targetDir != "" {
		if obj, err := drv.Stat(ctx, targetDir); err != nil || obj.Kind != storage.KindDirectory {
			if err == nil || errors.Is(err, storage.ErrNotFound) {
				writeJSON(w, http.StatusConflict, map[string]string{
					"error":      "the folder this draft is meant for is not there any more",
					"code":       draftFolderGone,
					"target_dir": joinAdapterPath(st.Name, targetDir),
				})
				return
			}
			writeJSON(w, mapDriverErr(err), map[string]string{"error": clientErrText(err)})
			return
		}
	}
	// ⚠ Fail closed: "could not tell whether something is there" followed by a
	// move is how a document gets replaced by a feature that promised never to.
	taken, err := destinationTaken(ctx, h.Store, drv, st.ID, "", want)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "could not check whether that name is free: " + clientErrText(err),
			"code":  "EXISTS_CHECK_FAILED",
		})
		return
	}
	if taken {
		free, err := ops.UniqueDestNumbered(ctx, drv, want, liveRowTaken(ctx, h.Store, st.ID))
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": clientErrText(err), "code": "NO_FREE_NAME"})
			return
		}
		writeJSON(w, http.StatusConflict, map[string]string{
			"error":      "a file with that name is already there",
			"code":       draftTargetTaken,
			"name":       name,
			"suggested":  path.Base(free),
			"target_dir": joinAdapterPath(st.Name, targetDir),
		})
		return
	}

	// The move and its bookkeeping finish even if the client leaves half way.
	ctx, cancel := detachedMutation(ctx)
	defer cancel()
	src := strings.TrimPrefix(normalizeDBPath(d.Path), "/")
	if err := storage.EnsureFileTarget(ctx, drv, want); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
		return
	}
	if err := moveOneFile(ctx, drv, src, want); err != nil {
		slog.Warn("draft save failed", slog.Int64("storage", st.ID), slog.String("err", err.Error()))
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "save: " + clientErrText(err)})
		return
	}
	sy := protocolsync.New(h.Store, h.Index, nil, writehook.OriginManager)
	sy.MoveRows(ctx, st, src, want)
	if err := h.Store.DeleteDraft(ctx, d.ID); err != nil {
		// The bytes are where they belong; a row left behind no longer names a
		// file in the drafts area, and the listing drops it (visibleDrafts).
		slog.Warn("draft row left behind after its save", slog.Int64("draft", d.ID), slog.String("err", err.Error()))
	}
	h.dropDraftFiles(ctx, st, drv, path.Dir(src))
	// To everyone else the document appears NOW, at its place: the event is a
	// new file there (a webhook reads `file.uploaded`), and it is scanned like
	// one. Its time in the drafts area was nobody's business.
	if node, err := h.Store.GetNode(ctx, d.NodeID); err == nil && node != nil {
		writehook.OnFileWritten(ctx, st.ID, node, writehook.OriginManager, writehook.Created)
		h.dispatchThumb(node)
	}
	emitFolderChange(st.ID, targetDir, realtime.ChangeEvent{Action: "upload", Name: path.Base(want)})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"path":       joinAdapterPath(st.Name, want),
		"name":       path.Base(want),
		"target_dir": joinAdapterPath(st.Name, targetDir),
	})
}

// moveOneFile moves one file within a storage: a rename where the driver has
// one, else copy then delete — the same two shapes trash.Put takes, so a
// driver that can hold a draft can also save it.
func moveOneFile(ctx context.Context, drv storage.Driver, src, dst string) error {
	if mv, ok := drv.(storage.Mover); ok {
		return mv.Move(ctx, src, dst)
	}
	cp, okC := drv.(storage.Copier)
	del, okD := drv.(storage.Deleter)
	if !okC || !okD {
		return storage.ErrUnsupported
	}
	if err := cp.Copy(ctx, src, dst); err != nil {
		return err
	}
	return del.Delete(ctx, src)
}

// DiscardDraft answers DELETE /api/files/drafts/{key}: the draft goes to the
// TRASH, like any deleted file, and the trash's retention deletes it for good
// (the owner's design, issue #71). Its row is kept: a restore from the trash
// puts the file back in its drafts folder, and it is a draft again. The trash
// shows it to its owner alone (Trash.trashListKeep).
func (h *Manager) DiscardDraft(w http.ResponseWriter, r *http.Request) {
	d, st, ok := h.ownDraft(w, r, true)
	if !ok {
		return
	}
	ctx, cancel := detachedMutation(r.Context())
	defer cancel()
	drv, err := h.StorageResolver(st.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	rel := strings.TrimPrefix(normalizeDBPath(d.Path), "/")
	sy := protocolsync.New(h.Store, h.Index, nil, writehook.OriginManager)
	out, terr := trash.Put(ctx, drv, rel)
	switch {
	case terr == nil && out.Trashed:
		sy.Trash(ctx, st, rel, out.Key)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trashed": true})
	case terr == nil && out.Missing, errors.Is(terr, trash.ErrUnsupported):
		// Nothing left to keep (the bytes were already gone), or no way to keep
		// them (a driver that can neither move nor copy): the draft ends here.
		if del, ok := drv.(storage.Deleter); ok && terr != nil {
			_ = del.Delete(ctx, rel)
		}
		_ = h.Store.DeleteDraft(ctx, d.ID)
		h.dropDraftFiles(ctx, st, drv, path.Dir(rel))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trashed": false})
	default:
		writeJSON(w, mapDriverErr(terr), map[string]string{"error": "trash: " + clientErrText(terr)})
	}
}
