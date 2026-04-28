package handlers

import (
	"archive/zip"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gitlab.com/brftech/filemanager/backend/internal/db"
	"gitlab.com/brftech/filemanager/backend/internal/storage"
)

// Archive handles zip-listing, zip-extract, and zip-create operations.
//
// All zip ops materialize the source archive to a tmp file (since
// archive/zip needs a Seeker), then stream extracts back to the storage.
type Archive struct {
	Store           db.Store
	StorageResolver func(int64) (storage.Driver, error)
}

// NewArchive constructs an Archive handler.
func NewArchive(store db.Store, resolver func(int64) (storage.Driver, error)) *Archive {
	return &Archive{Store: store, StorageResolver: resolver}
}

type archiveRequest struct {
	StorageID int64    `json:"storage_id"`
	Path      string   `json:"path"`
	Members   []string `json:"members,omitempty"`
	DestDir   string   `json:"dest,omitempty"`
}

// List enumerates archive members.
func (a *Archive) List(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
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
	type entry struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		Dir  bool   `json:"dir"`
	}
	out := make([]entry, 0, len(zr.File))
	for _, f := range zr.File {
		out = append(out, entry{
			Name: f.Name,
			Size: int64(f.UncompressedSize64),
			Dir:  strings.HasSuffix(f.Name, "/"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

// Extract pulls members out of an archive into the destination directory.
//
// The DestDir path is interpreted on the SAME storage as the source.
// Members defaults to "all" when empty.
func (a *Archive) Extract(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	tmp, err := a.fetchToTemp(r, req.StorageID, req.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer os.Remove(tmp)

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
	extracted := 0
	for _, f := range zr.File {
		if len(wanted) > 0 && !wanted[f.Name] {
			continue
		}
		// zip-slip protection: ensure the joined path doesn't escape dest.
		clean := filepath.ToSlash(path.Clean("/" + f.Name))
		if strings.Contains(clean, "..") {
			continue
		}
		target := path.Join(dest, clean)
		if strings.HasSuffix(f.Name, "/") {
			if md, ok := drv.(storage.Mkdirer); ok {
				_ = md.Mkdir(r.Context(), target)
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		if err := writer.Write(r.Context(), target, rc, int64(f.UncompressedSize64)); err != nil {
			rc.Close()
			continue
		}
		rc.Close()
		extracted++
	}
	writeJSON(w, http.StatusOK, map[string]any{"extracted": extracted})
}

// Add packs members into a (new or existing) zip archive on the same storage.
//
// V1 simplification: always rewrites the archive from scratch — appending
// to an existing zip would require a multi-pass merge.
func (a *Archive) Add(w http.ResponseWriter, r *http.Request) {
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
		return
	}
	if req.StorageID == 0 || req.Path == "" || len(req.Members) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing fields"})
		return
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
	tmp, err := os.CreateTemp("", "filex-zip-*.zip")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer os.Remove(tmp.Name())
	zw := zip.NewWriter(tmp)
	for _, member := range req.Members {
		rc, err := drv.Read(r.Context(), member)
		if err != nil {
			continue
		}
		fw, err := zw.Create(strings.TrimLeft(member, "/"))
		if err != nil {
			rc.Close()
			continue
		}
		_, _ = io.Copy(fw, rc)
		rc.Close()
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "size": stat.Size()})
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

