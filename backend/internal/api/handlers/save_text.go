// Package handlers — save_text.go
//
// `/api/files/save-text` accepts plain-text writes from the SFC's code
// editor (Monaco) + markdown viewer "edit" mode. The SFC posts:
//
//	POST /api/files/save-text
//	{ "path": "<adapter>://<rel>", "content": "..." }
//
// We resolve the storage from the adapter prefix, write the content
// through the storage Driver, then refresh the cache row's metadata
// (size + mtime + etag where the driver supplies one).
//
// Whitelist: only files matching a text/code MIME class are saveable
// here. Binary edits go through OnlyOffice's callback channel.
//
// # Antivirus
//
// This surface does NOT call writehook.OnFileWritten, and that omission was
// the gap: the hook enqueues an antivirus scan, so routing every Ctrl+S
// through it would have queued a ClamAV pass per keystroke-burst — and the
// answer taken instead was to scan nothing, which made the editor a way to
// introduce a file that is never scanned on an install where every uploaded
// file is. The two branches below now split it the way the behaviour actually
// splits:
//
//	create (no node row for the path)  → scan immediately, like an upload
//	save   (node row already existed)  → schedule one scan, debounced
//
// The debounce lives in the queue (not_before + a coalescing key), not in a
// timer in this process, so a restart mid-window does not lose the pending
// scan. See queue.AntivirusScanner.EnqueueAfterSave.
//
// # The write event
//
// The scan is the only thing this surface schedules for itself. The EVENT goes
// through the shared gate like every other write surface --
// writehook.EmitWritten, which is OnFileWritten minus the antivirus half it
// has already taken care of -- so the editor is not a place where webhook
// subscribers hear something different from the browser upload that wrote the
// same file. The two branches split the event exactly as they split the scan:
//
//	create (no node row for the path)  -> file.uploaded
//	save   (node row already existed)  -> file.updated
//
// Same `existing` fact, one decision, two consequences. Until this was wired
// the editor announced NOTHING at all: a file created or rewritten in the
// browser reached no webhook and no notification bell, so an integration
// watching a folder simply did not see edits happen.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/realtime"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// VersionSnapshotter is the narrow surface save-text needs to capture
// a snapshot before a destructive write.
type VersionSnapshotter interface {
	Snapshot(ctx context.Context, nodeID int64) (*model.NodeVersion, error)
}

// SaveText handles plain-text edits from the SFC's code/markdown viewer.
type SaveText struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
	Versions        VersionSnapshotter
	ACL             *acl.Resolver
	// Index keeps the saved text searchable. Optional; nil skips indexing.
	Index *search.Index
}

// AttachSearchIndex wires the search index. ⚠ Without it an edit saved from
// the built-in editor never reaches Bleve: the file keeps whatever text it had
// when it was last indexed, so a content search finds the OLD words and misses
// the new ones — and a name search only appears to work because the search
// endpoint falls back to a SQL LIKE over node rows.
func (h *SaveText) AttachSearchIndex(i *search.Index) { h.Index = i }

// NewSaveText constructs the handler.
func NewSaveText(store db.Store, resolver func(int64) (storage.Driver, error)) *SaveText {
	return &SaveText{Store: store, StorageResolver: resolver}
}

// AttachACL wires the RBAC resolver so save-text requires ≥editor on the file.
func (h *SaveText) AttachACL(r *acl.Resolver) { h.ACL = r }

// AttachVersions wires the versioning service so save-text snapshots
// the previous content before writing. Without it edits silently
// overwrite history (the SFC's "Sürüm geçmişi" / Versions page would
// show no entries even after multiple saves).
func (h *SaveText) AttachVersions(v VersionSnapshotter) {
	h.Versions = v
}

