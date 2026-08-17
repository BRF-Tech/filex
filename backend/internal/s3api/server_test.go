package s3api_test

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/acl"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/s3api"
	"github.com/brf-tech/filex/backend/internal/secretbox"
	"github.com/brf-tech/filex/backend/internal/staging"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/tenantstore"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// Bucket = storage, and a listing must show exactly what the caller may reach:
// the tenant boundary, the caller's grants, and the credential's own
// confinement, all three.

type harness struct {
	h       *s3api.Handler
	res     *protocolauth.Resolver
	store   db.Store
	at      time.Time
	roots   map[int64]string
	staging string
}

// stagingDirs counts the uploads currently staged on disk. A completed or
// aborted upload must leave none: half-finished bytes that nobody will ever
// claim are how a disk fills up quietly.
func (hz *harness) stagingDirs(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir(hz.staging)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range ents {
		if e.IsDir() {
			n++
		}
	}
	return n
}

// rootOf returns the on-disk root of a seeded storage.
func (hz *harness) rootOf(t *testing.T, st *model.Storage) string {
	t.Helper()
	root, ok := hz.roots[st.ID]
	if !ok {
		t.Fatalf("storage %d was not created by this harness", st.ID)
	}
	return root
}

func newHarness(t *testing.T, multiTenant bool) *harness {
	t.Helper()
	_, raw := testutil.NewTestDB(t)
	var store db.Store = identitystore.New(raw)
	if multiTenant {
		// The scoped store is what production hands the protocol servers;
		// testing against the raw one would measure a store the product does
		// not have.
		store = tenantstore.New(store)
	}

	res := protocolauth.New(store, acl.New(store), multiTenant)
	box, err := secretbox.New("test-environment-secret-key")
	if err != nil {
		t.Fatalf("secretbox: %v", err)
	}
	res.Secrets = box

	stagingRoot := t.TempDir()
	hz := &harness{res: res, store: store, at: time.Now().UTC(), roots: map[int64]string{}, staging: stagingRoot}
	hz.h = s3api.NewHandler(s3api.Config{
		Enabled:     true,
		Store:       store,
		Auth:        res,
		ACL:         acl.New(store),
		MultiTenant: multiTenant,
		Domain:      "s3.filex.test",
		// A real staging area, so multipart measures the product's own parts
		// on disk rather than a fake that agrees with it by construction.
		Staging:   staging.New(stagingRoot),
		PublicURL: "https://s3.filex.test",
		// The per-user ceiling, wired exactly as production wires it — an
		// untested quota is a quota nobody knows is off.
		Quota: quota.New(store),
		// The one read door the product uses everywhere else. Reading the
		// driver directly here would make this suite green over a surface
		// production does not have.
		Body: filebody.New(store, nil),
		// A real local driver, so a listing measures the product's own walk
		// rather than a fake that agrees with it by construction.
		Resolver: func(id int64) (storage.Driver, error) {
			st, err := store.GetStorage(context.Background(), id)
			if err != nil {
				return nil, err
			}
			var cfg map[string]any
			if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
				return nil, err
			}
			drv := &local.Driver{}
			if err := drv.Init(context.Background(), cfg); err != nil {
				return nil, err
			}
			return drv, nil
		},
	})
	return hz
}

func (hz *harness) user(t *testing.T, email, role string) *model.User {
	t.Helper()
	hash, err := authlocal.HashPassword("S3Pass!1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := hz.store.CreateUser(context.Background(), email, hash, role, "en", "UTC")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	return u
}

func (hz *harness) storage(t *testing.T, name string) *model.Storage {
	t.Helper()
	root := t.TempDir()
	st, err := hz.store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", Enabled: true,
		ConfigJSON: json.RawMessage(`{"path":` + strconv.Quote(filepath.ToSlash(root)) + `}`),
	})
	if err != nil {
		t.Fatalf("storage %s: %v", name, err)
	}
	hz.roots[st.ID] = root
	return st
}

func (hz *harness) key(t *testing.T, u *model.User, req protocolauth.IssueRequest) *protocolauth.IssuedKey {
	t.Helper()
	req.User = u
	issued, err := hz.res.Issue(context.Background(), req)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return issued
}

// recorderFor runs an already-signed request through the handler.
func recorderFor(hz *harness, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	hz.h.ServeHTTP(rec, r)
	return rec
}

// do signs a request and runs it through the handler.
func (hz *harness) do(t *testing.T, key *protocolauth.IssuedKey, method, url string) *httptest.ResponseRecorder {
	t.Helper()
	r := signed(t, key, method, url, hz.at)
	rec := httptest.NewRecorder()
	hz.h.ServeHTTP(rec, r)
	return rec
}

