package handlers

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/e2e" /* wiring:e2 */
	"github.com/brf-tech/filex/backend/internal/onlyoffice"
)

// fetchOffered serves a file put on offer for one conversion - the office
// engine's input (onlyoffice/offer.go): `o=<token>&exp=<unix>&p=convert&sig=`.
// It is the same door as a document's download, so a reverse proxy that lets
// the document server reach /api/files/onlyoffice/fetch needs nothing new.
func (h *OnlyOffice) fetchOffered(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()
	if !h.Service.EnabledCtx(ctx) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "onlyoffice not configured"})
		return
	}
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad exp"})
		return
	}
	f, name, err := h.Service.OpenOffered(ctx, q.Get("o"), exp, q.Get("p"), q.Get("sig"))
	if err != nil {
		status := http.StatusUnauthorized
		switch {
		case errors.Is(err, onlyoffice.ErrUnknownPurpose):
			status = http.StatusBadRequest
		case errors.Is(err, onlyoffice.ErrOfferGone):
			status = http.StatusNotFound
		}
		slog.Warn("onlyoffice: a conversion download of an offered file was refused", slog.String("err", err.Error()))
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	/* wiring:e2 fxe - no ciphertext to the document server, whatever asked
	   for it: an end-to-end encrypted file's bytes are refused by their
	   first bytes, like a document's purpose download. */
	head := make([]byte, len(e2e.MagicPrefix))
	n, _ := io.ReadFull(f, head)
	if n == len(head) && e2e.HasEncryptedPrefix(head) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "file is e2e-encrypted"})
		return
	}
	if info, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+safeHeaderName(name)+"\"")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, io.MultiReader(bytes.NewReader(head[:n]), f))
}

// safeHeaderName keeps a file name inside a quoted header value.
func safeHeaderName(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 0x20 || c == '"' || c == '\\' || c >= 0x7f {
			c = '_'
		}
		out = append(out, c)
	}
	return string(out)
}
