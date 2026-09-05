package s3api

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Multipart uploads: how every client sends anything large.
//
// The parts land in filex's own staging area — the same one the browser's
// resumable upload uses — so there is one place on disk holding half-finished
// uploads, with one sweeper, one disk guard and one GC. ⚠ The two protocols
// disagree about part sizes (filex's own upload knows the total up front and
// cuts an even grid; S3 does not), which is why staging grew a variable-part
// mode rather than growing a second staging area.
//
// The upload id is the staging id, so a crashed server leaves exactly one kind
// of debris and the existing sweeper already knows how to clean it.

const (
	// minPartSize is S3's rule: every part except the last must be at least
	// 5 MiB. It is enforced at COMPLETE rather than at upload, exactly as S3
	// does — a client that uploads a small part and then abandons the upload
	// has done nothing wrong.
	minPartSize = 5 << 20
	// maxPartSize caps a single part. S3's own limit is 5 GiB.
	maxPartSize = 5 << 30
)

// InitiateMultipartUploadResult answers CreateMultipartUpload.
type InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

// CompleteMultipartUpload is the request body listing the parts to assemble.
type CompleteMultipartUpload struct {
	XMLName xml.Name        `xml:"CompleteMultipartUpload"`
	Parts   []CompletedPart `xml:"Part"`
}

// CompletedPart is one part the client believes it uploaded.
type CompletedPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

// CompleteMultipartUploadResult answers a successful completion.
type CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

// ListPartsResult answers ListParts.
type ListPartsResult struct {
	XMLName     xml.Name    `xml:"http://s3.amazonaws.com/doc/2006-03-01/ ListPartsResult"`
	Bucket      string      `xml:"Bucket"`
	Key         string      `xml:"Key"`
	UploadID    string      `xml:"UploadId"`
	MaxParts    int         `xml:"MaxParts"`
	IsTruncated bool        `xml:"IsTruncated"`
	Parts       []PartEntry `xml:"Part"`
}

// PartEntry is one staged part, as ListParts reports it.
type PartEntry struct {
	PartNumber int    `xml:"PartNumber"`
	Size       int64  `xml:"Size"`
	ETag       string `xml:"ETag"`
}

// uploadKey ties a staging id to the object it is destined for.
//
// ⚠ It is remembered in memory, not on disk. A restart therefore loses the
// binding and the parts become sweeper debris — which is the honest failure:
// the alternative is a new table for state that lives minutes, and a client
// that gets NoSuchUpload retries the whole upload, which is exactly what it
// does against S3 when an upload expires.
type uploadKey struct {
	StorageID int64
	Key       string
	UserID    int64
}

// createMultipartUpload starts an upload and hands back its id.
func (h *Handler) createMultipartUpload(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage, key string) {
	ctx := r.Context()
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
	if h.cfg.Staging == nil || !h.cfg.Staging.Enabled() {
		WriteError(w, r, http.StatusNotImplemented, "NotImplemented", "multipart uploads need a staging directory")
		return
	}

	id := uuid.NewString()
	if _, err := h.cfg.Staging.CreateVariable(id); err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	var userID int64
	if u := auth.UserFrom(ctx); u != nil {
		userID = u.ID
	}
	h.uploads.put(id, uploadKey{StorageID: st.ID, Key: key, UserID: userID})

	WriteXML(w, r, http.StatusOK, InitiateMultipartUploadResult{
		Bucket: st.Name, Key: key, UploadID: id,
	})
}

// uploadPart stores one part.
func (h *Handler) uploadPart(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage, key, uploadID string, partNumber int) {
	ctx := r.Context()
	up, ok := h.uploads.get(uploadID)
	if !ok || up.StorageID != st.ID || up.Key != key {
		// ⚠ NoSuchUpload, and it is a normal answer rather than an incident:
		// an id from before a restart, or one that has been swept, lands here
		// and the client's correct response is to start over.
		WriteError(w, r, http.StatusNotFound, "NoSuchUpload", "the specified upload does not exist")
		return
	}
	set, err := p.ACL(ctx, st)
	if err != nil || !h.writable(p, set, key) {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "you do not have write access to this key")
		return
	}
	if partNumber < 1 || partNumber > staging.MaxParts {
		WriteError(w, r, http.StatusBadRequest, "InvalidArgument", "part number out of range")
		return
	}

	// The staging area's own disk guard, before the bytes rather than after.
	if r.ContentLength > 0 {
		if err := h.cfg.Staging.EnsureFree(r.ContentLength); err != nil {
			WriteError(w, r, http.StatusInsufficientStorage, "ServiceUnavailable", err.Error())
			return
		}
	}

	// ⚠⚠ Through the SAME body door as PutObject. A part can be aws-chunked
	// too — the current SDKs stream every part as
	// STREAMING-UNSIGNED-PAYLOAD-TRAILER when they attach a checksum — and
	// handing r.Body straight to the staging area writes the length prefixes
	// and chunk signatures INTO the part. The object then assembles from
	// corrupt pieces and nothing reports an error, which is the exact failure
	// chunked.go exists to prevent; this path had been missed (2026-08-16).
	body, _, cerr := h.requestBody(r)
	if cerr != nil {
		status, code := chunkedError(cerr)
		WriteError(w, r, status, code, cerr.Error())
		return
	}
	sums := newChecksumSet(r.Header)

	m, err := h.cfg.Staging.WriteVariablePart(uploadID, partNumber, sums.Wrap(body), maxPartSize)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "InvalidArgument", err.Error())
		return
	}
	if err := sums.Verify(trailersOf(body)); err != nil {
		// The part stays staged and provably wrong; a retry overwrites it, and
		// a completion that used it would fail its own ETag check anyway.
		status, code := checksumErrorCode(err)
		WriteError(w, r, status, code, err.Error())
		return
	}
	part, _ := m.Part(partNumber)

	// The part's ETag is its md5, and the client will send it back at
	// completion — that round trip is where S3 puts the integrity check, so
	// answering with anything else breaks the check rather than the upload.
	w.Header().Set("ETag", `"`+part.MD5+`"`)
	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(http.StatusOK)
}

