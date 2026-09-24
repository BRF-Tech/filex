package nfssrv_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/syspath"
)

// TestInternalDirsAreNotWritable: nothing is created, written or renamed into
// filex's own directories over NFS.
func TestInternalDirsAreNotWritable(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "doc.txt", []byte("x"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	for _, d := range syspath.Dirs() {
		if _, err := target.Mkdir("/"+d, 0o755); err == nil {
			t.Errorf("mkdir %s succeeded", d)
		}
		if wr, err := target.OpenFile("/"+d+"/x.txt", 0o644); err == nil {
			_, _ = wr.Write([]byte("x"))
			_ = wr.Close()
			t.Errorf("a write into %s was accepted", d)
		}
		if err := target.Rename("/doc.txt", "/"+d); err == nil {
			t.Errorf("a rename to %s succeeded", d)
		}
	}
	root := hz.rootOf(t, st)
	for _, d := range syspath.Dirs() {
		if _, err := os.Stat(filepath.Join(root, d)); err == nil {
			t.Errorf("%s exists on the storage", d)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "doc.txt")); err != nil {
		t.Errorf("the document moved: %v", err)
	}
}
