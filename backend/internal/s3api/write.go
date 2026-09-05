package s3api

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Writing and deleting objects.
//
// The bytes are the easy half. What makes a write correct is everything that
// has to happen around it — the node row, the parent chain, the search index,
// the thumbnail, the quota and the write hook — and all of that lives in
// internal/protocolsync, shared with /dav and with every protocol still to
// come. Reimplementing it here is the mistake this package is written to
// avoid.

// putObject stores an object.
func (h *Handler) putObject(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage, key string) {
	ctx := r.Context()
	if key == "" {
		WriteError(w, r, http.StatusBadRequest, "InvalidRequest", "a key may not be empty")
		return
	}
	if isDirKey(key) {
		// ⚠⚠ A key ending in "/" is a DIRECTORY MARKER — how every S3 client
		// that presents folders creates one, because S3 itself has no
		// directories. Refusing it means `mkdir` fails over the endpoint: s3fs
		// reported "Input/output error" and no folder could be created at all
		// (2026-08-16). Creating a FILE with a trailing separator would be
		// worse — every other filex surface shows that as a broken entry — so
		// the marker maps onto the thing filex actually has: a directory.
		h.putDirectoryMarker(w, r, p, st, key)
		return
	}
	if st.ReadOnly {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "this bucket is read-only")
		return
	}

	set, err := p.ACL(ctx, st)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	// ⚠ AccessDenied here, unlike a read. A write that cannot happen must SAY
	// so: answering NoSuchKey would make a client retry forever against a
	// bucket it can see. The existence-oracle argument does not apply — the
	// caller already proved it can see this bucket to get here.
	if !h.writable(p, set, key) {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "you do not have write access to this key")
		return
	}

	drv, err := h.cfg.Resolver(st.ID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", "storage unavailable")
		return
	}
	writer, ok := drv.(storage.Writer)
	if !ok {
		// A backend that cannot write says so with the right code rather than
		// failing in a way a client reads as a transient error and retries.
		WriteError(w, r, http.StatusNotImplemented, "NotImplemented", "this storage does not support writes")
		return
	}

	// ⚠⚠ aws-chunked framing. When a client signs the payload as it streams
	// the BODY IS NOT THE OBJECT: it is the object cut into length-prefixed,
	// individually signed chunks. Writing it through verbatim would store the
	// framing inside the file — silent corruption. See chunked.go.
	body, size, cerr := h.requestBody(r)
	if cerr != nil {
		status, code := chunkedError(cerr)
		WriteError(w, r, status, code, cerr.Error())
		return
	}

	// The per-user ceiling, checked BEFORE the bytes land. Checking afterwards
	// means the disk already holds what the quota was meant to prevent.
	if u := auth.UserFrom(ctx); u != nil && h.cfg.Quota != nil {
		if err := h.cfg.Quota.CheckCanWrite(ctx, u.ID, size); err != nil {
			WriteError(w, r, http.StatusRequestEntityTooLarge, "EntityTooLarge", err.Error())
			return
		}
	}

	// The digest is computed as the bytes go past, so the ETag this returns is
	// the real MD5 and the read path never has to re-read the object to learn
	// it (see etag.go for why a synthesised value is not an option).
	digest := md5.New()
	sums := newChecksumSet(r.Header)
	hashed := sums.Wrap(io.TeeReader(body, digest))

	// The last moment at which the bytes this PUT is about to replace still
	// exist -- see writehook/overwrite.go. Single-part only: anything above a
	// client's multipart threshold lands in completeMultipartUpload instead,
	// which carries its own guard.
	if err := writehook.BeforeOverwrite(ctx, st.ID, key); err != nil {
		WriteError(w, r, http.StatusServiceUnavailable, "ServiceUnavailable",
			"could not preserve the existing object: "+err.Error())
		return
	}
	if err := writer.Write(ctx, key, hashed, size); err != nil {
		WriteError(w, r, statusForStorageErr(err), codeForWriteErr(err), err.Error())
		return
	}
	sum := hex.EncodeToString(digest.Sum(nil))

	// ⚠⚠ The integrity check the client asked for, before anything downstream
	// believes the object. On a mismatch the bytes are removed rather than
	// left: the object under this key is provably not what the caller sent, and
	// keeping it would publish corruption under a name somebody trusts. (The
	// overwrite is already destructive by then — every S3 PUT is — so the
	// choice is between corrupt bytes and no bytes, and no bytes is the one a
	// client can act on.)
	if err := sums.Verify(trailersOf(body)); err != nil {
		if del, ok := drv.(storage.Deleter); ok {
			_ = del.Delete(context.WithoutCancel(ctx), key)
		}
		status, code := checksumErrorCode(err)
		WriteError(w, r, status, code, err.Error())
		return
	}

	// ⚠ Bookkeeping on a WithoutCancel context: the bytes are already on the
	// driver, so a client that hangs up now must not leave the node row
	// missing — the file would be invisible in the UI until the next sync run.
	sctx := context.WithoutCancel(ctx)
	h.sync().Write(sctx, st, key, size, mimeFor(r, key))
	// The client's own timestamp, if it sent one — applied AFTER the write, or
	// the write would stamp it back to now. See meta.go for why this decides
	// whether a sync ever settles.
	h.applyMtime(sctx, drv, st, key, r.Header)
	if stat, serr := drv.Stat(sctx, key); serr == nil {
		h.etags.put(etagKey(st.ID, key, stat.Size, stat.Mtime), sum)
	}

	w.Header().Set("ETag", `"`+sum+`"`)
	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(http.StatusOK)
}

