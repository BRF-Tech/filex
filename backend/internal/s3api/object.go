package s3api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/storage"
)

// Reading an object. GET and HEAD are one function with one flag, because the
// only correct difference between them is the body: a HEAD whose headers
// disagree with the GET that follows it is worse than no HEAD at all, and
// separate code paths are how they drift.

// getObject serves GetObject and HeadObject.
func (h *Handler) getObject(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage, key string, bodyWanted bool) {
	ctx := r.Context()

	set, err := p.ACL(ctx, st)
	if err != nil {
		h.objectError(w, r, bodyWanted, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	// ⚠ NoSuchKey, not AccessDenied. For an object the caller has no grant on,
	// answering "forbidden" confirms it exists — and across tenants that is an
	// existence oracle. S3 itself answers 404 cross-account for this reason.
	if !h.readable(p, set, key) {
		h.objectError(w, r, bodyWanted, http.StatusNotFound, "NoSuchKey", "the specified key does not exist")
		return
	}

	drv, err := h.cfg.Resolver(st.ID)
	if err != nil {
		h.objectError(w, r, bodyWanted, http.StatusInternalServerError, "InternalError", "storage unavailable")
		return
	}

	// ⚠ A key ending in "/" asks about a FOLDER. Clients HEAD the marker right
	// after creating it to confirm the folder is there, and a 404 tells them
	// the mkdir failed — so this answers from the directory itself rather than
	// from a zero-byte object filex never stored.
	if isDirKey(key) {
		h.headDirectoryMarker(w, r, drv, bodyWanted, key)
		return
	}

	src, err := h.cfg.Body.Resolve(ctx, drv, st.ID, key, nil)
	if err != nil {
		h.objectError(w, r, bodyWanted, statusForStorageErr(err), codeForStorageErr(err), "the specified key does not exist")
		return
	}
	stat, err := src.Stat(ctx)
	if err != nil {
		h.objectError(w, r, bodyWanted, statusForStorageErr(err), codeForStorageErr(err), "the specified key does not exist")
		return
	}
	if stat.Kind == storage.KindDirectory {
		// A folder is not an object. S3 has no directories, only prefixes, so
		// the honest answer is that this key does not exist.
		h.objectError(w, r, bodyWanted, http.StatusNotFound, "NoSuchKey", "the specified key does not exist")
		return
	}

	// The backend's ETag when it has one, a computed MD5 when it does not, and
	// nothing at all when it cannot be known — never a synthesised value. See
	// etag.go.
	etag := h.etagFor(ctx, src, stat, st.ID, key)
	if code, ok := preconditionFailed(r, etag, stat.Mtime); !ok {
		h.objectError(w, r, bodyWanted, code, precondCode(code), "the precondition you specified did not hold")
		return
	}

	setObjectHeaders(w, stat, etag)

	if !bodyWanted {
		w.WriteHeader(http.StatusOK)
		return
	}

	// ⚠ No "preparing" 202 here, ever. The manager API answers 202 with a JSON
	// progress body while a slow backend fills the cache; an S3 client cannot
	// read that — it would write the JSON into the file. On this surface a slow
	// read is a slow read.
	rng, err := parseRange(r.Header.Get("Range"), stat.Size)
	switch {
	case errors.Is(err, errUnsatisfiable):
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(stat.Size, 10))
		WriteError(w, r, http.StatusRequestedRangeNotSatisfiable, "InvalidRange", "the requested range is not satisfiable")
		return
	case err != nil:
		// An unparseable Range is ignored, not refused — that is what every
		// HTTP server does, and refusing would break clients that send a
		// header we simply do not understand.
		rng = nil
	}

	if rng != nil && src.CanRange() {
		body, rerr := src.ReadRange(ctx, rng.off, rng.length)
		if rerr != nil {
			h.objectError(w, r, true, statusForStorageErr(rerr), codeForStorageErr(rerr), rerr.Error())
			return
		}
		defer body.Close()
		w.Header().Set("Content-Length", strconv.FormatInt(rng.length, 10))
		w.Header().Set("Content-Range", rng.contentRange(stat.Size))
		w.WriteHeader(http.StatusPartialContent)
		copyBody(w, body, r)
		return
	}

	body, err := src.Open(ctx)
	if err != nil {
		h.objectError(w, r, true, statusForStorageErr(err), codeForStorageErr(err), err.Error())
		return
	}
	defer body.Close()
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size, 10))
	w.WriteHeader(http.StatusOK)
	copyBody(w, body, r)
}