type saveTextReq struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Save writes `content` as the new body of the addressed file.
//
// Body shape mirrors brf-mono's `FilesController::saveText`. We do
// NOT version on save — the SFC's preview re-renders against the new
// bytes immediately, and a future versions endpoint can snapshot the
// previous payload before the write if requested.
func (h *SaveText) Save(w http.ResponseWriter, r *http.Request) {
	if h.StorageResolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "storage offline"})
		return
	}
	var req saveTextReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path"})
		return
	}

	adapter, rel := splitAdapterPath(req.Path)
	if rel == "" || pathHasDotDot(rel) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad path"})
		return
	}
	storages, err := h.Store.ListEnabledStorages(r.Context())
	if err != nil || len(storages) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no storages"})
		return
	}
	if adapter == "" {
		adapter = storages[0].Name
	}
	var st *storage.Object // unused, kept to mirror manager.go conventions
	_ = st
	var storageID int64
	var readOnly bool
	for _, s := range storages {
		if s.Name == adapter {
			storageID = s.ID
			readOnly = s.ReadOnly
			break
		}
	}
	if storageID == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown adapter: " + adapter})
		return
	}
	if readOnly {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "storage is read-only"})
		return
	}
	// RBAC: editing file content needs ≥editor.
	if !aclAllowID(r.Context(), h.ACL, h.Store, storageID, rel, acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}

	if !isTextSafePath(rel) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "extension not allowed for save-text"})
		return
	}

	drv, err := h.StorageResolver(storageID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver: " + err.Error()})
		return
	}
	wr, ok := drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "driver does not support write"})
		return
	}
	// Saving text onto an existing folder name would leave `X` and `X/…` side
	// by side on an object store (storage.ErrKindConflict).
	if err := storage.EnsureFileTarget(r.Context(), drv, rel); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": err.Error()})
		return
	}
	// Look up the existing node FIRST so we can snapshot the
	// pre-edit bytes into the version history before the destructive
	// write. The cache row's `clean`/`hash` derivation also feeds the
	// post-write metadata refresh below.
	clean := strings.TrimRight(path.Clean("/"+rel), "/")
	hash := pathkey.Hash(storageID, clean)
	var existing *model.Node
	if n, err := h.Store.GetNodeByPath(r.Context(), storageID, hash); err == nil {
		existing = n
	}
	// This call predates writehook.BeforeOverwrite and still snapshots
	// directly through h.Versions, because it already has the node row in
	// hand. What it no longer does is proceed past a failure: it used to log
	// "snapshot failed (continuing with write)" and overwrite anyway, which
	// contradicted Snapshot's own documented contract two lines up from its
	// body -- "if the snapshot itself fails the caller should NOT proceed".
	// Editing a file in the browser is now refused on a failed snapshot for
	// the same reason every other write surface is: losing history is not a
	// reason to also lose the file.
	if existing != nil && h.Versions != nil {
		if _, snapErr := h.Versions.Snapshot(r.Context(), existing.ID); snapErr != nil {
			slog.Warn("save-text refused: snapshot",
				slog.Int64("node", existing.ID),
				slog.Int64("storage", storageID),
				slog.String("path", rel),
				slog.String("err", snapErr.Error()))
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "could not preserve the existing file: " + snapErr.Error(),
				"code":  "SNAPSHOT_FAILED",
			})
			return
		}
	}

	body := []byte(req.Content)
	if err := wr.Write(r.Context(), rel, bytes.NewReader(body), int64(len(body))); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + err.Error()})
		return
	}

	// Whatever happens to the row below, the bytes changed and anyone looking
	// at this folder is now out of date.
	defer emitFolderChange(storageID, path.Dir(clean), realtime.ChangeEvent{
		Action: "upload", Name: path.Base(clean),
	})
	sy := protocolsync.New(h.Store, h.Index, nil, "")

	// Refresh cache metadata so the next listing carries the new size.
	if existing != nil {
		_ = h.Store.UpdateNodeMeta(r.Context(), existing.ID, int64(len(body)), existing.Mime, existing.Etag, time.Now())
		existing.Size = int64(len(body))
		// ⚠ Re-index, or the document keeps the pre-edit text for good:
		// nothing else ever revisits a file whose path did not change.
		sy.IndexNode(r.Context(), existing)
		// SAVE to a file that already existed → file.updated, and a debounced
		// scan. Scanning on every Ctrl+S is why this surface had no scan at
		// all; one scan per file per editing window is the answer to that, not
		// no scan.
		writehook.EmitWritten(r.Context(), storageID, existing, writehook.OriginManager, writehook.Replaced,
			map[string]any{"editor": true})
		enqueueAntivirusScanAfterSave(r.Context(), existing)
	} else {
		// A brand-new file had NO node row: the bytes went to the driver and
		// the catalogue only learned about them on the next storage scan —
		// which meant the file was invisible until then, and the row it
		// eventually got belonged to nobody, so those bytes were never
		// counted against anyone's quota. Create the row here, the way every
		// other write path does.
		var parentID *int64
		if dir := path.Dir(clean); dir != "" && dir != "." && dir != "/" {
			if p, perr := h.Store.GetNodeByPath(r.Context(), storageID, pathkey.Hash(storageID, dir)); perr == nil && p != nil {
				parentID = &p.ID
			}
		}
		created, cerr := h.Store.CreateNode(r.Context(), &model.Node{
			StorageID:  storageID,
			ParentID:   parentID,
			Name:       path.Base(clean),
			Path:       clean,
			PathHash:   hash,
			StorageKey: clean,
			Type:       model.NodeTypeFile,
			Size:       int64(len(body)),
			Mime:       "text/plain; charset=utf-8",
			SyncState:  model.SyncStateSynced,
		})
		if cerr != nil {
			slog.Warn("save-text: node create",
				slog.String("path", clean), slog.String("err", cerr.Error()))
		} else {
			sy.IndexNode(r.Context(), created)
			writehook.EmitWritten(r.Context(), storageID, created, writehook.OriginManager, writehook.Created,
				map[string]any{"editor": true})
			// CREATE → scan now, exactly like an upload. This branch is the
			// hole: `existing == nil` means no catalogue row addressed this
			// path before the write, so these bytes are new to filex and
			// nothing else will ever look at them. Until now the editor was
			// the one way to put a file on an install where every uploaded
			// file is scanned and have it never be.
			//
			// ⚠ There is no debounce here on purpose. A create happens once;
			// the saves that follow it take the branch above.
			enqueueAntivirusScan(r.Context(), created)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"size": len(body),
	})
}

// isTextSafePath returns true for extensions that round-trip cleanly as
// UTF-8 plain text — JSON, YAML, code, markdown, config files. Binary
// formats (images, archives, office docs) are rejected; they have
// dedicated edit channels (OnlyOffice / drawio / explicit upload).
func isTextSafePath(rel string) bool {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(rel), "."))
	switch ext {
	case "txt", "md", "markdown", "log", "csv", "tsv",
		"conf", "ini", "env", "toml", "cfg", "properties",
		"json", "jsonc", "yaml", "yml", "xml", "svg", "html", "htm",
		"css", "scss", "sass", "less",
		"js", "mjs", "cjs", "ts", "tsx", "jsx", "vue", "svelte",
		"php", "py", "rb", "rs", "go", "java", "kt", "swift",
		"cpp", "c", "h", "hpp", "cs", "dart",
		"sh", "bash", "zsh", "sql", "lua", "pl", "r",
		"dockerfile", "gradle", "gitignore", "editorconfig":
		return true
	}
	// Files with no extension OR special filenames.
	base := strings.ToLower(path.Base(rel))
	switch base {
	case "dockerfile", "makefile", ".env", ".gitignore", ".editorconfig":
		return true
	}
	return false
}