// emptyMD5 is the ETag of zero bytes. A directory marker is a zero-byte
// object, and clients compare the value they get back.
const emptyMD5 = "d41d8cd98f00b204e9800998ecf8427e"

// isDirKey reports whether a key names a directory marker.
func isDirKey(key string) bool { return strings.HasSuffix(key, "/") }

// dirOf strips the marker's trailing slash.
func dirOf(key string) string { return strings.TrimSuffix(key, "/") }

// putDirectoryMarker creates the folder a client is asking for.
func (h *Handler) putDirectoryMarker(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage, key string) {
	ctx := r.Context()
	dir := dirOf(key)
	if dir == "" {
		WriteError(w, r, http.StatusBadRequest, "InvalidRequest", "a key may not be empty")
		return
	}
	// ⚠ A marker carries no bytes. Accepting a body here would silently throw
	// it away, so a client that meant to upload a file to a path ending in "/"
	// is told rather than left to discover an empty folder later.
	if r.ContentLength > 0 {
		WriteError(w, r, http.StatusBadRequest, "InvalidRequest",
			"a key ending in a slash names a folder and cannot carry content")
		return
	}
	if st.ReadOnly {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "this bucket is read-only")
		return
	}
	set, err := p.ACL(ctx, st)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if !h.writable(p, set, dir) {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "you do not have write access to this key")
		return
	}
	drv, err := h.cfg.Resolver(st.ID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", "storage unavailable")
		return
	}
	mk, ok := drv.(storage.Mkdirer)
	if !ok {
		WriteError(w, r, http.StatusNotImplemented, "NotImplemented", "this storage cannot create folders")
		return
	}
	if err := mk.Mkdir(ctx, dir); err != nil {
		WriteError(w, r, statusForStorageErr(err), codeForWriteErr(err), err.Error())
		return
	}
	h.sync().Mkdir(context.WithoutCancel(ctx), st, dir)

	w.Header().Set("ETag", `"`+emptyMD5+`"`)
	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(http.StatusOK)
}

// deleteObject moves an object to the trash.
//
// ⚠ To the TRASH, not to oblivion — the owner's decision, and the same rule on
// every protocol (2026-08-15). S3 clients delete in bulk, so this is also the
// path that fills the trash fastest; the retention policy and its sweeper are
// what keep that bounded.
func (h *Handler) deleteObject(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage, key string) {
	ctx := r.Context()
	// A client removing a folder deletes its marker. The slash is the client's
	// spelling of the same path, so it is dropped and the folder itself is
	// trashed — the alternative is a "file" named `foo/` that no other surface
	// can see.
	key = strings.TrimSuffix(key, "/")
	if key == "" {
		WriteError(w, r, http.StatusBadRequest, "InvalidRequest", "a key may not be empty")
		return
	}
	if st.ReadOnly {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "this bucket is read-only")
		return
	}
	set, err := p.ACL(ctx, st)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	if !h.writable(p, set, key) {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "you do not have write access to this key")
		return
	}

	drv, err := h.cfg.Resolver(st.ID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", "storage unavailable")
		return
	}

	out, terr := trash.Put(ctx, drv, key)
	sctx := context.WithoutCancel(ctx)
	switch {
	case terr == nil && out.Trashed:
		h.sync().Trash(sctx, st, key, out.Key)
	case terr == nil && out.Missing:
		// ⚠ Deleting something that is not there is a SUCCESS in S3, not a
		// 404. Clients delete optimistically and treat an error as a failed
		// operation to retry; answering 404 turns a no-op into a retry loop.
		h.sync().Delete(sctx, st, key)
	case errors.Is(terr, trash.ErrUnsupported):
		del, ok := drv.(storage.Deleter)
		if !ok {
			WriteError(w, r, http.StatusNotImplemented, "NotImplemented", "this storage cannot delete")
			return
		}
		if derr := del.Delete(ctx, key); derr != nil && !errors.Is(derr, storage.ErrNotFound) {
			WriteError(w, r, statusForStorageErr(derr), codeForWriteErr(derr), derr.Error())
			return
		}
		// The bytes are gone for good, so the rows go too rather than sitting
		// in the trash behind a Restore that could never work.
		h.sync().Delete(sctx, st, key)
	default:
		WriteError(w, r, statusForStorageErr(terr), codeForWriteErr(terr), terr.Error())
		return
	}

	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(http.StatusNoContent)
}

