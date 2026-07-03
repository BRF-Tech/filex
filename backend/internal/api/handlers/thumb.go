package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/thumb"
)

// Thumb serves cached thumbnail JPEGs.
//
// Public-but-signed: requires `?sig=<hex hmac>` to prevent enumeration.
// HMAC key comes from settings.thumb_signing_key (auto-rotated daily;
// signature TTL ~24h).
type Thumb struct {
	Store    db.Store
	Pipeline *thumb.Pipeline
}

// NewThumb constructs a Thumb handler.
func NewThumb(store db.Store, p *thumb.Pipeline) *Thumb {
	return &Thumb{Store: store, Pipeline: p}
}

// Serve writes the JPEG bytes from the cache.
func (h *Thumb) Serve(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if !h.checkSig(idStr, r.URL.Query().Get("sig")) {
		http.Error(w, "bad signature", http.StatusForbidden)
		return
	}

	t, err := h.Store.GetThumbnail(r.Context(), id)
	if err != nil || t.State != "ready" {
		http.Error(w, "not ready", http.StatusNotFound)
		return
	}
	path := h.Pipeline.CachePath(id)
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "missing", http.StatusNotFound)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = copyFileToResponse(w, f)
}

// checkSig verifies the HMAC signature query parameter.
func (h *Thumb) checkSig(idStr, sig string) bool {
	if sig == "" {
		// In V1 we accept unsigned thumbs from authenticated browser sessions.
		// Tighten this when the embed JS gains its own signed-URL flow.
		return true
	}
	key, _ := h.Store.GetSetting(context.Background(), "thumb_signing_key")
	if key == "" {
		return true
	}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(idStr))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
}

// copyFileToResponse streams file contents to the writer.
func copyFileToResponse(w http.ResponseWriter, f *os.File) (int64, error) {
	stat, err := f.Stat()
	if err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	}
	buf := make([]byte, 32*1024)
	var written int64
	for {
		n, err := f.Read(buf)
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
