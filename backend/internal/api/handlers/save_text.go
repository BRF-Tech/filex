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
	"github.com/brf-tech/filex/backend/internal/storage"
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
}

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
// Body shape mirrors the origin app's `FilesController::saveText`. We do
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
	if rel == "" || strings.Contains(rel, "..") {
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
	if existing != nil && h.Versions != nil {
		if _, snapErr := h.Versions.Snapshot(r.Context(), existing.ID); snapErr != nil {
			slog.Warn("save-text: snapshot failed (continuing with write)",
				slog.Int64("node", existing.ID),
				slog.String("err", snapErr.Error()))
		}
	}

	body := []byte(req.Content)
	if err := wr.Write(r.Context(), rel, bytes.NewReader(body), int64(len(body))); err != nil {
		writeJSON(w, mapDriverErr(err), map[string]string{"error": "write: " + err.Error()})
		return
	}

	// Refresh cache metadata so the next listing carries the new size.
	if existing != nil {
		_ = h.Store.UpdateNodeMeta(r.Context(), existing.ID, int64(len(body)), existing.Mime, existing.Etag, time.Now())
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