// writable applies the confinement and the grant for a mutation.
func (h *Handler) writable(p *protocolauth.Principal, set *acl.Set, key string) bool {
	// Internal trees are not writable through the gateway either: a caller who
	// could PUT over .versions/42/1 could destroy the very history the
	// overwrite guard exists to keep, and the guard skips internal paths so
	// nothing would snapshot it first.
	if hiddenPath(key) {
		return false
	}
	if c := p.Confine; c != nil && c.Rel != "" {
		if key != c.Rel && !strings.HasPrefix(key, c.Rel+"/") {
			return false
		}
	}
	return set.Effective(key) >= acl.LevelEditor
}

// mimeFor picks the content type to record: what the client declared, else a
// guess from the extension. The declared value wins because the client knows
// what it uploaded and the extension is only a hint.
func mimeFor(r *http.Request, key string) string {
	if ct := strings.TrimSpace(r.Header.Get("Content-Type")); ct != "" && ct != "application/octet-stream" {
		return ct
	}
	if ext := path.Ext(key); ext != "" {
		if ct := mime.TypeByExtension(ext); ct != "" {
			return ct
		}
	}
	return "application/octet-stream"
}

func codeForWriteErr(err error) string {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return "NoSuchKey"
	case errors.Is(err, storage.ErrReadOnly):
		return "AccessDenied"
	case errors.Is(err, storage.ErrUnsupported):
		return "NotImplemented"
	default:
		return "InternalError"
	}
}

// ─────────────────────────── bulk delete ───────────────────────────

// DeleteObjectsRequest is the body of a bulk delete (POST /bucket?delete).
type DeleteObjectsRequest struct {
	XMLName xml.Name `xml:"Delete"`
	// Quiet suppresses the per-key success entries, which is what rclone and
	// mc ask for — a thousand <Deleted> elements per batch is a lot of XML
	// nobody reads.
	Quiet   bool                 `xml:"Quiet"`
	Objects []DeleteObjectsEntry `xml:"Object"`
}

// DeleteObjectsEntry is one key to delete.
type DeleteObjectsEntry struct {
	Key string `xml:"Key"`
}

// DeleteResult is the response.
type DeleteResult struct {
	XMLName xml.Name           `xml:"http://s3.amazonaws.com/doc/2006-03-01/ DeleteResult"`
	Deleted []DeletedEntry     `xml:"Deleted,omitempty"`
	Errors  []DeleteErrorEntry `xml:"Error,omitempty"`
}

// DeletedEntry reports one success.
type DeletedEntry struct {
	Key string `xml:"Key"`
}

// DeleteErrorEntry reports one failure.
type DeleteErrorEntry struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

// maxBulkDelete is S3's own per-request ceiling. Beyond it a client is told to
// split the batch rather than being served a request that could tie up the
// handler for minutes.
const maxBulkDelete = 1000

