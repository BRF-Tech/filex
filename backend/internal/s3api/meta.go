package s3api

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// User metadata — the `x-amz-meta-*` headers — and the one entry in it that
// changes what a file IS rather than what it is called.
//
// ⚠⚠ `x-amz-meta-mtime` is how every S3 client carries a file's own timestamp:
// rclone, s3fs and restic's cache all write it, and rclone reads it back to
// decide whether a file needs copying again. A server that ignores it stamps
// every synced file with the moment it arrived, so the next run sees a modified
// file and copies the lot again — for ever. Worse, rclone then tries to CORRECT
// the timestamp with a copy-onto-self, which is the request that failed the
// first real-client run of this endpoint (2026-08-16: "Failed to set
// modification time … the source and destination are the same object").

// amzMetaMtime is the header clients agree on. The value is seconds since the
// epoch, integral (s3fs) or fractional (rclone).
const amzMetaMtime = "X-Amz-Meta-Mtime"

// metadataDirective is REPLACE when a copy carries new metadata instead of
// inheriting the source's.
const metadataDirective = "X-Amz-Metadata-Directive"

// replaceRequested reports whether the caller asked for the metadata to be
// replaced rather than copied.
func replaceRequested(h http.Header) bool {
	return strings.EqualFold(strings.TrimSpace(h.Get(metadataDirective)), "REPLACE")
}

// parseAmzMtime reads x-amz-meta-mtime.
//
// ⚠ Both spellings are real and mean the same instant: rclone sends
// `1786891763.9357628`, s3fs sends `1786891763`. Parsing only the integer form
// drops the header from rclone entirely, which is the client this exists for.
func parseAmzMtime(h http.Header) (time.Time, bool) {
	raw := strings.TrimSpace(h.Get(amzMetaMtime))
	if raw == "" {
		return time.Time{}, false
	}
	secs, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(secs) || math.IsInf(secs, 0) {
		return time.Time{}, false
	}
	// A timestamp before the epoch or far in the future is a client bug, and
	// writing it onto a file makes the file sort strangely for ever. Ignore it
	// rather than persist it — the bytes are still correct.
	if secs <= 0 || secs > 4102444800 { // 2100-01-01
		return time.Time{}, false
	}
	whole, frac := math.Modf(secs)
	return time.Unix(int64(whole), int64(frac*float64(time.Second))).UTC(), true
}

// applyMtime carries a client-supplied modification time onto the stored file.
//
// It returns the time it applied, or the zero time when there was nothing to
// apply or the backend cannot hold one.
//
// ⚠ A driver that cannot set a modification time (an object store has no such
// operation) is NOT an error: the object was written correctly and the caller
// asked for something the backend does not have. Failing the whole PUT over it
// would refuse a good upload; the visible cost is that the next sync run copies
// the file again, which is a slow surface rather than a wrong one.
func (h *Handler) applyMtime(ctx context.Context, drv storage.Driver, st *model.Storage, key string, hdr http.Header) time.Time {
	mtime, ok := parseAmzMtime(hdr)
	if !ok {
		return time.Time{}
	}
	toucher, ok := drv.(storage.Toucher)
	if !ok {
		slog.Debug("s3api: backend cannot carry a modification time",
			slog.String("driver", drv.Name()), slog.String("key", key))
		return time.Time{}
	}
	if err := toucher.SetMtime(ctx, key, mtime); err != nil {
		slog.Debug("s3api: set mtime", slog.String("key", key), slog.Any("err", err))
		return time.Time{}
	}
	h.sync().Touch(ctx, st, key, mtime)
	return mtime
}
