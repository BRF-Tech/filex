package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
)

// declareBodyLength sets Content-Length from the STORAGE, never from the
// catalogue row.
//
// The row's size is what the last sync saw. When it disagrees with the object
// — a file changed behind filex's back, a catalogue half-repaired after a
// rename, a sync that has not run since the upload — promising the row's
// number makes the response a lie the transport enforces: Go truncates a body
// longer than the declared length and the client sees a short read on one
// shorter. Either way the download fails, and it fails in the recipient's
// program, which reports something like "Download failed" and names nothing.
//
// So the length comes from Source.Stat, which asks the driver (or, for a file
// still in staging, the committed manifest). One extra describe before a full
// transfer is cheap next to the transfer, and it is the only way the number
// can be true.
//
// A storage that cannot describe the object gets no Content-Length at all: a
// chunked response the client reads to EOF is correct, where an unverified
// length is a coin toss.
func declareBodyLength(ctx context.Context, w http.ResponseWriter, src *filebody.Source, node *model.Node) {
	st, err := src.Stat(ctx)
	if err != nil {
		slog.Debug("serving without a declared length: the storage could not describe the object",
			slog.String("err", err.Error()))
		return
	}
	if node != nil && node.Size > 0 && node.Size != st.Size {
		// Worth a line: it means this storage's catalogue is behind, which
		// affects quota totals and search results too, not just this response.
		slog.Warn("the catalogue's size for this file disagrees with the storage",
			slog.Int64("node", node.ID),
			slog.Int64("storage", node.StorageID),
			slog.Int64("catalogue", node.Size),
			slog.Int64("storage_size", st.Size))
	}
	if st.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size, 10))
	}
}
