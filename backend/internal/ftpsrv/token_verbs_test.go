package ftpsrv_test

// An API token used as the FTPS password carries its verbs into the session,
// exactly as on SFTP and WebDAV: `read` to list and download, `write` to create,
// change and move, `delete` to remove.

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brf-tech/filex/backend/internal/testutil"
)

func TestTokenVerbsBindTheSession(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "keep.txt", []byte("keep"))
	hz.writeFile(t, st, "gone.txt", []byte("gone"))
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}
	tok := func(scopes string) string { return testutil.NewAPIToken(t, hz.store, u.ID, scopes) }

	ro := hz.mustLogin(t, "ftp@example.com", tok("read"))
	r, err := ro.Retr("/main/keep.txt")
	if err != nil {
		t.Fatalf("read token: retr: %v", err)
	}
	got, _ := io.ReadAll(r)
	_ = r.Close()
	if string(got) != "keep" {
		t.Fatalf("read token: retr = %q", got)
	}
	if err := ro.MakeDir("/main/ro-dir"); err == nil || exists("ro-dir") {
		t.Errorf("read token created a folder (err=%v)", err)
	}
	if err := ro.Stor("/main/ro-new.txt", strings.NewReader("x")); err == nil || exists("ro-new.txt") {
		t.Errorf("read token wrote a file (err=%v)", err)
	}
	if err := ro.Rename("/main/keep.txt", "/main/moved.txt"); err == nil || !exists("keep.txt") {
		t.Errorf("read token moved a file (err=%v)", err)
	}
	if err := ro.Delete("/main/keep.txt"); err == nil || !exists("keep.txt") {
		t.Errorf("read token deleted a file (err=%v)", err)
	}

	rw := hz.mustLogin(t, "ftp@example.com", tok("read,write"))
	if err := rw.MakeDir("/main/rw-dir"); err != nil || !exists("rw-dir") {
		t.Errorf("read,write token: mkd: %v", err)
	}
	if err := rw.Delete("/main/gone.txt"); err == nil || !exists("gone.txt") {
		t.Errorf("read,write token deleted a file (err=%v)", err)
	}

	rd := hz.mustLogin(t, "ftp@example.com", tok("read,delete"))
	if err := rd.Delete("/main/gone.txt"); err != nil || exists("gone.txt") {
		t.Errorf("read,delete token: dele: %v", err)
	}
	if err := rd.MakeDir("/main/rd-dir"); err == nil || exists("rd-dir") {
		t.Errorf("read,delete token created a folder (err=%v)", err)
	}

	blind := hz.mustLogin(t, "ftp@example.com", tok("write,delete"))
	if r, err := blind.Retr("/main/keep.txt"); err == nil {
		b, _ := io.ReadAll(r)
		_ = r.Close()
		if len(b) > 0 {
			t.Error("a token without read downloaded a file")
		}
	}
}
