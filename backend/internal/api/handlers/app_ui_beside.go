// Package handlers — app_ui_beside.go
//
// An app's save that does not go over the file it opened (filex 0.57,
// docs/APP-PLUGINS-API.md → Saving beside the file, conflict copies): the
// Document Server's two rules, now an app's too, written by the SAME writer
// (internal/officesave write.go).
//
//   - `beside=<ext>`: the app saves in a format the file cannot hold (an
//     office editor writes OOXML; a .doc, .xls or .ppt is not written over).
//     The save is written beside the file, which is left as it was, under a
//     name filex chooses - `rapor.docx`, `rapor (2).docx` when that is taken -
//     for a person who may create files in that folder.
//   - `conflict=copy`: the file changed since the app opened it (the
//     `expect` the explorer sends, or the editing session's own record). The
//     save is written as a conflict copy beside it
//     (`<name>.filex-conflict-<UTC time>.<ext>`) instead of being refused with
//     412 `app_save_changed` - which is still the answer to an app that does
//     not ask, so an app built for 0.56 keeps working as it did.
//
// Later saves of the same opening go to that file: the explorer's frame
// saves over the name the answer gave (AppFrame), and an editing session
// records it (coedit.Relay.SavedTo), so every member's save lands there.
// Neither is offered for a file of an encrypted folder: the server holds its
// ciphertext only and could not name or write a new file for it.
package handlers

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/apierr"
	"github.com/brf-tech/filex/backend/internal/assoc"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/officesave"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/perm"
	"github.com/brf-tech/filex/backend/internal/srvtext"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/wasmplugin"
	"github.com/brf-tech/filex/backend/internal/writegate"
	"github.com/brf-tech/filex/backend/internal/writehook"
	"github.com/brf-tech/filex/backend/pkg/pluginkit/wire"
)

// Audit actions of an app's save written beside the file
// (web/tests/lib/auditLabel.test.ts holds their labels).
const (
	auditAppSavedBeside   = "file.app_saved_beside"
	auditAppSavedConflict = "file.app_saved_conflict"
)

// What the people it was written for are told (srvtext, every built-in
// language into the notice's meta) - the editor's words, not the Document
// Server's.
const (
	appSavedBesideTitle   = "server.officesave.saved_beside_title"
	appSavedBesideBody    = "server.officesave.saved_beside_body"
	appSavedConflictTitle = "server.officesave.saved_conflict_title"
	appSavedConflictBody  = "server.officesave.saved_conflict_body"
)

// uiVersionHeader is the read's answer header naming the version handed over
// as a save's `expect` names it, "<size>:<ms>" (the listing's size and date,
// upload_expect.go; app_ui_read.go sets it). The explorer's frame sends it
// back with the app's next save, so a file somebody else changed after the
// app read it is noticed at the save (conflict copies). CORS exposes it
// (routes.go).
const uiVersionHeader = "X-Filex-Version"

// readVersion is the file at target as uiVersionHeader says it: its catalogue
// row's size and date, else what the storage answers.
func (h *AppPlugins) readVersion(r *http.Request, target string) (string, bool) {
	ctx := r.Context()
	storageID, rels, err := h.resolvePaths(ctx, 0, []string{target})
	if err != nil || len(rels) != 1 {
		return "", false
	}
	clean := normalizeDBPath(rels[0])
	if n, err := h.Store.GetNodeByPath(ctx, storageID, pathkey.Hash(storageID, clean)); err == nil && n != nil && n.Type != model.NodeTypeDirectory {
		if ms, ok := listingMtimeMillis(n); ok {
			return strconv.FormatInt(n.Size, 10) + ":" + strconv.FormatInt(ms, 10), true
		}
	}
	if obj, known := statOnStorage(ctx, h.StorageResolver, storageID, clean); known && obj != nil && obj.Kind == storage.KindFile {
		return strconv.FormatInt(obj.Size, 10) + ":" + strconv.FormatInt(obj.Mtime.UnixMilli(), 10), true
	}
	return "", false
}

// uiAside is what a save asks beyond its bytes.
type uiAside struct {
	// beside is the format the save is in when the app says it is not the
	// file's own (`beside=docx`), "" otherwise.
	beside string
	// copy: a file that changed since it was opened gets the save as a
	// conflict copy (`conflict=copy`).
	copy bool
}

// ext is the format a save written at or beside rel is in: beside's when the
// app named one, else rel's own.
func (a uiAside) ext(rel string) string {
	if a.beside != "" {
		return a.beside
	}
	return officesave.DocExt(rel)
}

// besideOf reports whether a save over rel goes beside it: the app's format
// is not rel's own.
func (a uiAside) besideOf(rel string) bool {
	return a.beside != "" && a.beside != officesave.DocExt(rel)
}

// uiAsideOf reads `beside` and `conflict` off a save over rel. An option filex
// does not know is refused (nothing is written); beside a file of an
// encrypted folder is refused, and a conflict copy is not offered there
// (e2eSave: the save's body is the folder's ciphertext). On refusal the
// answer is written.
func uiAsideOf(w http.ResponseWriter, r *http.Request, rel string, e2eSave bool) (uiAside, bool) {
	q := r.URL.Query()
	var a uiAside
	if b := strings.ToLower(strings.TrimSpace(q.Get("beside"))); b != "" {
		if !officesave.BesideFormats[b] {
			writeErrorSaid(w, r, http.StatusBadRequest, "bad_request", "app_save_option", nil)
			return a, false
		}
		a.beside = b
	}
	switch q.Get("conflict") {
	case "":
	case "copy":
		a.copy = !e2eSave
	default:
		writeErrorSaid(w, r, http.StatusBadRequest, "bad_request", "app_save_option", nil)
		return a, false
	}
	if e2eSave && a.besideOf(rel) {
		writeErrorSaid(w, r, http.StatusUnprocessableEntity, "encrypted", "app_save_beside_encrypted",
			apierr.Params{"name": rel, "format": officesave.FormatName(a.beside)})
		return a, false
	}
	return a, true
}

