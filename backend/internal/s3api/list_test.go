package s3api_test

import (
	"context"
	"encoding/xml"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// Listing is the operation every client runs first and most often, so it is
// where the filtering and the ordering both have to be exactly right.

type listing struct {
	Name        string `xml:"Name"`
	IsTruncated bool   `xml:"IsTruncated"`
	KeyCount    int    `xml:"KeyCount"`
	NextToken   string `xml:"NextContinuationToken"`
	NextMarker  string `xml:"NextMarker"`
	Contents    []struct {
		Key  string `xml:"Key"`
		Size int64  `xml:"Size"`
		ETag string `xml:"ETag"`
	} `xml:"Contents"`
	CommonPrefixes []struct {
		Prefix string `xml:"Prefix"`
	} `xml:"CommonPrefixes"`
}

func (l listing) keys() []string {
	out := make([]string, 0, len(l.Contents))
	for _, c := range l.Contents {
		out = append(out, c.Key)
	}
	return out
}

func (l listing) prefixes() []string {
	out := make([]string, 0, len(l.CommonPrefixes))
	for _, p := range l.CommonPrefixes {
		out = append(out, p.Prefix)
	}
	return out
}

func parseListing(t *testing.T, body []byte) listing {
	t.Helper()
	var out listing
	if err := xml.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	return out
}

// seedFiles writes files (relative POSIX paths) into a storage's root.
func (hz *harness) seedFiles(t *testing.T, st *model.Storage, files ...string) {
	t.Helper()
	root := hz.rootOf(t, st)
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

func eq(t *testing.T, got, want []string, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

func TestListObjectsFlatIsRecursiveAndSorted(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, "b.txt", "a/deep/one.txt", "a/two.txt", "z.txt")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main?list-type=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	got := parseListing(t, rec.Body.Bytes())
	eq(t, got.keys(), []string{"a/deep/one.txt", "a/two.txt", "b.txt", "z.txt"}, "keys")
	if got.IsTruncated {
		t.Error("a four-key listing came back truncated")
	}
}

// ⚠⚠ The ordering trap. S3 sorts by the FULL key, so a folder sorts as if it
// already carried its trailing slash: '-' (0x2D) < '/' (0x2F), so `c-file.txt`
// comes BEFORE `c/inside.txt`. Sorting the raw names puts the folder first and
// produces a listing that looks sorted and is not — which makes a client's
// pagination skip entries.
func TestListObjectsSortsFoldersAsIfTheyEndedInASlash(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, "c-file.txt", "c/inside.txt", "c0.txt")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	got := parseListing(t, hz.do(t, key, http.MethodGet, "https://s3.filex.test/main?list-type=2").Body.Bytes())
	eq(t, got.keys(), []string{"c-file.txt", "c/inside.txt", "c0.txt"}, "keys")
}

func TestListObjectsWithDelimiterGroupsFolders(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, "top.txt", "docs/a.txt", "docs/sub/b.txt", "images/c.png")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	got := parseListing(t, hz.do(t, key, http.MethodGet,
		"https://s3.filex.test/main?list-type=2&delimiter=%2F").Body.Bytes())
	eq(t, got.keys(), []string{"top.txt"}, "keys")
	eq(t, got.prefixes(), []string{"docs/", "images/"}, "prefixes")

	// One level down, the prefix narrows and the nested folder appears.
	got = parseListing(t, hz.do(t, key, http.MethodGet,
		"https://s3.filex.test/main?list-type=2&delimiter=%2F&prefix=docs%2F").Body.Bytes())
	eq(t, got.keys(), []string{"docs/a.txt"}, "keys")
	eq(t, got.prefixes(), []string{"docs/sub/"}, "prefixes")
}

func TestListObjectsPaginates(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, "a.txt", "b.txt", "c.txt", "d.txt", "e.txt")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	var seen []string
	token := ""
	for page := 0; page < 10; page++ {
		url := "https://s3.filex.test/main?list-type=2&max-keys=2"
		if token != "" {
			url += "&continuation-token=" + token
		}
		got := parseListing(t, hz.do(t, key, http.MethodGet, url).Body.Bytes())
		seen = append(seen, got.keys()...)
		if !got.IsTruncated {
			token = ""
			break
		}
		if got.NextToken == "" {
			t.Fatal("a truncated page came back with no continuation token — the client cannot continue")
		}
		token = got.NextToken
	}
	if token != "" {
		t.Fatal("pagination did not terminate")
	}
	eq(t, seen, []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"}, "paged keys")
}