// headDirectoryMarker answers a GET or HEAD of `folder/`.
//
// The answer is a zero-byte object with the content type clients recognise as
// a folder. It is not a fiction: the directory exists, and this is the only
// spelling S3 has for it.
func (h *Handler) headDirectoryMarker(w http.ResponseWriter, r *http.Request, drv storage.Driver, bodyWanted bool, key string) {
	stat, err := drv.Stat(r.Context(), dirOf(key))
	if err != nil || stat.Kind != storage.KindDirectory {
		h.objectError(w, r, bodyWanted, http.StatusNotFound, "NoSuchKey", "the specified key does not exist")
		return
	}
	w.Header().Set("Content-Length", "0")
	w.Header().Set("ETag", `"`+emptyMD5+`"`)
	w.Header().Set("Content-Type", "application/x-directory")
	if !stat.Mtime.IsZero() {
		w.Header().Set("Last-Modified", stat.Mtime.UTC().Format(http.TimeFormat))
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(http.StatusOK)
}

// readable applies the confinement and the grant to one key.
func (h *Handler) readable(p *protocolauth.Principal, set *acl.Set, key string) bool {
	if c := p.Confine; c != nil && c.Rel != "" {
		if key != c.Rel && !strings.HasPrefix(key, c.Rel+"/") {
			return false
		}
	}
	return set.Effective(key) >= acl.LevelViewer
}

func setObjectHeaders(w http.ResponseWriter, stat storage.Object, etag string) {
	// ⚠⚠ Content-Length, on HEAD as well as GET. A HEAD carries no body, so Go
	// answers `Content-Length: 0` unless the handler sets one — and the size is
	// the whole reason a client sends a HEAD. Without it rclone uploaded a file
	// correctly, read back "0", called the transfer corrupted and DELETED its
	// own upload (2026-08-16, found by the real-client E2E: the unit suite
	// compared HEAD and GET headers but had left this one out of the list).
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size, 10))
	if etag != "" {
		w.Header().Set("ETag", etag)
	}
	if stat.Mime != "" {
		w.Header().Set("Content-Type", stat.Mime)
	}
	if !stat.Mtime.IsZero() {
		w.Header().Set("Last-Modified", stat.Mtime.UTC().Format(http.TimeFormat))
	}
	// Every S3 client checks this before trying a ranged read; without it,
	// restic and rclone fall back to whole-object downloads.
	w.Header().Set("Accept-Ranges", "bytes")
}

// objectError writes an error, or just the status when the caller asked for a
// HEAD — a HEAD response carries no body, so the XML would be dropped on the
// floor and the status is all a client can read.
func (h *Handler) objectError(w http.ResponseWriter, r *http.Request, bodyWanted bool, status int, code, msg string) {
	if !bodyWanted {
		w.Header().Set("x-amz-request-id", requestID(r))
		w.WriteHeader(status)
		return
	}
	WriteError(w, r, status, code, msg)
}

func copyBody(w http.ResponseWriter, body io.Reader, r *http.Request) {
	if _, err := io.Copy(w, body); err != nil {
		// The status line is already written, so there is nothing to report to
		// the client: a truncated body is what it will see. Log it so an
		// operator can tell a disconnect from a backend failure.
		slog.Debug("s3api: body copy interrupted",
			slog.String("path", r.URL.Path), slog.Any("err", err))
	}
}

