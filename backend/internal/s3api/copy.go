package s3api

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// CopyObject: how a client renames, moves, or duplicates without moving bytes
// through itself. rclone's server-side copy, `mc mv`, and every "rename" in a
// GUI client land here.

// CopyObjectResult is the response body.
//
// ⚠ The result is XML in the BODY of a 200, not a status — including when the
// copy fails. S3 does that because the connection is already open and streaming
// for a long copy; a client that only checks the status code will read a
// failure as success. filex's copies are short enough that this cannot bite in
// practice, but the shape is what clients parse, so it is the shape we send.
type CopyObjectResult struct {
	XMLName      xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ CopyObjectResult"`
	ETag         string   `xml:"ETag"`
	LastModified string   `xml:"LastModified"`
}

// copySource is a parsed x-amz-copy-source header.
type copySource struct {
	Bucket string
	Key    string
}

// parseCopySource reads `/bucket/key` or `bucket/key`, URL-decoded.
//
// ⚠ The value is percent-encoded, and decoding it is not optional: a key with
// a space or a Turkish character arrives escaped, and copying to the literal
// escaped name creates a second file whose name nobody can type.
func parseCopySource(v string) (copySource, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return copySource{}, false
	}
	// A versionId suffix is not supported (filex has no S3 versioning API);
	// dropping it silently would copy the wrong version, so refuse instead.
	if i := strings.Index(v, "?"); i >= 0 {
		return copySource{}, false
	}
	v = strings.TrimPrefix(v, "/")
	bucket, key, ok := strings.Cut(v, "/")
	if !ok || bucket == "" || key == "" {
		return copySource{}, false
	}
	db, err := url.PathUnescape(bucket)
	if err != nil {
		return copySource{}, false
	}
	dk, err := url.PathUnescape(key)
	if err != nil {
		return copySource{}, false
	}
	return copySource{Bucket: db, Key: dk}, true
}

// copyObject duplicates an object into this key.
func (h *Handler) copyObject(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, dstSt *model.Storage, dstKey, rawSource string) {
	ctx := r.Context()
	src, ok := parseCopySource(rawSource)
	if !ok {
		WriteError(w, r, http.StatusBadRequest, "InvalidArgument", "x-amz-copy-source is malformed or names a version")
		return
	}

	// ── the destination must be writable ──
	if dstKey == "" || strings.HasSuffix(dstKey, "/") {
		WriteError(w, r, http.StatusBadRequest, "InvalidRequest", "a key may not be empty or end in a slash")
		return
	}
	if dstSt.ReadOnly {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "this bucket is read-only")
		return
	}
	dstSet, err := p.ACL(ctx, dstSt)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if !h.writable(p, dstSet, dstKey) {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "you do not have write access to the destination")
		return
	}

	// ── the source must be readable ──
	//
	// ⚠ Both ends are checked, with their OWN rules: a copy is a read of one
	// object and a write of another, and checking only the destination would
	// turn CopyObject into a way to read anything the caller can write over.
	srcSt, code, ok := h.resolveBucket(ctx, p, src.Bucket)
	if !ok {
		WriteError(w, r, http.StatusNotFound, code, "the source bucket does not exist")
		return
	}
	srcSet, err := p.ACL(ctx, srcSt)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if !h.readable(p, srcSet, src.Key) {
		// NoSuchKey on the read side, for the same reason GetObject uses it.
		WriteError(w, r, http.StatusNotFound, "NoSuchKey", "the source key does not exist")
		return
	}

	srcDrv, err := h.cfg.Resolver(srcSt.ID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", "source storage unavailable")
		return
	}
	dstDrv, err := h.cfg.Resolver(dstSt.ID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", "destination storage unavailable")
		return
	}

	stat, err := srcDrv.Stat(ctx, src.Key)
	if err != nil {
		WriteError(w, r, statusForStorageErr(err), codeForStorageErr(err), "the source key does not exist")
		return
	}
	if stat.Kind == storage.KindDirectory {
		WriteError(w, r, http.StatusNotFound, "NoSuchKey", "the source key does not exist")
		return
	}
	if srcSt.ID == dstSt.ID && src.Key == dstKey {
		// ⚠⚠ A copy onto itself is how an S3 client CHANGES METADATA — there is
		// no other request for it. S3 refuses it unless the directive says
		// REPLACE, and allows it when it does; rclone uses exactly this to set a
		// file's modification time after an upload, so refusing it outright made
		// `rclone sync` fail on every file it had just transferred correctly
		// ("Failed to set modification time", 2026-08-16).
		if !replaceRequested(r.Header) {
			WriteError(w, r, http.StatusBadRequest, "InvalidRequest",
				"the source and destination are the same object")
			return
		}
		sctx := context.WithoutCancel(ctx)
		if applied := h.applyMtime(sctx, dstDrv, dstSt, dstKey, r.Header); !applied.IsZero() {
			// The digest is keyed by size and mtime, so moving the timestamp
			// orphans it. Carrying the entry across saves re-reading an object
			// whose bytes provably did not change.
			if known, ok := h.etags.get(etagKey(dstSt.ID, dstKey, stat.Size, stat.Mtime)); ok {
				h.etags.put(etagKey(dstSt.ID, dstKey, stat.Size, applied), known)
			}
			stat.Mtime = applied
		}
		WriteXML(w, r, http.StatusOK, CopyObjectResult{
			ETag:         h.etagFor(ctx, nil, stat, dstSt.ID, dstKey),
			LastModified: amzTime(stat.Mtime),
		})
		return
	}

	if u := auth.UserFrom(ctx); u != nil && h.cfg.Quota != nil {
		// A copy is a second physical object: it costs quota even though the
		// caller uploaded nothing.
		if err := h.cfg.Quota.CheckCanWrite(ctx, u.ID, stat.Size); err != nil {
			WriteError(w, r, http.StatusRequestEntityTooLarge, "EntityTooLarge", err.Error())
			return
		}
	}

	etag, err := h.performCopy(ctx, srcSt, dstSt, srcDrv, dstDrv, src.Key, dstKey, stat)
	if err != nil {
		var refused *overwriteRefusedError
		if errors.As(err, &refused) {
			// The S3 surface's own way of saying "refused, not failed": the
			// write never happened. statusForStorageErr/codeForWriteErr do not
			// recognise this error -- it is neither ErrNotFound, ErrReadOnly
			// nor ErrUnsupported -- and would default it to a bare 500.
			WriteError(w, r, http.StatusServiceUnavailable, "ServiceUnavailable",
				"could not preserve the existing object: "+refused.err.Error())
			return
		}
		WriteError(w, r, statusForStorageErr(err), codeForWriteErr(err), err.Error())
		return
	}

	sctx := context.WithoutCancel(ctx)
	h.sync().Write(sctx, dstSt, dstKey, stat.Size, stat.Mime)
	// A copy carrying REPLACE metadata sets the new object's timestamp too —
	// same rule as a PUT, so a client cannot get a different answer depending on
	// which verb it used to put the bytes there.
	if replaceRequested(r.Header) {
		if applied := h.applyMtime(sctx, dstDrv, dstSt, dstKey, r.Header); !applied.IsZero() {
			stat.Mtime = applied
		}
	}
	if s2, serr := dstDrv.Stat(sctx, dstKey); serr == nil && etag != "" {
		h.etags.put(etagKey(dstSt.ID, dstKey, s2.Size, s2.Mtime), etag)
	}

	WriteXML(w, r, http.StatusOK, CopyObjectResult{
		ETag:         quoteETag(etag),
		LastModified: amzTime(stat.Mtime),
	})
}

