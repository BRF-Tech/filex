package s3api

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"

	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// Bucket-level verbs, and the guard that keeps a question filex cannot answer
// from being answered with something else.
//
// ⚠⚠ Everything in this file was written after pointing rclone at the endpoint
// (2026-08-16). The suite was green and the endpoint was unusable: a client
// does not start with GetObject, it starts with CreateBucket and
// GetBucketLocation, and those requests were landing in the object path.

// LocationConstraint is the GetBucketLocation response.
type LocationConstraint struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ LocationConstraint"`
	Region  string   `xml:",chardata"`
}

// VersioningConfiguration is the GetBucketVersioning response. filex keeps
// version history of its own, but not the S3 object-version model, so the
// truthful answer is that S3 versioning is off.
type VersioningConfiguration struct {
	XMLName xml.Name `xml:"http://s3.amazonaws.com/doc/2006-03-01/ VersioningConfiguration"`
	Status  string   `xml:"Status,omitempty"`
}

// createBucket answers the ensure-the-bucket-exists call every client makes
// before its first upload.
//
// ⚠ A filex bucket is a STORAGE — a driver, a path, credentials, a quota. A
// CreateBucket request carries none of that, so creating one from here would
// produce a half-configured storage that nothing can read. Existing and
// reachable answers 200 (which is what S3 does for a bucket you own, and what
// clients need to hear to continue); anything else is refused.
//
// ⚠ The refusal is the SAME for a name that does not exist and for one that
// belongs to somebody else. Distinguishing them would let a stranger probe for
// bucket names, which is the existence oracle resolveBucket exists to avoid.
func (h *Handler) createBucket(w http.ResponseWriter, r *http.Request, p *protocolauth.Principal, bucket string) {
	if _, _, ok := h.resolveBucket(r.Context(), p, bucket); !ok {
		WriteError(w, r, http.StatusForbidden, "AccessDenied",
			"buckets map to filex storages and are created by an administrator, not over S3")
		return
	}
	w.Header().Set("Location", "/"+bucket)
	w.Header().Set("x-amz-request-id", requestID(r))
	w.WriteHeader(http.StatusOK)
}

// deleteBucket refuses.
//
// ⚠⚠ This branch exists so that `DELETE /bucket` cannot fall through to the
// object path, where an empty key aims a delete at the root of the storage.
// Deleting a storage is an administrative act with consequences an S3 client
// cannot see (replication targets, shares, quota accounting) — it belongs in
// the admin UI, and the honest answer here is no.
func (h *Handler) deleteBucket(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, http.StatusForbidden, "AccessDenied",
		"a bucket is a filex storage; delete it from the admin interface")
}

// getBucketLocation echoes the region the caller signed with.
//
// filex has no regions. Echoing what the client used is what a single-region
// server does: answering a DIFFERENT region sends the AWS SDKs into a redirect
// chase for an endpoint that does not exist.
func (h *Handler) getBucketLocation(w http.ResponseWriter, r *http.Request) {
	region := "us-east-1"
	if sr, err := Parse(r); err == nil && sr.Region != "" {
		region = sr.Region
	}
	WriteXML(w, r, http.StatusOK, LocationConstraint{Region: region})
}

// getBucketVersioning answers "not enabled", which is true.
func (h *Handler) getBucketVersioning(w http.ResponseWriter, r *http.Request) {
	WriteXML(w, r, http.StatusOK, VersioningConfiguration{})
}

// subresources is S3's own closed set of sub-resource query parameters — the
// same list SigV4 canonicalisation is defined over.
//
// ⚠ The membership test runs against THIS set rather than against an allowlist
// of listing parameters, because the two failure directions are not equal: an
// unknown listing parameter treated as a subresource breaks a listing that
// should work, while an unknown subresource treated as a listing answers a
// question about tags or policy with a list of files. Presigned requests also
// carry X-Amz-* parameters in the query, and those must never read as
// subresources.
var subresources = map[string]bool{
	"accelerate": true, "acl": true, "analytics": true, "cors": true,
	"delete": true, "encryption": true, "intelligent-tiering": true,
	"inventory": true, "legal-hold": true, "lifecycle": true, "location": true,
	"logging": true, "metrics": true, "notification": true, "object-lock": true,
	"ownershipControls": true, "partNumber": true, "policy": true,
	"policyStatus": true, "publicAccessBlock": true, "replication": true,
	"requestPayment": true, "restore": true, "retention": true, "select": true,
	"tagging": true, "torrent": true, "uploadId": true, "uploads": true,
	"versionId": true, "versioning": true, "versions": true, "website": true,
}

// subresourceOf returns the first sub-resource in the query that the caller
// has not already handled, or "".
func subresourceOf(q url.Values, handled ...string) string {
	skip := make(map[string]bool, len(handled))
	for _, h := range handled {
		skip[h] = true
	}
	for k := range q {
		if skip[k] {
			continue
		}
		if subresources[k] {
			return k
		}
	}
	return ""
}

// EndpointURL is the address a client program must be given.
//
// ⚠⚠ Not the application's own URL. With a dedicated host the endpoint IS that
// host's root; without one it lives under `/s3`, and a client pointed at the
// application root reaches the web app and parses an HTML page as XML. Handing
// out the wrong one is the difference between a paste that works and an error
// nobody can read, which is why this is computed in one place and returned by
// the key endpoints rather than assembled in the UI.
func EndpointURL(publicURL, domain string) string {
	publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return publicURL + Prefix
	}
	if strings.Contains(domain, "://") {
		return strings.TrimRight(domain, "/")
	}
	scheme := "https"
	if strings.HasPrefix(strings.ToLower(publicURL), "http://") {
		scheme = "http"
	}
	return scheme + "://" + strings.TrimRight(domain, "/")
}

// PathStyleRequired reports whether clients must be told to use path-style
// addressing.
//
// Without a dedicated domain there is no `bucket.host` to resolve, and a
// current SDK — which defaults to virtual-hosted — fails at DNS with an error
// that names neither filex nor the cause.
func PathStyleRequired(domain string) bool { return strings.TrimSpace(domain) == "" }

// refuseSubresource answers a sub-resource filex does not implement.
//
// The code matters more than the status: clients branch on it, and several of
// them treat NotImplemented as "this server does not have the feature, carry
// on" while an InternalError is a reason to abort the whole transfer.
func refuseSubresource(w http.ResponseWriter, r *http.Request, sub string) {
	WriteError(w, r, http.StatusNotImplemented, "NotImplemented",
		"the ?"+strings.TrimSpace(sub)+" sub-resource is not available on this endpoint")
}
