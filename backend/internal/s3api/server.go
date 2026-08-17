package s3api

import (
	"context"
	"net/http"
	"strings"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/protocolsync"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/search"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/tenant"
	"github.com/brf-tech/filex/backend/internal/thumb"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Prefix is where the endpoint is mounted when it shares a host with the app.
// A dedicated host (s3.filex.sh) mounts it at the root instead.
const Prefix = "/s3"

// Config wires the handler to the shared services.
type Config struct {
	// Enabled — FILEX_S3 kill switch. When false the whole subtree 404s, the
	// same shape /dav uses.
	Enabled bool
	Store   db.Store
	// Auth is the shared credential resolver. Required: without it nothing can
	// authenticate, and answering 500 to every request would look like an
	// outage rather than a configuration gap.
	Auth *protocolauth.Resolver
	// ACL resolves per-user grants.
	ACL *acl.Resolver
	// Resolver returns the live driver for a storage id.
	Resolver func(int64) (storage.Driver, error)
	// Staging holds in-flight multipart parts — the SAME area the browser's
	// resumable upload uses, so there is one place on disk with half-finished
	// uploads, one sweeper and one disk guard. Nil disables multipart.
	Staging *staging.Area
	// PublicURL is echoed in a completed upload's Location.
	PublicURL string
	// Quota is the per-user ceiling, checked before bytes land. Nil disables
	// the check, which is what a single-user install wants.
	Quota *quota.Service
	// Index and Thumbs feed the shared post-write bookkeeping. Both optional.
	Index  *search.Index
	Thumbs *thumb.Pipeline
	// Body is the ONE door every read surface goes through: it knows whether
	// the bytes are on the driver or still in the staging area of an upload
	// that has not finished transferring. Reading the driver directly here
	// would make a just-uploaded object 404 over S3 while it is readable
	// everywhere else.
	Body *filebody.Resolver
	// MultiTenant mirrors config.MultiTenant.
	MultiTenant bool
	// Domain is the base host for virtual-hosted-style addressing
	// (bucket.s3.filex.sh). Empty disables that style, leaving path-style —
	// which is what restic and the older SDKs use anyway.
	Domain string
}

// Handler serves the S3 protocol.
type Handler struct {
	cfg  Config
	auth *Authenticator
	// uploads maps a multipart upload id to its destination. See uploadKey for
	// why it is memory and not a table.
	uploads uploadRegistry
	// syncer is the shared post-write bookkeeping (node cache, search index,
	// thumbnails, write hooks) — the same one /dav uses, with this protocol's
	// own origin so the audit trail can tell them apart.
	syncer *protocolsync.Syncer
	// etags remembers digests computed for backends that report none. See
	// etag.go for why computing beats synthesising.
	etags etagCache
}

// NewHandler builds the handler.
func NewHandler(cfg Config) *Handler {
	return &Handler{
		cfg:    cfg,
		auth:   NewAuthenticator(cfg.Auth),
		syncer: protocolsync.New(cfg.Store, cfg.Index, cfg.Thumbs, writehook.OriginS3),
	}
}

// sync returns the post-write bookkeeper.
func (h *Handler) sync() *protocolsync.Syncer { return h.syncer }

// target is a parsed request: which bucket, which key.
type target struct {
	Bucket string
	Key    string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.cfg.Enabled {
		http.NotFound(w, r)
		return
	}

	principal, err := h.auth.Authenticate(r)
	if err != nil {
		code, status := ErrorCode(err)
		WriteError(w, r, status, code, "the request signature we calculated does not match the signature you provided")
		return
	}

	// Everything downstream — the scoped store, quota accounting, the audit
	// trail — reads identity and tenant from the context. Stamping both here,
	// in one call, is what stops a handler from attaching one and forgetting
	// the other (see internal/protocolauth).
	ctx := principal.WithContext(r.Context())
	ctx = WithPrincipal(ctx, principal)
	r = r.WithContext(ctx)

	tgt, err := h.parseTarget(r)
	if err != nil {
		WriteError(w, r, http.StatusBadRequest, "InvalidRequest", err.Error())
		return
	}

	switch {
	case tgt.Bucket == "":
		h.listBuckets(w, r, principal)
	case tgt.Key == "" && r.Method == http.MethodHead:
		h.headBucket(w, r, principal, tgt.Bucket)
	// ⚠ CreateBucket and DeleteBucket are BUCKET verbs and must be caught here.
	// Falling through leaves `PUT /bucket` reading as an upload with an empty
	// key — which is how every rclone transfer failed before a byte was sent —
	// and `DELETE /bucket` aiming a delete at the root of the storage.
	case tgt.Key == "" && r.Method == http.MethodPut:
		h.createBucket(w, r, principal, tgt.Bucket)
	case tgt.Key == "" && r.Method == http.MethodDelete:
		h.deleteBucket(w, r)
	case tgt.Key == "" && r.Method == http.MethodPost && r.URL.Query().Has("delete"):
		st, code, ok := h.resolveBucket(r.Context(), principal, tgt.Bucket)
		if !ok {
			WriteError(w, r, http.StatusNotFound, code, "the specified bucket does not exist")
			return
		}
		h.deleteObjects(w, r, principal, st)
	case tgt.Key == "" && r.Method == http.MethodGet:
		st, code, ok := h.resolveBucket(r.Context(), principal, tgt.Bucket)
		if !ok {
			WriteError(w, r, http.StatusNotFound, code, "the specified bucket does not exist")
			return
		}
		q := r.URL.Query()
		switch {
		case q.Has("location"):
			h.getBucketLocation(w, r)
		case q.Has("versioning"):
			h.getBucketVersioning(w, r)
		default:
			// ⚠ A sub-resource filex does not implement must be REFUSED, not
			// answered with a listing: `GET /bucket?tagging` handed back the
			// bucket's objects, and a client parses that as a tag set.
			if sub := subresourceOf(q); sub != "" {
				refuseSubresource(w, r, sub)
				return
			}
			h.listObjects(w, r, principal, st)
		}
	default:
		st, code, ok := h.resolveBucket(r.Context(), principal, tgt.Bucket)
		if !ok {
			WriteError(w, r, http.StatusNotFound, code, "the specified bucket does not exist")
			return
		}
		q := r.URL.Query()
		uploadID := q.Get("uploadId")
		switch {
		// ── multipart, selected by query parameter rather than by method ──
		case r.Method == http.MethodPost && q.Has("uploads"):
			h.createMultipartUpload(w, r, principal, st, tgt.Key)
		case r.Method == http.MethodPut && uploadID != "" && r.Header.Get("X-Amz-Copy-Source") != "":
			// UploadPartCopy. Refused honestly rather than half-done: it needs
			// a byte-range read of the source into a staged part, and a
			// plausible wrong answer here corrupts a multipart assembly.
			WriteError(w, r, http.StatusNotImplemented, "NotImplemented",
				"UploadPartCopy is not supported; copy the whole object instead")
		case r.Method == http.MethodPut && uploadID != "":
			n, err := partNumberFrom(r)
			if err != nil {
				WriteError(w, r, http.StatusBadRequest, "InvalidArgument", "partNumber is required")
				return
			}
			h.uploadPart(w, r, principal, st, tgt.Key, uploadID, n)
		case r.Method == http.MethodPost && uploadID != "":
			h.completeMultipartUpload(w, r, principal, st, tgt.Key, uploadID)
		case r.Method == http.MethodDelete && uploadID != "":
			h.abortMultipartUpload(w, r, principal, st, tgt.Key, uploadID)
		case r.Method == http.MethodGet && uploadID != "":
			h.listParts(w, r, principal, st, tgt.Key, uploadID)

		// ⚠ Same guard one level down: `GET /bucket/key?acl` must not hand back
		// the FILE. Multipart has already been dispatched above, so what is left
		// here is a sub-resource this endpoint does not implement — including
		// ?versionId, which asks for a specific version filex cannot produce and
		// must not silently answer with the current one.
		case subresourceOf(q, "uploads", "uploadId") != "":
			refuseSubresource(w, r, subresourceOf(q, "uploads", "uploadId"))

		// ── plain object verbs ──
		case r.Method == http.MethodGet:
			h.getObject(w, r, principal, st, tgt.Key, true)
		case r.Method == http.MethodHead:
			h.getObject(w, r, principal, st, tgt.Key, false)
		case r.Method == http.MethodPut && r.Header.Get("X-Amz-Copy-Source") != "":
			h.copyObject(w, r, principal, st, tgt.Key, r.Header.Get("X-Amz-Copy-Source"))
		case r.Method == http.MethodPut:
			h.putObject(w, r, principal, st, tgt.Key)
		case r.Method == http.MethodDelete:
			h.deleteObject(w, r, principal, st, tgt.Key)
		default:
			// A clean refusal with the right code beats a plausible wrong
			// answer — and a client can tell the difference, which is the
			// whole argument.
			WriteError(w, r, http.StatusNotImplemented, "NotImplemented",
				"this operation is not available on this endpoint yet")
		}
	}
}

// parseTarget works out the bucket and key from the request.
//
// ⚠ Both addressing styles must work. restic and the older SDKs use
// path-style (endpoint/bucket/key); current SDKs default to virtual-hosted
// (bucket.endpoint/key). Supporting only one silently excludes half the
// client ecosystem.
func (h *Handler) parseTarget(r *http.Request) (target, error) {
	path := strings.TrimPrefix(r.URL.Path, Prefix)
	path = strings.TrimPrefix(path, "/")

	if bucket, ok := h.virtualHostBucket(r.Host); ok {
		// ⚠ The host names the bucket here, but it does NOT decide tenancy —
		// that comes from the credential, always. resolveBucket enforces it:
		// a host naming a bucket the caller cannot reach answers NoSuchBucket
		// rather than quietly resolving to something else. (The Host header is
		// signed, so it cannot have been rewritten in transit either.)
		return target{Bucket: bucket, Key: path}, nil
	}

	bucket, key, _ := strings.Cut(path, "/")
	return target{Bucket: bucket, Key: key}, nil
}

// HostMatches reports whether a Host header targets the S3 endpoint — either
// the endpoint host itself or a `<bucket>.<host>` virtual-hosted address.
//
// ⚠ This is what makes the endpoint usable at all. Every S3 client talks to
// the ROOT of its endpoint URL: `GET /` is ListBuckets, `PUT /bucket/key` is an
// upload. Mounted only under a path prefix, `GET /` reaches the web app
// instead and the client is handed an HTML redirect to parse as XML — which is
// exactly how this was found, with rclone reporting "XML syntax error on line
// 10" against a page that says "Found".
//
// ⚠⚠ Pointing this at the host the web app already serves would hand the whole
// app to the S3 handler. It must be a DEDICATED host (s3.example.com), which is
// also what virtual-hosted addressing needs a wildcard record for.
func HostMatches(domain, host string) bool {
	if domain == "" {
		return false
	}
	h, _, _ := strings.Cut(host, ":")
	d, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(domain)), ":")
	h = strings.ToLower(h)
	return h == d || strings.HasSuffix(h, "."+d)
}