// deleteObjects handles a bulk delete.
//
// ⚠ The response is 200 with PER-KEY results, not a single status: a batch
// where nine keys succeed and one fails is neither a success nor a failure,
// and collapsing it either way loses what the client needs to retry. This is
// the one place in the protocol where partial failure is normal.
func (h *Handler) deleteObjects(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage) {
	ctx := r.Context()
	if st.ReadOnly {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "this bucket is read-only")
		return
	}

	var req DeleteObjectsRequest
	body := http.MaxBytesReader(w, r.Body, 8<<20)
	if err := xml.NewDecoder(body).Decode(&req); err != nil {
		WriteError(w, r, http.StatusBadRequest, "MalformedXML", "the delete request body could not be parsed")
		return
	}
	if len(req.Objects) == 0 {
		WriteError(w, r, http.StatusBadRequest, "MalformedXML", "no keys in the delete request")
		return
	}
	if len(req.Objects) > maxBulkDelete {
		WriteError(w, r, http.StatusBadRequest, "MalformedXML", "too many keys in one request")
		return
	}

	set, err := p.ACL(ctx, st)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	drv, err := h.cfg.Resolver(st.ID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", "storage unavailable")
		return
	}

	out := DeleteResult{}
	sctx := context.WithoutCancel(ctx)
	for _, o := range req.Objects {
		key := strings.TrimPrefix(o.Key, "/")
		if key == "" {
			out.Errors = append(out.Errors, DeleteErrorEntry{Key: o.Key, Code: "InvalidRequest", Message: "empty key"})
			continue
		}
		if !h.writable(p, set, key) {
			out.Errors = append(out.Errors, DeleteErrorEntry{Key: o.Key, Code: "AccessDenied", Message: "no write access to this key"})
			continue
		}
		res, terr := trash.Put(ctx, drv, key)
		switch {
		case terr == nil && res.Trashed:
			h.sync().Trash(sctx, st, key, res.Key)
		case terr == nil && res.Missing:
			// Already gone counts as deleted — see deleteObject.
			h.sync().Delete(sctx, st, key)
		default:
			out.Errors = append(out.Errors, DeleteErrorEntry{
				Key: o.Key, Code: codeForWriteErr(terr), Message: errString(terr),
			})
			continue
		}
		if !req.Quiet {
			out.Deleted = append(out.Deleted, DeletedEntry{Key: o.Key})
		}
	}
	WriteXML(w, r, http.StatusOK, out)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// requestBody returns the object's real bytes and its real size, unwrapping
// aws-chunked framing when the client used it.
//
// ⚠ Two lengths are in play and confusing them is a quiet bug: Content-Length
// describes the FRAMED body, x-amz-decoded-content-length the object. Handing
// the first to the driver records the framing overhead as part of the file.
func (h *Handler) requestBody(r *http.Request) (io.Reader, int64, error) {
	payloadHash := r.Header.Get("X-Amz-Content-Sha256")
	if !IsChunked(payloadHash) {
		if r.ContentLength < 0 {
			// The driver contract wants a size, and guessing truncates the
			// object. Refuse rather than write a partial file that looks whole.
			return nil, 0, errNoContentLength
		}
		return r.Body, r.ContentLength, nil
	}
	if !chunkedSupported(payloadHash) {
		return nil, 0, fmt.Errorf("%w: %s", errUnsupportedStreaming, payloadHash)
	}

	size, err := decodedLength(r.Header.Get("X-Amz-Decoded-Content-Length"))
	if err != nil {
		return nil, 0, err
	}

	sr, err := Parse(r)
	if err != nil {
		return nil, 0, ErrChunkFraming
	}
	verify := payloadHash == streamingSigned || payloadHash == streamingSignedTrailer
	secret := ""
	if verify {
		// The chunk chain is keyed by the same secret the request was signed
		// with, and the request has already been verified by the time a
		// handler runs — so looking it up again is a lookup, not a second
		// authentication.
		if _, s, lerr := h.cfg.Auth.AccessKey(r.Context(), sr.AccessKeyID); lerr == nil {
			secret = s
		} else {
			return nil, 0, ErrChunkSignature
		}
	}
	return newChunkedReader(r.Body, sr, secret, sr.Signature, verify), size, nil
}

// trailersOf returns the trailing headers of a chunked body, or nil.
//
// ⚠ Only meaningful once the body has been read to EOF — the trailer is at the
// end, which is the whole reason a client uses one.
func trailersOf(body io.Reader) map[string]string {
	if cr, ok := body.(*chunkedReader); ok {
		return cr.Trailers()
	}
	return nil
}

var (
	errNoContentLength      = errors.New("Content-Length is required")
	errUnsupportedStreaming = errors.New("this streaming payload variant is not supported")
)

// chunkedError maps a body-decoding failure onto an S3 code.
func chunkedError(err error) (int, string) {
	switch {
	case errors.Is(err, errNoContentLength):
		return http.StatusLengthRequired, "MissingContentLength"
	case errors.Is(err, errUnsupportedStreaming):
		return http.StatusNotImplemented, "NotImplemented"
	case errors.Is(err, ErrChunkSignature):
		return http.StatusForbidden, "SignatureDoesNotMatch"
	case errors.Is(err, ErrChunkFraming):
		return http.StatusBadRequest, "InvalidRequest"
	default:
		return http.StatusBadRequest, "InvalidRequest"
	}
}
