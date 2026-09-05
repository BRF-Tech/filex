package s3api_test

// filex's own bookkeeping trees must not be reachable through the S3 gateway.
//
// /dav, /sftp, /ftp and /nfs have each hidden `.versions`, `.thumbs` and
// `.filex-trash` since they were written, and so does the browser listing.
// The S3 gateway had no such filter on any verb, which made it the one surface
// where `.versions/42/1` was both listed and readable.
//
// That was always wrong; the pre-write overwrite guard makes it urgent.
// Before the guard, `.versions/` held a handful of text-editor snapshots.
// After it, it holds a copy of EVERY file any surface has ever replaced — so
// leaving it reachable hands any S3-key holder the prior contents of files
// whose folders they may since have lost access to.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
)

// internalKeys are the trees no protocol exposes, one probe key in each.
var internalKeys = []string{
	".versions/42/1",
	".thumbs/abc.jpg",
	".filex-trash/deleted.txt",
}

// They must not appear in a listing...
func TestInternalTreesAreNotListed(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, append([]string{"visible.txt"}, internalKeys...)...)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main?list-type=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	got := parseListing(t, rec.Body.Bytes())
	eq(t, got.keys(), []string{"visible.txt"}, "only real user files may be listed")
}

// ...nor be readable by a caller who already knows the key. A filter that only
// covered listings would be security theatre: these keys are guessable.
func TestInternalTreesAreNotReadable(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, internalKeys...)
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	for _, k := range internalKeys {
		rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/"+k)
		if rec.Code == http.StatusOK {
			t.Errorf("GET %s = 200: an internal tree was readable through the gateway", k)
		}
		rec = hz.do(t, key, http.MethodHead, "https://s3.filex.test/main/"+k)
		if rec.Code == http.StatusOK {
			t.Errorf("HEAD %s = 200: an internal tree was readable through the gateway", k)
		}
	}
}

// A nested spelling must be caught too — the check is per path segment, not a
// prefix match, so `docs/.versions/x` is refused just like `.versions/x`.
func TestInternalTreesAreHiddenAtAnyDepth(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, "docs/report.txt", "docs/.versions/7/1")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	rec := hz.do(t, key, http.MethodGet, "https://s3.filex.test/main?list-type=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rec.Code, rec.Body.String())
	}
	for _, k := range parseListing(t, rec.Body.Bytes()).keys() {
		if strings.Contains(k, ".versions") {
			t.Errorf("a nested internal tree was listed: %s", k)
		}
	}

	rec = hz.do(t, key, http.MethodGet, "https://s3.filex.test/main/docs/.versions/7/1")
	if rec.Code == http.StatusOK {
		t.Error("a nested internal tree was readable through the gateway")
	}
}