// asideEnv is where an app's save beside a file is written: the shared
// resolver's locks (a vault refuses a name in it), the person's files.create
// on the new name, the app's own lock let through, the catalogue written as
// any app's save.
func (h *AppPlugins) asideEnv(ctx context.Context, storageID int64, drv storage.Driver) (officesave.Env, bool) {
	wr, ok := drv.(storage.Writer)
	if !ok {
		return officesave.Env{}, false
	}
	return officesave.Env{
		Store: h.Store, Driver: drv, Writer: wr,
		Locks: h.ACL.Locks(ctx, storageID),
		App:   writegate.AppFrom(ctx),
		// The person asking: the request's own (aclCanID reads it from ctx).
		MayCreate: func(ctx context.Context, _ int64, rel string) bool {
			return aclCanID(ctx, h.ACL, h.Store, storageID, rel, perm.FilesCreate).ok
		},
		Sync: h.syncer(), Origin: writehook.OriginPlugin,
	}, true
}

// uiSaveAside writes an app's save beside `from` - the opened file rel, or
// the file beside it an editing session's saves already go to - for the
// person uid: a conflict copy when conflict is set, else the file in the
// app's format beside it. The answer says where it went: `name`, and
// `conflict` (the copy's name) for a conflict copy, `beside: true` for a save
// in another format.
func (h *AppPlugins) uiSaveAside(w http.ResponseWriter, r *http.Request, p *wasmplugin.Installed, v *wire.View, st *model.Storage, drv storage.Driver,
	rel, from, ext string, conflict bool, body io.Reader, size int64, uid int64) {
	ctx := r.Context()
	env, ok := h.asideEnv(ctx, st.ID, drv)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "save_failed", nil)
		return
	}
	node := &model.Node{StorageID: st.ID, Path: from, Name: path.Base(from), Type: model.NodeTypeFile}
	var (
		target string
		err    error
	)
	if conflict {
		target, err = env.ConflictTarget(ctx, node, ext, time.Now())
	} else {
		target, err = env.BesideTarget(ctx, node, ext)
	}
	if err != nil {
		writeError(w, r, http.StatusConflict, "save_failed", nil)
		return
	}
	// The kind the view saves holds for the new name too (a diagram editor
	// never writes a spreadsheet beside the diagram).
	if !viewSaves(w, r, v, target, assoc.MimeOf(ext)) {
		return
	}
	if !h.roomFor(w, r, size) {
		return
	}
	// A save in another format is the package that format is, or nothing is
	// written (the Document Server's save is held to the same rule).
	if !conflict {
		br := bufio.NewReader(body)
		head, _ := br.Peek(4)
		if !officesave.BesideBytesOK(head) {
			writeErrorSaid(w, r, http.StatusUnprocessableEntity, "not_applicable", "app_save_beside_bytes",
				apierr.Params{"name": rel, "format": officesave.FormatName(ext)})
			return
		}
		body = br
	}
	save := officesave.Save{
		Target: target, Format: ext, Mime: assoc.MimeOf(ext),
		TitleKey: appSavedBesideTitle, BodyKey: appSavedBesideBody,
		Vars: srvtext.Vars{"name": node.Name, "saved": path.Base(target), "format": officesave.FormatName(ext),
			"ext": officesave.DocExt(node.Name)},
		Action: auditAppSavedBeside, Why: "saved in another format",
	}
	denied := "app_save_beside_denied"
	if conflict {
		save.TitleKey, save.BodyKey = appSavedConflictTitle, appSavedConflictBody
		save.Vars = srvtext.Vars{"name": node.Name, "saved": path.Base(target)}
		save.Action, save.Why = auditAppSavedConflict, "changed while it was edited"
		denied = "app_save_conflict_denied"
	}
	res, err := officesave.Write(ctx, env, node, body, size, []int64{uid}, save)
	var gerr *officesave.GateError
	switch {
	case err == nil:
	case errors.As(err, &gerr):
		writeSaveFailure(w, r, gerr.Err)
		return
	case errors.Is(err, officesave.ErrNoCreate), errors.Is(err, officesave.ErrNoEditor):
		writeErrorSaid(w, r, http.StatusForbidden, "permission_denied", denied,
			apierr.Params{"name": rel, "format": officesave.FormatName(ext)})
		return
	default:
		writeSaveFailure(w, r, err)
		return
	}
	how := "beside"
	if conflict {
		how = "conflict"
	}
	h.auditUISave(r, p, st.ID, target, res.Size, how)
	// Editing together: the session's saves go there from now on, whoever of
	// its members saves.
	h.coeditSaved(r, p, drv, st.ID, rel, target)
	out := map[string]any{"saved": true, "path": st.Name + "://" + target, "name": path.Base(target), "size": res.Size}
	if conflict {
		out["conflict"] = path.Base(target)
	} else {
		out["beside"] = true
	}
	if ms, ok := h.listingModified(ctx, st.ID, target); ok {
		out["modified"] = ms
	}
	writeJSON(w, http.StatusOK, out)
}