// virtualHostBucket extracts the bucket from `bucket.s3.filex.sh`.
func (h *Handler) virtualHostBucket(host string) (string, bool) {
	if h.cfg.Domain == "" {
		return "", false
	}
	host, _, _ = strings.Cut(host, ":") // strip any port
	suffix := "." + h.cfg.Domain
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	name := strings.TrimSuffix(host, suffix)
	if name == "" || strings.Contains(name, ".") {
		// A multi-label prefix is not a bucket name; treating it as one would
		// invent a bucket out of a misconfigured DNS record.
		return "", false
	}
	return name, true
}

// listBuckets answers with the storages this caller may see.
//
// The mapping is bucket = storage, the same one /dav uses for its first path
// segment. Reusing it is not a shortcut: two protocols disagreeing about what
// a storage is called is how a user ends up unable to find, over one protocol,
// the folder they just created over another.
func (h *Handler) listBuckets(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal) {
	ctx := r.Context()
	storages, err := h.cfg.Store.ListEnabledStorages(ctx)
	if err != nil {
		WriteError(w, r, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}

	scope, _ := tenant.FromContext(ctx)
	out := ListAllMyBucketsResult{
		Owner: Owner{ID: ownerID(p), DisplayName: ownerName(p)},
	}
	out.Buckets.Bucket = []Bucket{}

	for _, st := range storages {
		if !h.visible(ctx, p, scope, st) {
			continue
		}
		out.Buckets.Bucket = append(out.Buckets.Bucket, Bucket{
			Name:         st.Name,
			CreationDate: amzTime(st.CreatedAt),
		})
	}
	WriteXML(w, r, http.StatusOK, out)
}

// headBucket answers whether the caller can reach a bucket. Every client calls
// it before doing anything else, and it is the cheapest possible check: no
// body, no listing, just the same resolution every other operation uses.
//
// ⚠ HEAD responses carry NO BODY, so the S3 error code cannot travel in one.
// Clients therefore key off the status alone here, which is the one place the
// usual "the code is the contract" rule does not apply.
func (h *Handler) headBucket(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, bucket string) {
	if _, _, ok := h.resolveBucket(r.Context(), p, bucket); !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(http.StatusOK)
}

// resolveBucket maps a bucket name onto a storage the caller may actually
// reach, or reports the S3 error to answer with.
//
// ⚠ It answers **NoSuchBucket**, never AccessDenied, for a bucket that exists
// but is not this caller's. Distinguishing the two would make the endpoint an
// existence oracle across tenants — S3 itself answers 404 cross-account for
// exactly this reason. The cost is a slightly confusing message for someone
// who mistyped their own bucket; the alternative is telling strangers which
// buckets exist.
func (h *Handler) resolveBucket(ctx context.Context, p *protocolauth.Principal, name string) (*model.Storage, string, bool) {
	if name == "" {
		return nil, "NoSuchBucket", false
	}
	st, err := h.cfg.Store.GetStorageByName(ctx, name)
	if err != nil || st == nil {
		return nil, "NoSuchBucket", false
	}
	if !st.Enabled {
		return nil, "NoSuchBucket", false
	}
	scope, _ := tenant.FromContext(ctx)
	if !h.visible(ctx, p, scope, st) {
		return nil, "NoSuchBucket", false
	}
	return st, "", true
}

// visible applies every gate a listing has to pass: the tenant boundary, the
// caller's grants, and the credential's own confinement.
//
// ⚠ All three, not one. ListEnabledStorages is already tenant-scoped when the
// store is the scoped one, and re-checking here keeps the answer correct even
// if this handler is ever handed a raw store — the same belt-and-braces /dav
// settled on after a tenant admin saw ten tenants' storages.
func (h *Handler) visible(ctx context.Context, p *protocolauth.Principal, scope *tenant.Scope, st *model.Storage) bool {
	if !scope.CanAccessStorage(st.ID) {
		return false
	}
	// A confined credential sees only the storage it is confined to. Without
	// this a key scoped to one bucket would still enumerate the names of every
	// other one, and a bucket name is information.
	if p.Confine != nil && p.Confine.Adapter != "" && p.Confine.Adapter != st.Name {
		return false
	}
	set, err := p.ACL(ctx, st)
	if err != nil || !set.StorageVisible() {
		return false
	}
	return true
}

// ownerID / ownerName fill S3's Owner element. The values are opaque to every
// client that matters, so filex uses identifiers that mean something in filex
// rather than inventing AWS-shaped ones.
func ownerID(p *protocolauth.Principal) string {
	if p == nil || p.User == nil {
		return ""
	}
	if p.User.Username != "" {
		return p.User.Username
	}
	return p.User.Email
}

func ownerName(p *protocolauth.Principal) string {
	if p == nil || p.User == nil {
		return ""
	}
	if p.User.DisplayName != "" {
		return p.User.DisplayName
	}
	return ownerID(p)
}
