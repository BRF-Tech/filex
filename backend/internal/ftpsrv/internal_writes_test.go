package ftpsrv_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/syspath"
)

// TestInternalDirsAreNotWritable: nothing is created, uploaded or renamed into
// filex's own directories over FTP.
func TestInternalDirsAreNotWritable(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "doc.txt", []byte("x"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	for _, d := range syspath.Dirs() {
		if err := c.MakeDir("/main/" + d); err == nil {
			t.Errorf("mkdir %s succeeded", d)
		}
		if err := c.Stor("/main/"+d+"/x.txt", strings.NewReader("x")); err == nil {
			t.Errorf("an upload into %s was accepted", d)
		}
		if err := c.Rename("/main/doc.txt", "/main/"+d); err == nil {
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