// performCopy uses the driver's own Copy when it has one, and streams
// otherwise.
//
// ⚠ The driver path is the point of the whole operation: a server-side copy on
// S3 or a hard-link-style copy on a filesystem never moves the bytes through
// this process. Falling back to a stream is correct but is NOT the same thing,
// and on a 4 GB object the difference is minutes of transfer.
// overwriteRefusedError marks a writehook.BeforeOverwrite refusal from
// performCopy so copyObject can answer with the S3 surface's own idiom instead
// of falling through the generic storage-error mapping.
type overwriteRefusedError struct{ err error }

func (e *overwriteRefusedError) Error() string { return e.err.Error() }
func (e *overwriteRefusedError) Unwrap() error { return e.err }

func (h *Handler) performCopy(ctx context.Context, srcSt, dstSt *model.Storage, srcDrv, dstDrv storage.Driver, srcKey, dstKey string, stat storage.Object) (string, error) {
	// The last moment at which the bytes this copy is about to replace still
	// exist -- see writehook/overwrite.go. One call here covers BOTH branches
	// below: the driver Copier fast path and the stream fallback are equally a
	// destructive write onto dstKey, and server-side CopyObject (rclone copy,
	// every GUI rename) was unguarded at any size.
	if err := writehook.BeforeOverwrite(ctx, dstSt.ID, dstKey); err != nil {
		return "", &overwriteRefusedError{err}
	}
	if srcSt.ID == dstSt.ID {
		if c, ok := srcDrv.(storage.Copier); ok {
			if err := c.Copy(ctx, srcKey, dstKey); err != nil {
				return "", err
			}
			// The driver moved the bytes without showing them to us, so the
			// digest is unknown here. Leave it to the read path, which computes
			// one on demand — reporting a guess would be the wrong-ETag failure
			// etag.go exists to avoid.
			return "", nil
		}
	}

	writer, ok := dstDrv.(storage.Writer)
	if !ok {
		return "", storage.ErrUnsupported
	}
	body, err := srcDrv.Read(ctx, srcKey)
	if err != nil {
		return "", err
	}
	defer body.Close()

	digest := newMD5()
	if err := writer.Write(ctx, dstKey, io.TeeReader(body, digest), stat.Size); err != nil {
		return "", err
	}
	return hexOf(digest), nil
}