// ───────────────────────────── ranges ─────────────────────────────

var errUnsatisfiable = errors.New("s3api: unsatisfiable range")

type byteRange struct {
	off    int64
	length int64
}

func (b byteRange) contentRange(total int64) string {
	last := b.off + b.length - 1
	return "bytes " + strconv.FormatInt(b.off, 10) + "-" + strconv.FormatInt(last, 10) + "/" + strconv.FormatInt(total, 10)
}

// parseRange handles the single-range forms S3 clients actually send.
//
// ⚠ Multi-range (`bytes=0-10,20-30`) is deliberately NOT supported: it needs a
// multipart/byteranges body, no S3 client sends it, and answering it wrong
// hands back bytes from the wrong offsets. Returning nil (whole object) for a
// header we do not understand is the safe direction.
func parseRange(header string, size int64) (*byteRange, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return nil, nil
	}
	if !strings.HasPrefix(header, "bytes=") {
		return nil, errors.New("unsupported range unit")
	}
	spec := strings.TrimPrefix(header, "bytes=")
	if strings.Contains(spec, ",") {
		return nil, errors.New("multi-range not supported")
	}
	startStr, endStr, ok := strings.Cut(spec, "-")
	if !ok {
		return nil, errors.New("malformed range")
	}

	// A suffix range: the last N bytes.
	if startStr == "" {
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n <= 0 {
			return nil, errors.New("malformed suffix range")
		}
		if n > size {
			n = size
		}
		if size == 0 {
			return nil, errUnsatisfiable
		}
		return &byteRange{off: size - n, length: n}, nil
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return nil, errors.New("malformed range start")
	}
	if start >= size {
		// ⚠ Past the end is 416, not an empty 206. A client that gets 206 here
		// concludes the object shrank and, in restic's case, calls the
		// repository damaged.
		return nil, errUnsatisfiable
	}
	end := size - 1
	if endStr != "" {
		end, err = strconv.ParseInt(endStr, 10, 64)
		if err != nil || end < start {
			return nil, errors.New("malformed range end")
		}
		if end > size-1 {
			end = size - 1
		}
	}
	return &byteRange{off: start, length: end - start + 1}, nil
}

// ─────────────────────────── preconditions ───────────────────────────

// preconditionFailed evaluates If-Match / If-None-Match / If-Modified-Since /
// If-Unmodified-Since, in the precedence RFC 9110 gives them.
func preconditionFailed(r *http.Request, etag string, mtime time.Time) (int, bool) {
	if v := r.Header.Get("If-Match"); v != "" {
		if !etagMatches(v, etag) {
			return http.StatusPreconditionFailed, false
		}
	} else if v := r.Header.Get("If-Unmodified-Since"); v != "" {
		if t, err := http.ParseTime(v); err == nil && mtime.After(t.Add(time.Second)) {
			return http.StatusPreconditionFailed, false
		}
	}
	if v := r.Header.Get("If-None-Match"); v != "" {
		if etagMatches(v, etag) {
			return http.StatusNotModified, false
		}
	} else if v := r.Header.Get("If-Modified-Since"); v != "" {
		if t, err := http.ParseTime(v); err == nil && !mtime.After(t.Add(time.Second)) {
			return http.StatusNotModified, false
		}
	}
	return 0, true
}

func etagMatches(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return etag != ""
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/")
		if part == etag {
			return true
		}
	}
	return false
}

func precondCode(status int) string {
	if status == http.StatusNotModified {
		return "NotModified"
	}
	return "PreconditionFailed"
}

func statusForStorageErr(err error) int {
	if errors.Is(err, storage.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func codeForStorageErr(err error) string {
	if errors.Is(err, storage.ErrNotFound) {
		return "NoSuchKey"
	}
	return "InternalError"
}
