package handlers

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Archive handles zip-listing, zip-extract, and zip-create operations.
//
// All zip ops materialize the source archive to a tmp file (since
// archive/zip needs an io.ReaderAt + Seeker), then stream extracts back
// to storage.
type Archive struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
	ACL             *acl.Resolver
}

// NewArchive constructs an Archive handler.
func NewArchive(store db.Store, resolver func(int64) (storage.Driver, error)) *Archive {
	return &Archive{Store: store, StorageResolver: resolver}
}

// AttachACL wires the RBAC resolver: list needs ≥viewer on the archive,
// extract/add need ≥editor on the write target (+ ≥viewer on sources read).
func (a *Archive) AttachACL(r *acl.Resolver) { a.ACL = r }

// archiveRequest is the union body for /api/files/archive/{list,extract,add}.
type archiveRequest struct {
	StorageID int64      `json:"storage_id"`
	Path      string     `json:"path"`
	Members   []string   `json:"members,omitempty"`
	DestDir   string     `json:"dest,omitempty"`
	Files     []addEntry `json:"files,omitempty"`
}

// addEntry is one source for /archive/add.
//
// Source is the path inside the storage to read from; Name is the
// destination path inside the zip.
type addEntry struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// archiveListEntry is the wire format for /archive/list responses.
type archiveListEntry struct {
	Name  string    `json:"name"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
	IsDir bool      `json:"is_dir"`
}

// List enumerates archive members.
func (a *Archive) List(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path"})
		return
	}
	storageID, rel, err := a.resolveStorage(r.Context(), req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.StorageID = storageID
	req.Path = rel
	if !aclAllowID(r.Context(), a.ACL, a.Store, storageID, rel, acl.LevelViewer) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	tmp, err := a.fetchToTemp(r, req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer os.Remove(tmp)

	zr, err := zip.OpenReader(tmp)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a zip: " + err.Error()})
		return
	}
	defer zr.Close()

	out := make([]archiveListEntry, 0, len(zr.File))
	for _, f := range zr.File {
		out = append(out, archiveListEntry{
			Name:  f.Name,
			Size:  int64(f.UncompressedSize64),
			Mtime: f.Modified,
			IsDir: strings.HasSuffix(f.Name, "/"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// Extract pulls members out of an archive into the destination directory.
//
// DestDir is interpreted on the SAME storage as the source. Members
// defaults to "all" when empty.
func (a *Archive) Extract(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path"})
		return
	}
	storageID, rel, err := a.resolveStorage(r.Context(), req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.StorageID = storageID
	req.Path = rel
	drv, err := a.StorageResolver(req.StorageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
		return
	}
	writer, ok := drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "storage not writable"})
		return
	}

	tmp, err := a.fetchToTemp(r, req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer os.Remove(tmp)

	zr, err := zip.OpenReader(tmp)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not a zip: " + err.Error()})
		return
	}
	defer zr.Close()

	wanted := map[string]bool{}
	for _, m := range req.Members {
		wanted[m] = true
	}
	dest := req.DestDir
	if dest == "" {
		dest = path.Dir(req.Path)
	}
	dest = "/" + strings.TrimLeft(path.Clean("/"+dest), "/")

	// RBAC: reading the archive needs ≥viewer; extracting writes into dest → ≥editor.
	if !aclAllowID(r.Context(), a.ACL, a.Store, req.StorageID, strings.Trim(req.Path, "/"), acl.LevelViewer) ||
		!aclAllowID(r.Context(), a.ACL, a.Store, req.StorageID, strings.Trim(dest, "/"), acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}

	mkdirer, _ := drv.(storage.Mkdirer)
	keys := make([]string, 0)
	for _, f := range zr.File {
		if len(wanted) > 0 && !wanted[f.Name] && !wanted[strings.TrimSuffix(f.Name, "/")] {
			continue
		}
		safeRel, err := sanitizeZipPath(f.Name)
		if err != nil {
			slog.Warn("archive: skipped zip-slip entry", slog.String("name", f.Name), slog.String("err", err.Error()))
			continue
		}
		target := path.Join(dest, safeRel)
		// Defense in depth: ensure the joined target stays under dest.
		if !strings.HasPrefix(target+"/", strings.TrimRight(dest, "/")+"/") {
			slog.Warn("archive: target escapes dest after join", slog.String("target", target))
			continue
		}
		if strings.HasSuffix(f.Name, "/") {
			if mkdirer != nil {
				_ = mkdirer.Mkdir(r.Context(), target)
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			slog.Warn("archive: zip member open", slog.String("name", f.Name), slog.String("err", err.Error()))
			continue
		}
		err = writer.Write(r.Context(), target, rc, int64(f.UncompressedSize64))
		_ = rc.Close()
		if err != nil {
			slog.Warn("archive: extract write", slog.String("target", target), slog.String("err", err.Error()))
			continue
		}
		keys = append(keys, target)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"keys":  keys,
		"count": len(keys),
	})
}

// Add packs members into a (new or existing) zip archive on the same storage.
//
// If the destination zip exists, we download it, append the new entries,
// then re-upload. Names are zip-slip protected on the read side.
func (a *Archive) Add(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.Path == "" || len(req.Files) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing path or files"})
		return
	}
	storageID, rel, err := a.resolveStorage(r.Context(), req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.StorageID = storageID
	req.Path = rel
	// Source paths in `Files[].Source` may also carry adapter prefixes —
	// strip them and assume same storage as the target archive.
	for i := range req.Files {
		_, srcRel := splitAdapterPath(req.Files[i].Source)
		if srcRel != "" {
			req.Files[i].Source = srcRel
		}
	}
	// RBAC: writing the archive needs ≥editor on the target; each source ≥viewer.
	if !aclAllowID(r.Context(), a.ACL, a.Store, req.StorageID, strings.Trim(req.Path, "/"), acl.LevelEditor) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission"})
		return
	}
	for _, f := range req.Files {
		if !aclAllowID(r.Context(), a.ACL, a.Store, req.StorageID, strings.Trim(f.Source, "/"), acl.LevelViewer) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "insufficient permission: " + f.Source})
			return
		}
	}
	drv, err := a.StorageResolver(req.StorageID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad storage"})
		return
	}
	writer, ok := drv.(storage.Writer)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "storage not writable"})
		return
	}

	// Try to fetch the existing zip — non-fatal if missing (we just create one).
	var existingMembers []*zip.File
	var existingTmp string
	if tmp, err := a.fetchToTemp(r, req.StorageID, req.Path); err == nil {
		existingTmp = tmp
		zr, zerr := zip.OpenReader(tmp)
		if zerr == nil {
			existingMembers = append(existingMembers, zr.File...)
			defer zr.Close()
		} else {
			slog.Warn("archive: existing zip unreadable, overwriting", slog.String("err", zerr.Error()))
		}
	}
	if existingTmp != "" {
		defer os.Remove(existingTmp)
	}

	// New tmp file we'll stream the rebuilt archive into.
	tmp, err := os.CreateTemp("", "filex-zip-add-*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	zw := zip.NewWriter(tmp)
	addedNames := map[string]bool{}
	for _, e := range req.Files {
		safe, err := sanitizeZipPath(e.Name)
		if err != nil {
			continue
		}
		rc, err := drv.Read(r.Context(), e.Source)
		if err != nil {
			slog.Warn("archive: source read", slog.String("source", e.Source), slog.String("err", err.Error()))
			continue
		}
		fw, err := zw.Create(safe)
		if err != nil {
			_ = rc.Close()
			continue
		}
		if _, err := io.Copy(fw, rc); err != nil {
			_ = rc.Close()
			slog.Warn("archive: copy member", slog.String("err", err.Error()))
			continue
		}
		_ = rc.Close()
		addedNames[safe] = true
	}
	for _, f := range existingMembers {
		if addedNames[f.Name] {
			continue // overwrite by name
		}
		fw, err := zw.CreateRaw(&f.FileHeader)
		if err != nil {
			continue
		}
		rc, err := f.OpenRaw()
		if err != nil {
			continue
		}
		_, _ = io.Copy(fw, rc)
	}
	if err := zw.Close(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if _, err := tmp.Seek(0, 0); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	stat, _ := tmp.Stat()
	if err := writer.Write(r.Context(), req.Path, tmp, stat.Size()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = tmp.Close()
	writeJSON(w, http.StatusOK, map[string]any{
		"path": req.Path,
		"size": stat.Size(),
	})
}

// resolveStorage takes the SFC's `path` (which may be either a bare
// relative path or `<adapter>://<rel>`) plus an optional `storage_id`
// and returns the resolved (storage_id, relative_path) pair.
//
// Order of precedence:
//  1. Explicit `storage_id` in the body (legacy embed.js).
//  2. `<adapter>://` prefix on `path`.
//  3. Fall back to storages[0].
func (a *Archive) resolveStorage(ctx context.Context, explicitID int64, fullPath string) (int64, string, error) {
	adapter, rel := splitAdapterPath(fullPath)
	if explicitID > 0 {
		return explicitID, strings.Trim(rel, "/"), nil
	}
	storages, err := a.Store.ListEnabledStorages(ctx)
	if err != nil {
		return 0, "", err
	}
	if len(storages) == 0 {
		return 0, "", errors.New("no storages configured")
	}
	if adapter == "" {
		adapter = storages[0].Name
	}
	for _, s := range storages {
		if s.Name == adapter {
			return s.ID, strings.Trim(rel, "/"), nil
		}
	}
	return 0, "", fmt.Errorf("unknown adapter: %s", adapter)
}

// fetchToTemp pulls a remote object into a local tmp file and returns the path.
func (a *Archive) fetchToTemp(r *http.Request, storageID int64, p string) (string, error) {
	drv, err := a.StorageResolver(storageID)
	if err != nil {
		return "", err
	}
	rc, err := drv.Read(r.Context(), p)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "filex-arc-*.zip")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

// sanitizeZipPath enforces zip-slip protection.
//
// Rules:
//   - Replace backslashes (Windows-authored zips) with forward slashes
//   - Reject absolute paths (drive letters, leading "/")
//   - Reject any path that resolves to "..", "." escapes, or that contains
//     a literal ".." component
//   - Strip leading "./"
//
// Returns the cleaned RELATIVE path or an error.
func sanitizeZipPath(name string) (string, error) {
	if name == "" {
		return "", errors.New("empty entry name")
	}
	clean := strings.ReplaceAll(name, `\`, `/`)
	clean = strings.TrimPrefix(clean, "./")
	clean = strings.TrimLeft(clean, "/")
	if clean == "" {
		return "", errors.New("empty after sanitize")
	}
	// Drive letters (e.g. "C:foo") — uncommon but possible from Windows zips.
	if len(clean) >= 2 && clean[1] == ':' {
		return "", fmt.Errorf("absolute path: %q", name)
	}
	// Component check.
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("parent traversal: %q", name)
		}
	}
	// Final clean — guarantees no "." segments.
	clean = strings.TrimLeft(path.Clean("/"+clean), "/")
	if clean == "" || strings.HasPrefix(clean, "..") {
		return "", fmt.Errorf("clean rejected: %q", name)
	}
	return clean, nil
}
