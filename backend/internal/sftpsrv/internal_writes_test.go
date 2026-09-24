package sftpsrv_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brf-tech/filex/backend/internal/syspath"
)

// TestInternalDirsAreNotWritable: nothing is created, uploaded or renamed into
// filex's own directories over SFTP — every one of them, from the one list.
func TestInternalDirsAreNotWritable(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "doc.txt", []byte("x"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	for _, d := range syspath.Dirs() {
		if err := cl.Mkdir("/main/" + d); err == nil {
			t.Errorf("mkdir %s succeeded", d)
		}
		if w, err := cl.Create("/main/" + d + "/x.txt"); err == nil {
			_, _ = w.Write([]byte("x"))
			_ = w.Close()
			t.Errorf("an upload into %s was accepted", d)
		}
		if err := cl.Rename("/main/doc.txt", "/main/"+d); err == nil {
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
