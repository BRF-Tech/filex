package s3api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/go-chi/chi/v5"

	"github.com/brf-tech/filex/backend/internal/basepath"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/s3api"
)

// Path-style S3 on the application's host, under a base path
// (FILEX_BASE_PATH). The client is configured with the endpoint
// `https://example.com/filex/s3` and SIGNS `/filex/s3/<bucket>/<key>`; filex
// takes the base off before the router sees the request. The signature is
// checked against the path the client sent — the one thing a proxy in front
// could not have changed without the client's knowledge — so path-style works
// under a base exactly as it works at the root, and a dedicated S3 host
// (FILEX_S3_DOMAIN) stays what it was: served at its own root, never under
// the app's base.
func baseRouter(hz *harness, base string) http.Handler {
	r := chi.NewRouter()
	r.Use(basepath.Middleware(base))
	r.Mount(s3api.Prefix, hz.h)
	return r
}

func serveSigned(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestPathStyleUnderABasePathVerifiesTheSignedPath(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	if err := os.WriteFile(filepath.Join(hz.rootOf(t, st), "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "rclone"})
	h := baseRouter(hz, "/filex")

	// ListBuckets, ListObjects and GetObject, each signed over the full path.
	rec := serveSigned(h, signed(t, key, http.MethodGet, "https://app.filex.test/filex/s3/", hz.at))
	if rec.Code != http.StatusOK {
		t.Fatalf("ListBuckets under the base = %d: %s", rec.Code, rec.Body.String())
	}
	if names := listBuckets(t, rec.Body.Bytes()); len(names) != 1 || names[0] != "main" {
		t.Fatalf("buckets = %v, want [main]", names)
	}
	rec = serveSigned(h, signed(t, key, http.MethodGet, "https://app.filex.test/filex/s3/main/", hz.at))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<Key>hello.txt</Key>") {
		t.Fatalf("ListObjects under the base = %d: %s", rec.Code, rec.Body.String())
	}
	rec = serveSigned(h, signed(t, key, http.MethodGet, "https://app.filex.test/filex/s3/main/hello.txt", hz.at))
	if rec.Code != http.StatusOK || rec.Body.String() != "hi" {
		t.Fatalf("GetObject under the base = %d: %q", rec.Code, rec.Body.String())
	}

	// A presigned URL is the same question asked in the query string.
	req, _ := http.NewRequest(http.MethodGet, "https://app.filex.test/filex/s3/main/hello.txt?X-Amz-Expires=900", nil)
	s := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	creds := aws.Credentials{AccessKeyID: key.Key.AccessKeyID, SecretAccessKey: key.Secret}
	signedURL, _, err := s.PresignHTTP(context.Background(), creds, req, "UNSIGNED-PAYLOAD", "s3", "us-east-1", hz.at)
	if err != nil {
		t.Fatal(err)
	}
	pr := httptest.NewRequest(http.MethodGet, signedURL, nil)
	pr.Host = "app.filex.test"
	if rec := serveSigned(h, pr); rec.Code != http.StatusOK || rec.Body.String() != "hi" {
		t.Fatalf("presigned GET under the base = %d: %s", rec.Code, rec.Body.String())
	}

	// A signature over the path WITHOUT the base (a client whose endpoint
	// omits it, or a proxy that stripped it after the client signed) does not
	// verify, and a request outside the base never reaches the endpoint.
	bare := signed(t, key, http.MethodGet, "https://app.filex.test/s3/main/hello.txt", hz.at)
	bare.URL.Path = "/filex" + bare.URL.Path
	bare.RequestURI = bare.URL.Path
	if rec := serveSigned(h, bare); rec.Code != http.StatusForbidden {
		t.Fatalf("a signature over another path = %d, want 403", rec.Code)
	}
	if rec := serveSigned(h, signed(t, key, http.MethodGet, "https://app.filex.test/s3/main/hello.txt", hz.at)); rec.Code != http.StatusNotFound {
		t.Fatalf("outside the base = %d, want 404", rec.Code)
	}
}
