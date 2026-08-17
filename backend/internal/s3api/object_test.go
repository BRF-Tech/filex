package s3api_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// Reading an object. The cases here are the ones a real client hits on its
// first day: a whole GET, a ranged GET, a HEAD whose headers must agree with
// the GET, and the two 404s that must not be 403s.

func (hz *harness) writeFile(t *testing.T, st *model.Storage, key string, body []byte) {
	t.Helper()
	p := filepath.Join(hz.rootOf(t, st), filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func (hz *harness) get(t *testing.T, key *protocolauth.IssuedKey, url string, headers map[string]string) *http.Response {
	t.Helper()
	r := signed(t, key, http.MethodGet, url, hz.at)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := recorderFor(hz, r)
	return rec.Result()
}

func TestGetObjectReturnsTheBytes(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	body := []byte("the quick brown fox jumps over the lazy dog")
	hz.writeFile(t, st, "docs/report.txt", body)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/docs/report.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != string(body) {
		t.Fatalf("body = %q, want %q", got, body)
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Error("no Accept-Ranges — clients fall back to whole-object downloads without it")
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("no ETag; restic and rclone treat a missing one as a broken server")
	}
}

// A HEAD that disagrees with the GET after it is worse than no HEAD at all.
func TestHeadObjectAgreesWithGetAndHasNoBody(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "a.bin", []byte("0123456789"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	get := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/a.bin")
	head := hz.do(t, key, http.MethodHead, "https://s3.filex.test/main/a.bin")

	if head.Code != http.StatusOK {
		t.Fatalf("HEAD = %d", head.Code)
	}
	if head.Body.Len() != 0 {
		t.Errorf("HEAD returned a body of %d bytes", head.Body.Len())
	}
	// ⚠⚠ Content-Length is in this list because it was MISSING from it, and the
	// gap cost a working endpoint: a HEAD carries no body, so Go answers
	// `Content-Length: 0` unless the handler sets one — and every client reads
	// that as "the object is empty". rclone uploaded correctly and then deleted
	// its own upload with "corrupted on transfer: sizes differ src 17 vs dst 0"
	// (2026-08-16, found by the real-client E2E, not by this suite).
	if got := head.Header().Get("Content-Length"); got != "10" {
		t.Errorf("HEAD Content-Length = %q, want 10 — clients learn the object size from this header alone", got)
	}
	for _, hdr := range []string{"ETag", "Content-Type", "Last-Modified", "Accept-Ranges", "Content-Length"} {
		if g, h := get.Header().Get(hdr), head.Header().Get(hdr); g != h {
			t.Errorf("%s: GET %q, HEAD %q — they must agree", hdr, g, h)
		}
	}
}

func TestGetObjectRange(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	body := []byte("0123456789abcdefghij") // 20 bytes
	hz.writeFile(t, st, "r.bin", body)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	cases := []struct {
		header string
		want   string
		cr     string
	}{
		{"bytes=0-4", "01234", "bytes 0-4/20"},
		{"bytes=5-9", "56789", "bytes 5-9/20"},
		{"bytes=15-", "fghij", "bytes 15-19/20"},
		{"bytes=-3", "hij", "bytes 17-19/20"},
		// An end past EOF is clamped, not refused — every HTTP server does
		// this and a client that asked for "the rest" expects it.
		{"bytes=18-999", "ij", "bytes 18-19/20"},
	}
	for _, c := range cases {
		t.Run(c.header, func(t *testing.T) {
			resp := hz.get(t, key, "https://s3.filex.test/main/r.bin", map[string]string{"Range": c.header})
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusPartialContent {
				t.Fatalf("status = %d, want 206", resp.StatusCode)
			}
			got, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if string(got) != c.want {
				t.Errorf("body = %q, want %q", got, c.want)
			}
			if got := resp.Header.Get("Content-Range"); got != c.cr {
				t.Errorf("Content-Range = %q, want %q", got, c.cr)
			}
		})
	}
}

// ⚠ Past the end is 416, not an empty 206. A client that gets 206 here
// concludes the object shrank; restic calls that repository damage.
func TestRangePastTheEndIs416(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "r.bin", []byte("0123456789"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	resp := hz.get(t, key, "https://s3.filex.test/main/r.bin", map[string]string{"Range": "bytes=50-60"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("status = %d, want 416", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes */10" {
		t.Errorf("Content-Range = %q, want bytes */10 — clients read the real size from it", got)
	}
}

// A range header we do not understand must be IGNORED (whole object), never
// answered with bytes from the wrong offsets.
func TestUnsupportedRangeFallsBackToTheWholeObject(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	body := []byte("0123456789")
	hz.writeFile(t, st, "r.bin", body)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	for _, hdr := range []string{"bytes=0-2,5-7", "items=0-2", "nonsense"} {
		resp := hz.get(t, key, "https://s3.filex.test/main/r.bin", map[string]string{"Range": hdr})
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || string(got) != string(body) {
			t.Errorf("Range %q = %d / %q, want 200 and the whole object", hdr, resp.StatusCode, got)
		}
	}
}

func TestConditionalRequests(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "c.txt", []byte("hello"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	first := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/c.txt")
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Skip("this storage driver reports no ETag; the conditional path needs one")
	}

	resp := hz.get(t, key, "https://s3.filex.test/main/c.txt", map[string]string{"If-None-Match": etag})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match on the current ETag = %d, want 304", resp.StatusCode)
	}
	resp = hz.get(t, key, "https://s3.filex.test/main/c.txt", map[string]string{"If-Match": `"nope"`})
	resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Errorf("If-Match on a stale ETag = %d, want 412", resp.StatusCode)
	}
}

// The two 404s that must not be 403s. Telling a caller "forbidden" confirms
// the object is there.
func TestUnreadableObjectIsNoSuchKey(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "projects/acme/ok.txt", []byte("ok"))
	hz.writeFile(t, st, "secrets/theirs.txt", []byte("secret"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main", Prefix: "projects/acme"})

	if rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/projects/acme/ok.txt"); rec.Code != http.StatusOK {
		t.Fatalf("its own object = %d, want 200", rec.Code)
	}
	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/secrets/theirs.txt")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outside the confinement = %d, want 404", rec.Code)
	}
	if code := errorCode(t, rec.Body.Bytes()); code != "NoSuchKey" {
		t.Errorf("code = %q, want NoSuchKey (AccessDenied would confirm it exists)", code)
	}
	// A missing key answers the same way, which is the point: the two are
	// indistinguishable from outside.
	rec = hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/projects/acme/missing.txt")
	if rec.Code != http.StatusNotFound || errorCode(t, rec.Body.Bytes()) != "NoSuchKey" {
		t.Errorf("missing key = %d / %s", rec.Code, errorCode(t, rec.Body.Bytes()))
	}
}

// A folder is not an object: S3 has no directories, only prefixes.
func TestGetOnAFolderIsNoSuchKey(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "docs/a.txt", []byte("a"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/docs")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET on a folder = %d, want 404", rec.Code)
	}
}

// The ETag a client stores must be the one it gets back, or every subsequent
// conditional request misses.
func TestETagIsStableAndQuoted(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	body := []byte("stable content")
	hz.writeFile(t, st, "e.txt", body)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	a := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/e.txt").Header().Get("ETag")
	b := hz.do(t, key, http.MethodHead, "https://s3.filex.test/main/e.txt").Header().Get("ETag")
	if a == "" || a != b {
		t.Fatalf("ETag GET=%q HEAD=%q, want equal and non-empty", a, b)
	}
	if !strings.HasPrefix(a, `"`) || !strings.HasSuffix(a, `"`) {
		t.Errorf("ETag %q is not quoted; some clients compare it literally and never match", a)
	}

	// ⚠ And it must be the REAL MD5, not something that merely looks like one.
	// The local driver reports no ETag at all, so this value is computed — and
	// a computed value that is not the digest is exactly the wrong-ETag failure
	// that makes restic call a repository damaged.
	sum := md5.Sum(body)
	want := `"` + hex.EncodeToString(sum[:]) + `"`
	if a != want {
		t.Fatalf("ETag = %s, want the MD5 %s", a, want)
	}

	// Changing the bytes must change it: a cached digest that survives a
	// rewrite is a stale ETag, which is the same failure wearing a hat.
	hz.writeFile(t, st, "e.txt", []byte("different content entirely"))
	after := hz.do(t, key, http.MethodHead, "https://s3.filex.test/main/e.txt").Header().Get("ETag")
	if after == a {
		t.Fatalf("ETag did not change after the object did: %s", after)
	}
	_ = context.Background
}