func listBuckets(t *testing.T, body []byte) []string {
	t.Helper()
	var out struct {
		Buckets struct {
			Bucket []struct {
				Name         string `xml:"Name"`
				CreationDate string `xml:"CreationDate"`
			} `xml:"Bucket"`
		} `xml:"Buckets"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	names := make([]string, 0, len(out.Buckets.Bucket))
	for _, b := range out.Buckets.Bucket {
		if b.CreationDate == "" {
			t.Error("a bucket came back with no CreationDate; clients sort on it")
		}
		names = append(names, b.Name)
	}
	return names
}

func TestListBucketsShowsTheCallersStorages(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	hz.storage(t, "archive")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "rclone"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/")
	if rec.Code != http.StatusOK {
		t.Fatalf("ListBuckets = %d: %s", rec.Code, rec.Body.String())
	}
	names := listBuckets(t, rec.Body.Bytes())
	if len(names) != 2 {
		t.Fatalf("buckets = %v, want main and archive", names)
	}
}

// A confined key must not even learn the NAMES of the other buckets — a bucket
// name is information, and this is the same leak the ACL filter exists to stop
// one level down.
func TestConfinedKeySeesOnlyItsOwnBucket(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	hz.storage(t, "secret-archive")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main", Prefix: "projects"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/")
	names := listBuckets(t, rec.Body.Bytes())
	if len(names) != 1 || names[0] != "main" {
		t.Fatalf("buckets = %v, want only main", names)
	}
}

// An RBAC storage with no grant is invisible to a non-admin, exactly as it is
// over /dav and in the web explorer.
func TestRBACStorageWithoutAGrantIsInvisible(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "open")
	locked := hz.storage(t, "locked")
	locked.RBACEnabled = true
	if err := hz.store.UpdateStorage(context.Background(), locked); err != nil {
		t.Fatalf("update storage: %v", err)
	}
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	names := listBuckets(t, hz.do(t, key, http.MethodGet, "https://s3.filex.test/").Body.Bytes())
	if len(names) != 1 || names[0] != "open" {
		t.Fatalf("buckets = %v, want only open", names)
	}
}

// Path-style and virtual-hosted addressing must BOTH work: restic and the
// older SDKs use the first, current SDKs default to the second, and supporting
// one silently excludes half the client ecosystem.
//
// ⚠ Measured through HeadBucket rather than through an object GET. The first
// draft of this test asserted NotImplemented on both styles — which passes with
// virtual-host parsing removed entirely, because `main.s3.filex.test/report.pdf`
// then reads as bucket "report.pdf" and answers NotImplemented too. A test that
// cannot tell the two apart is not testing the thing it is named after.
func TestBothAddressingStylesReachTheSameBucket(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	if rec := hz.do(t, key, http.MethodHead, "https://s3.filex.test/main"); rec.Code != http.StatusOK {
		t.Errorf("path-style HeadBucket = %d, want 200", rec.Code)
	}
	if rec := hz.do(t, key, http.MethodHead, "https://main.s3.filex.test/"); rec.Code != http.StatusOK {
		t.Errorf("virtual-hosted HeadBucket = %d, want 200 — the host label is the bucket", rec.Code)
	}
	// And a bucket that does not exist must answer 404 in BOTH styles, so a
	// client cannot be told "exists" by one address and "gone" by the other.
	if rec := hz.do(t, key, http.MethodHead, "https://s3.filex.test/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("path-style unknown bucket = %d, want 404", rec.Code)
	}
	if rec := hz.do(t, key, http.MethodHead, "https://nope.s3.filex.test/"); rec.Code != http.StatusNotFound {
		t.Errorf("virtual-hosted unknown bucket = %d, want 404", rec.Code)
	}
}

// A bucket the caller cannot reach must look ABSENT, not forbidden — S3 itself
// answers 404 cross-account, because the alternative is an existence oracle.
func TestUnreachableBucketIsNoSuchBucketNotAccessDenied(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	hz.storage(t, "secret-archive")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/secret-archive/x.txt")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("confined key reaching another bucket = %d, want 404", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "NoSuchBucket" {
		t.Errorf("code = %q, want NoSuchBucket (AccessDenied would confirm it exists)", code)
	}
	if rec := hz.do(t, key, http.MethodHead, "https://s3.filex.test/secret-archive"); rec.Code != http.StatusNotFound {
		t.Errorf("HeadBucket on an unreachable bucket = %d, want 404", rec.Code)
	}
}

// An unsigned request must be refused before anything else happens.
func TestUnsignedRequestIsRefused(t *testing.T) {
	hz := newHarness(t, false)
	hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")

	rec := httptest.NewRecorder()
	hz.h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://s3.filex.test/", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unsigned = %d, want 403", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "AccessDenied" {
		t.Errorf("code = %q, want AccessDenied", code)
	}
}

// The kill switch takes the whole subtree out, the same shape /dav uses.
func TestDisabledEndpointIs404(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	off := s3api.NewHandler(s3api.Config{Enabled: false, Store: hz.store, Auth: hz.res, ACL: acl.New(hz.store)})
	rec := httptest.NewRecorder()
	off.ServeHTTP(rec, signed(t, key, http.MethodGet, "https://s3.filex.test/", hz.at))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled endpoint = %d, want 404", rec.Code)
	}
}

func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Code string `xml:"Code"`
	}
	if err := xml.Unmarshal(body, &e); err != nil {
		return ""
	}
	return e.Code
}
