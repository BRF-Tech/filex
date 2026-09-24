package s3api_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// TestInternalTreesAreNotWritable: no PUT and no server-side copy lands a key
// under filex's own directories.
func TestInternalTreesAreNotWritable(t *testing.T) {
	hz := newHarness(t, false)
	u := hz.user(t, "s3@example.com", model.RoleUser)
	st := hz.storage(t, "main")
	hz.seedFiles(t, st, "doc.txt")
	key := hz.key(t, u, protocolauth.IssueRequest{Label: "k"})

	for _, d := range syspath.Dirs() {
		if rec := hz.put(t, key, "https://s3.filex.test/main/"+d+"/x.txt", []byte("x")); rec.Code == http.StatusOK {
			t.Errorf("PUT into %s = 200", d)
		}
		if rec := hz.copy(t, key, "https://s3.filex.test/main/"+d+"/doc.txt", "/main/doc.txt"); rec.Code == http.StatusOK {
			t.Errorf("a copy into %s = 200", d)
		}
	}
	root := hz.rootOf(t, st)
	for _, d := range syspath.Dirs() {
		if _, err := os.Stat(filepath.Join(root, d)); err == nil {
			t.Errorf("%s exists on the storage", d)
		}
	}
}
