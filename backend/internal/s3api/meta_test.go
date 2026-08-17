package s3api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// Modification times, carried by `x-amz-meta-mtime`.
//
// ⚠⚠ This is not a nicety. rclone decides whether a file needs copying by
// comparing timestamps: a server that stamps every upload with "now" reports
// every file as changed, so the next sync copies the whole tree again — and
// rclone then tries to fix the timestamp with a copy-onto-self, which is the
// request that failed the first real-client run (2026-08-16).

// srcMtime is a fixed, recognisable instant — a wrong answer here is usually
// "now", and a time far from now makes that obvious.
var srcMtime = time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)

func TestPutObjectCarriesTheClientsModificationTime(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	req := signedBody(t, key, http.MethodPut, "https://s3.filex.test/main/dated.txt", []byte("payload"), hz.at)
	// rclone's spelling: fractional seconds.
	req.Header.Set("X-Amz-Meta-Mtime", "1614834367.0")
	if rec := recorderFor(hz, req); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d: %s", rec.Code, rec.Body.String())
	}

	info, err := os.Stat(filepath.Join(hz.rootOf(t, st), "dated.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.ModTime().UTC(); !got.Equal(srcMtime) {
		t.Fatalf("file mtime = %s, want %s — the client's timestamp was dropped", got, srcMtime)
	}

	// And the endpoint reports it back, because that is what the client reads
	// to decide the file is already up to date.
	head := hz.do(t, key, http.MethodHead, "https://s3.filex.test/main/dated.txt")
	if got := head.Header().Get("Last-Modified"); got != srcMtime.Format(http.TimeFormat) {
		t.Fatalf("Last-Modified = %q, want %q", got, srcMtime.Format(http.TimeFormat))
	}
}

// ⚠ A copy onto itself is the ONLY request S3 has for changing metadata, and
// rclone uses it after an upload whose timestamp it wants to correct. Refusing
// it made every `rclone sync` fail on files it had just transferred correctly.
func TestSelfCopyWithReplaceSetsTheModificationTime(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "a.txt", []byte("unchanged bytes"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	req := signedBody(t, key, http.MethodPut, "https://s3.filex.test/main/a.txt", nil, hz.at)
	req.Header.Set("X-Amz-Copy-Source", "/main/a.txt")
	req.Header.Set("X-Amz-Metadata-Directive", "REPLACE")
	req.Header.Set("X-Amz-Meta-Mtime", "1614834367")
	rec := recorderFor(hz, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("self-copy with REPLACE = %d: %s", rec.Code, rec.Body.String())
	}

	p := filepath.Join(hz.rootOf(t, st), "a.txt")
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.ModTime().UTC(); !got.Equal(srcMtime) {
		t.Fatalf("mtime after the metadata copy = %s, want %s", got, srcMtime)
	}
	// The bytes must be untouched: this request moves metadata, not content.
	body, err := os.ReadFile(p)
	if err != nil || string(body) != "unchanged bytes" {
		t.Fatalf("content = %q (%v) — a metadata copy rewrote the object", body, err)
	}
}

// Without REPLACE the refusal stands: a copy that changes nothing is a client
// bug, and S3 says so too.
func TestSelfCopyWithoutReplaceIsStillRefused(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "a.txt", []byte("x"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	if rec := hz.copy(t, key, "https://s3.filex.test/main/a.txt", "/main/a.txt"); rec.Code != http.StatusBadRequest {
		t.Fatalf("self-copy without REPLACE = %d, want 400", rec.Code)
	}
}

// A metadata copy is a WRITE. A viewer must not be able to restamp a file, and
// a confined key must not reach outside its prefix to do it.
func TestSelfCopyMetadataStillNeedsWriteAccess(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "elsewhere/a.txt", []byte("not yours"))
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "confined", Bucket: "main", Prefix: "mine"})

	req := signedBody(t, key, http.MethodPut, "https://s3.filex.test/main/elsewhere/a.txt", nil, hz.at)
	req.Header.Set("X-Amz-Copy-Source", "/main/elsewhere/a.txt")
	req.Header.Set("X-Amz-Metadata-Directive", "REPLACE")
	req.Header.Set("X-Amz-Meta-Mtime", "1614834367")
	if rec := recorderFor(hz, req); rec.Code != http.StatusForbidden {
		t.Fatalf("metadata copy outside the confinement = %d, want 403", rec.Code)
	}

	info, err := os.Stat(filepath.Join(hz.rootOf(t, st), "elsewhere", "a.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.ModTime().UTC().Equal(srcMtime) {
		t.Fatal("the timestamp was changed by a caller with no write access")
	}
}

// ⚠ A nonsense timestamp is ignored rather than written: a file dated 1970 or
// 2400 sorts wrongly for ever, and the bytes are still correct without it.
func TestNonsenseMtimeHeadersAreIgnoredNotStored(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	for i, raw := range []string{"", "   ", "not-a-number", "-1", "0", "99999999999", "NaN"} {
		name := "bad" + string(rune('a'+i)) + ".txt"
		req := signedBody(t, key, http.MethodPut, "https://s3.filex.test/main/"+name, []byte("x"), hz.at)
		if raw != "" {
			req.Header.Set("X-Amz-Meta-Mtime", raw)
		}
		if rec := recorderFor(hz, req); rec.Code != http.StatusOK {
			t.Fatalf("PUT with mtime %q = %d — a bad timestamp must not fail a good upload", raw, rec.Code)
		}
		info, err := os.Stat(filepath.Join(hz.rootOf(t, st), name))
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if time.Since(info.ModTime()) > time.Hour {
			t.Fatalf("mtime %q was applied: file is dated %s", raw, info.ModTime())
		}
	}
}
