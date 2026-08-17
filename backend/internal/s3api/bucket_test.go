package s3api_test

import (
	"encoding/xml"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/s3api"
)

// Bucket-level verbs. These are the requests a client sends BEFORE it does any
// real work — and every one of them was found by pointing a real client at the
// endpoint rather than by reading the specification.

// ⚠⚠ rclone (and mc, and the aws SDKs with their default settings) call
// CreateBucket before the first upload, to make sure the destination exists.
// Without a branch for it, `PUT /main` reads as PutObject with an empty key and
// answers "a key may not be empty or end in a slash" — so every upload failed
// with "failed to prepare upload" before a single byte was sent (2026-08-16).
func TestCreateBucketOnABucketYouCanSeeSucceeds(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.put(t, key, "https://s3.filex.test/main", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateBucket on an existing bucket = %d: %s", rec.Code, rec.Body.String())
	}
	// S3 answers OK for a bucket you already own, and clients treat anything
	// else as a hard failure of the whole transfer.
	if loc := rec.Header().Get("Location"); loc != "/main" {
		t.Errorf("Location = %q, want /main", loc)
	}
}

// ⚠ A filex bucket is a STORAGE: a driver, a path, and often credentials.
// CreateBucket cannot express any of that, so inventing one from an S3 request
// would create something half-configured that nothing can read. The refusal is
// the honest answer — and it is the SAME answer for a name that does not exist
// and for one that exists but is not this caller's, or the endpoint becomes an
// existence oracle for every other tenant's bucket names.
func TestCreateBucketCannotInventAStorage(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	hz.storage(t, "secret-archive")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main"})

	unknown := hz.put(t, key, "https://s3.filex.test/brand-new", nil)
	hidden := hz.put(t, key, "https://s3.filex.test/secret-archive", nil)

	for name, rec := range map[string]int{"unknown": unknown.Code, "invisible": hidden.Code} {
		if rec != http.StatusForbidden {
			t.Errorf("CreateBucket (%s) = %d, want 403", name, rec)
		}
	}
	if a, b := errorCode(t, unknown.Body.Bytes()), errorCode(t, hidden.Body.Bytes()); a != b {
		t.Errorf("codes differ: unknown %q vs invisible %q — that difference tells a stranger the bucket exists", a, b)
	}
	if code := errorCode(t, unknown.Body.Bytes()); code != "AccessDenied" {
		t.Errorf("code = %q, want AccessDenied", code)
	}
	// Nothing was created behind it.
	if names := listBuckets(t, hz.do(t, key, http.MethodGet, "https://s3.filex.test/").Body.Bytes()); len(names) != 1 {
		t.Errorf("buckets after the refusal = %v, want only main", names)
	}
}

// ⚠⚠ DeleteBucket must never reach the object path. `DELETE /main` with no key
// is a bucket verb; falling through to deleteObject with an empty key aims a
// delete at the root of the storage.
func TestDeleteBucketIsRefusedAndTouchesNothing(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "keep/me.txt", []byte("still here"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.do(t, key, http.MethodDelete, "https://s3.filex.test/main")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("DeleteBucket = %d, want 403", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "keep", "me.txt")); err != nil {
		t.Fatalf("a bucket-level DELETE reached the objects: %v", err)
	}
}