// V1 is NOT optional: clients that still use it see an empty bucket rather
// than an error if only V2 exists, and nothing in their logs says why.
func TestListObjectsV1UsesMarkerNotToken(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, "a.txt", "b.txt", "c.txt")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	got := parseListing(t, hz.do(t, key, http.MethodGet,
		"https://s3.filex.test/main?max-keys=2").Body.Bytes())
	eq(t, got.keys(), []string{"a.txt", "b.txt"}, "v1 first page")
	if !got.IsTruncated || got.NextMarker != "b.txt" {
		t.Fatalf("v1 truncated=%v nextMarker=%q, want true / b.txt", got.IsTruncated, got.NextMarker)
	}
	got = parseListing(t, hz.do(t, key, http.MethodGet,
		"https://s3.filex.test/main?marker=b.txt").Body.Bytes())
	eq(t, got.keys(), []string{"c.txt"}, "v1 second page")
}

// ⚠⚠ The leak this filter exists to stop: with a delimiter, a listing returns
// FOLDER NAMES, and filtering only the keys tells the caller that
// `clients/acme-merger/` exists. A confined credential must see neither the
// keys nor the names outside its subtree.
func TestConfinedKeySeesNeitherKeysNorFolderNamesOutsideIt(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st,
		"projects/acme/notes.txt",
		"projects/acme/sub/deep.txt",
		"clients/acme-merger/secret.txt",
		"top-secret.txt",
	)
	key := hz.key(t, u, protocolauth.IssueRequest{
		Label: "confined", Bucket: "main", Prefix: "projects/acme",
	})

	flat := parseListing(t, hz.do(t, key, http.MethodGet,
		"https://s3.filex.test/main?list-type=2").Body.Bytes())
	eq(t, flat.keys(), []string{"projects/acme/notes.txt", "projects/acme/sub/deep.txt"}, "confined keys")

	// And with a delimiter, the folder NAMES outside the confinement must not
	// appear either.
	grouped := parseListing(t, hz.do(t, key, http.MethodGet,
		"https://s3.filex.test/main?list-type=2&delimiter=%2F").Body.Bytes())
	for _, p := range grouped.prefixes() {
		if p == "clients/" {
			t.Fatalf("a confined key was shown the folder name %q — a folder name is information", p)
		}
	}
	if len(grouped.keys()) != 0 {
		t.Errorf("confined key saw root-level keys: %v", grouped.keys())
	}
}

// The ACL is the other half of the same filter, and it applies per entry.
func TestListObjectsFiltersByGrant(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, "granted/mine.txt", "granted/sub/deep.txt", "ungranted/theirs.txt")

	st.RBACEnabled = true
	if err := hz.store.UpdateStorage(context.Background(), st); err != nil {
		t.Fatalf("rbac on: %v", err)
	}
	if _, err := hz.store.CreateFileGrant(context.Background(), &model.FileGrant{
		StorageID: st.ID, PathPrefix: "granted", IsDir: true, UserID: u.ID, Level: "viewer",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	got := parseListing(t, hz.do(t, key, http.MethodGet,
		"https://s3.filex.test/main?list-type=2").Body.Bytes())
	eq(t, got.keys(), []string{"granted/mine.txt", "granted/sub/deep.txt"}, "granted keys")

	grouped := parseListing(t, hz.do(t, key, http.MethodGet,
		"https://s3.filex.test/main?list-type=2&delimiter=%2F").Body.Bytes())
	eq(t, grouped.prefixes(), []string{"granted/"}, "granted prefixes")
}

// A listing must carry the ETag filex ALREADY knows — the digest computed when
// the object was written — without hashing anything during the walk.
//
// ⚠ Measured through a real upload rather than by planting a cache entry: what
// matters is that the PUT path and the listing path agree on the key, and a
// test that plants the entry itself would pass with them disagreeing.
// (2026-08-16: `aws s3api list-objects-v2` returned `"ETag": ""` for objects it
// had just uploaded, so every client fell back to size-and-time comparison.)
func TestListingCarriesTheDigestFromTheUpload(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	body := []byte("listing etag payload")
	if rec := hz.put(t, key, "https://s3.filex.test/main/e/tagged.txt", body); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d", rec.Code)
	}
	want := hz.do(t, key, http.MethodHead, "https://s3.filex.test/main/e/tagged.txt").Header().Get("ETag")
	if want == "" {
		t.Fatal("HEAD returned no ETag")
	}

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main?list-type=2&prefix=e/")
	var out struct {
		Contents []struct {
			Key  string `xml:"Key"`
			ETag string `xml:"ETag"`
		} `xml:"Contents"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(out.Contents))
	}
	if out.Contents[0].ETag != want {
		t.Fatalf("listing ETag = %q, HEAD says %q — a client comparing the two sees a changed object",
			out.Contents[0].ETag, want)
	}
}