// completeMultipartUpload assembles the parts into the object.
func (h *Handler) completeMultipartUpload(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage, key, uploadID string) {
	ctx := r.Context()
	up, ok := h.uploads.get(uploadID)
	if !ok || up.StorageID != st.ID || up.Key != key {
		WriteError(w, r, http.StatusNotFound, "NoSuchUpload", "the specified upload does not exist")
		return
	}
	set, err := p.ACL(ctx, st)
	if err != nil || !h.writable(p, set, key) {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "you do not have write access to this key")
		return
	}

	var req CompleteMultipartUpload
	if err := xml.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		WriteError(w, r, http.StatusBadRequest, "MalformedXML", "the completion body could not be parsed")
		return
	}
	if len(req.Parts) == 0 {
		WriteError(w, r, http.StatusBadRequest, "InvalidRequest", "no parts listed")
		return
	}

	m, err := h.cfg.Staging.Manifest(uploadID)
	if err != nil {
		WriteError(w, r, http.StatusNotFound, "NoSuchUpload", "the specified upload does not exist")
		return
	}

	// ⚠ The client's list is checked against what is actually staged, part by
	// part. This is where S3 puts the integrity check for a multipart upload,
	// and skipping it would let a truncated part through — the upload would
	// "succeed" and the object would be quietly wrong.
	sort.Slice(req.Parts, func(i, j int) bool { return req.Parts[i].PartNumber < req.Parts[j].PartNumber })

	// ⚠ Two passes, and the order matters for the ERROR a client sees. A
	// missing or mismatched part is a different mistake from an undersized
	// one — "you listed a part that was never uploaded" tells a client to
	// look at its own bookkeeping, while EntityTooSmall tells it to re-chunk.
	// Checking sizes first would answer the second question to someone asking
	// the first.
	staged := make([]staging.Part, 0, len(req.Parts))
	for _, cp := range req.Parts {
		part, found := m.Part(cp.PartNumber)
		if !found {
			WriteError(w, r, http.StatusBadRequest, "InvalidPart", fmt.Sprintf("part %d was never uploaded", cp.PartNumber))
			return
		}
		if want := strings.Trim(cp.ETag, `"`); want != "" && !strings.EqualFold(want, part.MD5) {
			WriteError(w, r, http.StatusBadRequest, "InvalidPart", fmt.Sprintf("part %d does not match the uploaded bytes", cp.PartNumber))
			return
		}
		staged = append(staged, part)
	}
	// Every part but the last must reach the minimum. Enforced here rather than
	// at upload because S3 does it here — a client that uploads a small part
	// and then abandons the upload has done nothing wrong.
	for i := 0; i < len(staged)-1; i++ {
		if staged[i].Size < minPartSize {
			WriteError(w, r, http.StatusBadRequest, "EntityTooSmall",
				fmt.Sprintf("part %d is %d bytes; every part but the last must be at least %d", staged[i].N, staged[i].Size, minPartSize))
			return
		}
	}

	body, err := h.cfg.Staging.Open(uploadID)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "InvalidPart", err.Error())
		return
	}
	defer body.Close()
	size := body.Size()

	if up.UserID != 0 && h.cfg.Quota != nil {
		if err := h.cfg.Quota.CheckCanWrite(ctx, up.UserID, size); err != nil {
			WriteError(w, r, http.StatusRequestEntityTooLarge, "EntityTooLarge", err.Error())
			return
		}
	}

	drv, err := h.cfg.Resolver(st.ID)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", "storage unavailable")
		return
	}
	writer, ok := drv.(storage.Writer)
	if !ok {
		WriteError(w, r, http.StatusNotImplemented, "NotImplemented", "this storage does not support writes")
		return
	}
	// The last moment at which the bytes this completion is about to replace
	// still exist -- see writehook/overwrite.go. Every S3 client above a size
	// threshold (aws-cli, rclone, restic, Cyberduck, s3fs) switches to
	// multipart, so this is the guard that actually covers a large upload;
	// write.go's single-part guard never runs for one.
	if err := writehook.BeforeOverwrite(ctx, st.ID, key); err != nil {
		WriteError(w, r, http.StatusServiceUnavailable, "ServiceUnavailable",
			"could not preserve the existing object: "+err.Error())
		return
	}
	if err := writer.Write(ctx, key, body, size); err != nil {
		WriteError(w, r, statusForStorageErr(err), codeForWriteErr(err), err.Error())
		return
	}

	etag := m.CompositeETag()
	sctx := context.WithoutCancel(ctx)
	h.sync().Write(sctx, st, key, size, mimeFor(r, key))
	if stat, serr := drv.Stat(sctx, key); serr == nil && etag != "" {
		// ⚠ The composite ETag is `md5(concat(part md5s))-N`, NOT the md5 of
		// the object. That is what S3 returns for a multipart upload and what
		// clients store, so reporting the whole-object digest here would make
		// every later comparison fail.
		h.etags.put(etagKey(st.ID, key, stat.Size, stat.Mtime), strings.Trim(etag, `"`))
	}

	h.uploads.drop(uploadID)
	if err := h.cfg.Staging.Remove(uploadID); err != nil {
		// The bytes are already on the driver; leftover staging is the
		// sweeper's problem, not the client's.
		_ = err
	}

	WriteXML(w, r, http.StatusOK, CompleteMultipartUploadResult{
		Location: strings.TrimSuffix(h.cfg.PublicURL, "/") + "/" + st.Name + "/" + key,
		Bucket:   st.Name, Key: key, ETag: quoteETag(etag),
	})
}

