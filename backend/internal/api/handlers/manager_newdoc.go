package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/newdoc"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/throughput"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// vfNewFileBody is POST /api/files/manager?action=newfile.
//
// `Name` is what the person typed, `Type` is the registry key from
// newdoc.Types() (published in /api/files/capabilities as `newdoc_types`).
// The two are separate because the extension is not a free-text field: it
// selects which bytes get written, and a name alone could not do that. The
// person may still type the extension themselves — see resolveNewDocName.
type vfNewFileBody struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// vfNewFileResponse tells the caller where the file landed, so it can open it
// without guessing how the server resolved the name.
//
// ⚠ Deliberately NOT the re-rendered listing that the other mutating verbs
// answer with. A create is followed by "open the thing I just made", and the
// one fact the client cannot reconstruct is the final path — the name it sent
// may have gained an extension, and on a name collision there is no file at
// all. Returning the listing would answer the question nobody asked and leave
// that one unanswered.
type vfNewFileResponse struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Ext  string `json:"ext"`
	Size int64  `json:"size"`
	Mime string `json:"mime"`
}

// resolveNewDocName turns what the person typed into the file name to write.
//
// The extension is appended unless it is already there, case-insensitively:
// somebody who types "Q3 report" gets "Q3 report.docx", and somebody who types
// "Q3 report.docx" gets the same thing rather than "Q3 report.docx.docx".
// A name that is nothing but the extension ("docx", ".docx") is not a name.
func resolveNewDocName(raw, ext string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", false
	}
	// Path separators are how a "name" becomes a traversal. The caller already
	// chose a directory; the name is a leaf and nothing else.
	if strings.ContainsAny(name, `/\`) {
		return "", false
	}
	// ⚠ A name of nothing but dots is not refused by the checks below: "." and
	// ".." both survive the extension step (as "..md" and "...md") and both
	// pass sanitizeUploadName, because by then they ARE ordinary leaf names.
	// Measured in TestNewFile_RejectsNamesThatAreNotLeaves. Nobody means to
	// create a file called "...md", so the intent is refused here where it is
	// still legible, rather than honoured into a file nobody can explain.
	if strings.Trim(name, ".") == "" {
		return "", false
	}
	suffix := "." + ext
	if !strings.EqualFold(path.Ext(name), suffix) {
		name += suffix
	}
	if strings.EqualFold(name, suffix) {
		return "", false
	}
	// One shared gate with the upload path: whatever it refuses there it
	// refuses here, so a name cannot be legal to create and illegal to upload.
	return sanitizeUploadName(name)
}

// vfNewFile creates an empty document of a known type.
//
// # Why this is not "upload with no bytes"
//
// For a .md it nearly is — an empty text file is a correct empty text file.
// For a .docx it is not: an Office document is a ZIP of XML parts and a
// zero-byte file with that name is rejected by Word, OnlyOffice and
// LibreOffice alike. internal/newdoc owns that difference; this handler only
// asks it for bytes and never decides what "empty" means for a format.
//
// # Why it refuses instead of overwriting
//
// Every other write verb in this file may replace an existing file, and
// writehook.BeforeOverwrite exists to snapshot the bytes first. Creation is
// the one verb where replacing is never the intent: a person asking for a new
// document has not asked to destroy an old one that happens to share a name.
// So the collision is a refusal (409) rather than a snapshot, and the client's
// own pre-flight warning is a courtesy on top of it — not the check.
func (h *Manager) vfNewFile(w http.ResponseWriter, r *http.Request) {
	var body vfNewFileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}

	docType, known := newdoc.Lookup(body.Type)
	if !known {
		// A type this build cannot materialise must never reach storage: the
		// alternative is a file named .docx that no editor can open, which is
		// worse than the error.
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "unsupported document type: " + body.Type,
			"code":  "UNSUPPORTED_TYPE",
		})
		return
	}

	name, nameOK := resolveNewDocName(body.Name, docType.Ext)
	if !nameOK {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad file name"})
		return
	}

	// ACL (acl.LevelEditor on the destination) happens in here. The picker
	// also refuses to offer a folder the person cannot write to, but that is
	// a courtesy to the person, not a permission check: the client is never
	// the check.
	current, destRel, _, ok := h.resolveAdapterDir(w, r, body.Path)
	if !ok {
		return
	}
	if current.ReadOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}

	blob, err := newdoc.Bytes(docType.Ext)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	size := int64(len(blob))

	// The templates are kilobytes, but "small" is not a reason to skip the
	// ceiling: a quota that the New-document button can step over is not a
	// quota. Same status + code as the upload path so one client branch
	// handles both.
	if err := h.checkQuota(r.Context(), size); err != nil {
		if errors.Is(err, quota.ErrQuotaExceeded) {
			slog.Info("newfile refused: quota",
				slog.Int64("user", quotastore.OwnerFrom(r.Context())),
				slog.Int64("size", size))
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error": "quota exceeded",
				"code":  "QUOTA_EXCEEDED",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	drv, err := h.StorageResolver(current.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support write"})
		return
	}

	fullRel := path.Join(destRel, name)

	// A file landing on an existing folder name: the same collision from the
	// other side, and on an object store nothing else would notice it.
	if err := storage.EnsureFileTarget(r.Context(), drv, fullRel); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
		return
	}

	// ⚠ The refusal, and the one place where failing OPEN would be wrong.
	// storage.EnsureFileTarget deliberately allows the write when Stat is
	// inconclusive, because a flaky backend must not start rejecting uploads.
	// Here the stakes are reversed: "I could not tell whether a file is
	// already there" followed by a write is how a document gets destroyed by
	// a feature whose entire purpose is to make a new one. So anything other
	// than a clean not-found stops us.
	if _, err := drv.Stat(r.Context(), fullRel); err == nil {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "a file with that name already exists here",
			"code":  "NAME_TAKEN",
			"name":  name,
		})
		return
	} else if !errors.Is(err, storage.ErrNotFound) {
		slog.Warn("newfile refused: existence check inconclusive",
			slog.Int64("storage", current.ID),
			slog.String("path", fullRel),
			slog.String("err", err.Error()))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "could not check whether that file already exists: " + err.Error(),
			"code":  "EXISTS_CHECK_FAILED",
		})
		return
	}

	// Same reasoning as the upload path: a destination whose directories have
	// no node rows yet is normal, and giving up here would leave the new file
	// out of the catalogue.
	parentID, parentLookupErr := h.ensureDirChain(r.Context(), current.ID, destRel)

	started := time.Now()
	if err := wr.Write(r.Context(), fullRel, bytes.NewReader(blob), size); err != nil {
		slog.Warn("newfile failed",
			slog.Int64("storage", current.ID),
			slog.String("path", fullRel),
			slog.String("type", docType.Ext),
			slog.String("reason", err.Error()))
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + err.Error()})
		return
	}
	throughput.Observe(current.ID, throughput.Write, size, time.Since(started))

	// The mime comes from the registry rather than from sniffing what we just
	// wrote. We know exactly what these bytes are; sniffing an empty .json
	// would answer "text/plain" and sniffing a .docx would answer
	// "application/zip", and both would be a worse answer than the one the
	// template already carries.
	mime := docType.MIME
	clean := normalizeDBPath(fullRel)

	if parentLookupErr != nil {
		// DB mirror unavailable — the bytes ARE on storage, so still announce
		// the write. A transient (unsaved) node makes the writehook skip the
		// antivirus enqueue, same as the upload path.
		writehook.OnFileWritten(r.Context(), current.ID, &model.Node{
			StorageID: current.ID,
			Name:      name,
			Path:      clean,
			Type:      model.NodeTypeFile,
			Size:      size,
			Mime:      mime,
		}, writehook.OriginManager, writehook.Created)
	} else {
		h.mirrorNewDocNode(r, current.ID, parentID, name, clean, mime, size)
	}

	emitFolderChange(current.ID, destRel, realtime.ChangeEvent{Action: "upload"})
	writeJSON(w, http.StatusOK, vfNewFileResponse{
		Path: joinAdapterPath(current.Name, fullRel),
		Name: name,
		Ext:  docType.Ext,
		Size: size,
		Mime: mime,
	})
}

// mirrorNewDocNode puts the new file into the catalogue: node row, search
// index, thumbnail, notify + antivirus gate.
//
// The "row already exists" branch is not dead code even though the write
// refuses to overwrite. The refusal asks STORAGE whether the file is there;
// the catalogue can still hold a row for a file that storage no longer has
// (deleted out of band, restored from a backup, a driver that lost it). In
// that state a plain CreateNode would fail the path-hash uniqueness constraint
// and the file would be on disk but invisible.
func (h *Manager) mirrorNewDocNode(r *http.Request, storageID int64, parentID *int64, name, clean, mime string, size int64) {
	ctx := r.Context()
	hash := pathkey.Hash(storageID, clean)

	if existing, _ := h.Store.GetNodeByPath(ctx, storageID, hash); existing != nil {
		_ = h.Store.UpdateNodeMeta(ctx, existing.ID, size, mime, existing.Etag, time.Now())
		if fresh, _ := h.Store.GetNode(ctx, existing.ID); fresh != nil {
			h.indexNode(ctx, fresh)
			h.dispatchThumb(fresh)
			writehook.OnFileWritten(ctx, storageID, fresh, writehook.OriginManager, writehook.Replaced)
		}
		return
	}

	n := &model.Node{
		StorageID:  storageID,
		ParentID:   parentID,
		Name:       name,
		Path:       clean,
		PathHash:   hash,
		StorageKey: clean,
		Type:       model.NodeTypeFile,
		Size:       size,
		Mime:       mime,
		SyncState:  model.SyncStateSynced,
	}
	created, err := h.Store.CreateNode(ctx, n)
	if err != nil {
		slog.Warn("manager: newfile db create",
			slog.String("path", clean),
			slog.String("err", err.Error()))
		return
	}
	h.indexNode(ctx, created)
	h.dispatchThumb(created)
	writehook.OnFileWritten(ctx, storageID, created, writehook.OriginManager, writehook.Created)
}