// GetBucketLocation is the second call many clients make. Answering it with an
// object LISTING — which is what a bucket GET does without this branch — hands
// the client XML it cannot parse for a question it asked in good faith.
func TestGetBucketLocation(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main?location=")
	if rec.Code != http.StatusOK {
		t.Fatalf("GetBucketLocation = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		XMLName xml.Name `xml:"LocationConstraint"`
		Region  string   `xml:",chardata"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal %s: %v", rec.Body.String(), err)
	}
	// filex has no regions. Echoing the one the caller signed with is what a
	// single-region server does: a different answer sends SDKs into a redirect
	// loop chasing a region endpoint that does not exist.
	if out.Region != "us-east-1" {
		t.Errorf("LocationConstraint = %q, want the signed region back", out.Region)
	}
}

// ⚠ A subresource filex does not implement must SAY so. Falling through to a
// listing means `GET /main?tagging` answers with the bucket's objects, and the
// client parses an object list as a tag set — a plausible wrong answer, which
// is the failure mode this endpoint is written to avoid.
func TestUnsupportedBucketSubresourcesAreNotAListing(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "visible.txt", []byte("x"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	for _, sub := range []string{"tagging", "acl", "policy", "lifecycle", "cors", "versions", "object-lock"} {
		t.Run(sub, func(t *testing.T) {
			rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main?"+sub+"=")
			if rec.Code == http.StatusOK {
				t.Fatalf("?%s answered 200 with %s", sub, rec.Body.String())
			}
			if body := rec.Body.String(); len(body) > 0 && xmlRoot(t, rec.Body.Bytes()) == "ListBucketResult" {
				t.Fatalf("?%s was answered with an object listing", sub)
			}
		})
	}

	// Versioning is the exception: clients ask whether it is on, and "off" is a
	// true answer filex can give. Silence here makes rclone assume versions
	// exist and ask for them on every listing.
	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main?versioning=")
	if rec.Code != http.StatusOK {
		t.Fatalf("GetBucketVersioning = %d: %s", rec.Code, rec.Body.String())
	}
	if root := xmlRoot(t, rec.Body.Bytes()); root != "VersioningConfiguration" {
		t.Fatalf("GetBucketVersioning root = %q", root)
	}
}

// A listing must still be a listing — the subresource guard must not swallow
// the query parameters ListObjectsV2 actually uses.
func TestListingParametersAreNotMistakenForSubresources(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "docs/a.txt", []byte("a"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	for _, q := range []string{
		"?list-type=2&prefix=docs/&delimiter=/&encoding-type=url",
		"?list-type=2&max-keys=10&fetch-owner=true&start-after=docs/",
		"?prefix=docs/&marker=&max-keys=1000",
		"?list-type=2&continuation-token=&x-id=ListObjectsV2",
	} {
		rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main"+q)
		if rec.Code != http.StatusOK {
			t.Fatalf("listing %s = %d: %s", q, rec.Code, rec.Body.String())
		}
		if root := xmlRoot(t, rec.Body.Bytes()); root != "ListBucketResult" {
			t.Fatalf("listing %s answered %q", q, root)
		}
	}
}

func xmlRoot(t *testing.T, body []byte) string {
	t.Helper()
	var v struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(body, &v); err != nil {
		return ""
	}
	return v.XMLName.Local
}

// ⚠⚠ Host routing is what makes the endpoint reachable at all: every S3 client
// talks to the ROOT of its endpoint URL, so the handler has to own a host
// rather than a path prefix. Mounted only under /s3, `GET /` reached the web
// app and rclone parsed an HTML redirect as XML ("XML syntax error on line
// 10", 2026-08-16).
func TestHostMatches(t *testing.T) {
	cases := map[string]struct {
		domain, host string
		want         bool
	}{
		"exact":            {"s3.filex.sh", "s3.filex.sh", true},
		"with port":        {"s3.filex.sh", "s3.filex.sh:9000", true},
		"virtual hosted":   {"s3.filex.sh", "backups.s3.filex.sh", true},
		"case insensitive": {"s3.filex.sh", "S3.Filex.SH", true},
		"domain has port":  {"s3.filex.sh:9000", "s3.filex.sh", true},
		// ⚠ A different host must NOT match, or enabling the endpoint hands the
		// whole application to it — measured live: FILEX_S3_DOMAIN pointed at
		// the app's own host made /healthz answer with a signed-request refusal.
		"other host":    {"s3.filex.sh", "fm.example.com", false},
		"suffix trick":  {"s3.filex.sh", "nots3.filex.sh", false},
		"empty domain":  {"", "s3.filex.sh", false},
		"empty host":    {"s3.filex.sh", "", false},
		"parent domain": {"s3.filex.sh", "filex.sh", false},
	}
	for name, c := range cases {
		if got := s3api.HostMatches(c.domain, c.host); got != c.want {
			t.Errorf("%s: HostMatches(%q, %q) = %v, want %v", name, c.domain, c.host, got, c.want)
		}
	}
}