// abortMultipartUpload discards the staged parts.
func (h *Handler) abortMultipartUpload(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage, key, uploadID string) {
	up, ok := h.uploads.get(uploadID)
	if !ok || up.StorageID != st.ID || up.Key != key {
		// ⚠ Aborting an upload that is already gone is a SUCCESS. Clients
		// abort in a defer; answering 404 turns cleanup into a error path.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	set, err := p.ACL(r.Context(), st)
	if err != nil || !h.writable(p, set, key) {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "you do not have write access to this key")
		return
	}
	h.uploads.drop(uploadID)
	_ = h.cfg.Staging.Remove(uploadID)
	w.WriteHeader(http.StatusNoContent)
}

// listParts reports what is staged, so a client can resume.
func (h *Handler) listParts(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, st *model.Storage, key, uploadID string) {
	up, ok := h.uploads.get(uploadID)
	if !ok || up.StorageID != st.ID || up.Key != key {
		WriteError(w, r, http.StatusNotFound, "NoSuchUpload", "the specified upload does not exist")
		return
	}
	set, err := p.ACL(r.Context(), st)
	if err != nil || !h.writable(p, set, key) {
		WriteError(w, r, http.StatusForbidden, "AccessDenied", "you do not have write access to this key")
		return
	}
	m, err := h.cfg.Staging.Manifest(uploadID)
	if err != nil {
		WriteError(w, r, http.StatusNotFound, "NoSuchUpload", "the specified upload does not exist")
		return
	}
	out := ListPartsResult{Bucket: st.Name, Key: key, UploadID: uploadID, MaxParts: staging.MaxParts, Parts: []PartEntry{}}
	parts := append([]staging.Part(nil), m.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].N < parts[j].N })
	for _, pt := range parts {
		out.Parts = append(out.Parts, PartEntry{PartNumber: pt.N, Size: pt.Size, ETag: `"` + pt.MD5 + `"`})
	}
	WriteXML(w, r, http.StatusOK, out)
}

// partNumberFrom reads the part-number query parameter.
func partNumberFrom(r *http.Request) (int, error) {
	v := r.URL.Query().Get("partNumber")
	if v == "" {
		return 0, errors.New("missing partNumber")
	}
	return strconv.Atoi(v)
}

// ─────────────────────── the upload registry ───────────────────────

// uploadRegistry remembers where each in-flight multipart upload is headed.
type uploadRegistry struct {
	mu sync.Mutex
	m  map[string]uploadKey
}

func (u *uploadRegistry) put(id string, k uploadKey) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.m == nil {
		u.m = map[string]uploadKey{}
	}
	u.m[id] = k
}

func (u *uploadRegistry) get(id string) (uploadKey, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	k, ok := u.m[id]
	return k, ok
}

func (u *uploadRegistry) drop(id string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.m, id)
}
