package share

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Viewer renders the public file at /s/{token} (or the metadata at
// /api/files/share/{token}).
//
// On GET with PIN-protected shares, the controller expects ?pin= in the
// querystring (or the X-Filex-Pin header).
type Viewer struct {
	Service *Service
	Store   db.Store

	// StorageResolver returns a constructed storage.Driver for a given
	// storage ID. Wired by the server.
	StorageResolver func(int64) (storage.Driver, error)
}

// HandleMetadata returns share metadata as JSON — used by the embed.js
// viewer to decide whether to show the PIN input or jump straight to download.
func (v *Viewer) HandleMetadata(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	if tok == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing token"})
		return
	}
	sh, err := v.Service.store.GetShareByToken(r.Context(), tok)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	resp := map[string]any{
		"has_pin":        sh.PinHash != "",
		"expires_at":     sh.ExpiresAt,
		"download_count": sh.DownloadCount,
		"max_downloads":  sh.MaxDownloads,
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleDownload streams the shared file (or returns inline preview).
func (v *Viewer) HandleDownload(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	pin := r.URL.Query().Get("pin")
	if pin == "" {
		pin = r.Header.Get("X-Filex-Pin")
	}
	sh, err := v.Service.Resolve(r.Context(), tok, pin)
	switch {
	case errors.Is(err, ErrExpired):
		writeJSON(w, http.StatusGone, map[string]string{"error": "expired"})
		return
	case errors.Is(err, ErrBadPIN):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad pin"})
		return
	case err != nil:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	node, err := v.Store.GetNode(r.Context(), sh.NodeID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "node missing"})
		return
	}
	drv, err := v.StorageResolver(node.StorageID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "no driver"})
		return
	}
	rc, err := drv.Read(r.Context(), node.Path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read"})
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Disposition", `attachment; filename="`+node.Name+`"`)
	w.Header().Set("Content-Type", "application/octet-stream")
	if node.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(node.Size, 10))
	}
	_, _ = copyToWriter(w, rc)

	_ = v.Service.IncrementDownload(r.Context(), sh.ID)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

// copyToWriter is a tiny io.Copy wrapper that swallows the count.
func copyToWriter(w http.ResponseWriter, r interface {
	Read([]byte) (int, error)
}) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return written, nil
			}
			return written, err
		}
	}
}
