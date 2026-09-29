package sftpsrv_test

// An API token used as the SFTP password carries its verbs into the session:
// `read` to list and download, `write` to create, change and move, `delete` to
// remove — the same verbs, with the same meaning, as on /dav and /api/ai.

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestTokenVerbsBindTheSession(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "keep.txt", []byte("keep"))
	hz.writeFile(t, st, "gone.txt", []byte("gone"))
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}

	// read: lists and downloads, changes nothing.
	ro := hz.mustDial(t, "sftp@example.com", hz.token(t, u, "read"))
	f, err := ro.Open("/main/keep.txt")
	if err != nil {
		t.Fatalf("read token: open: %v", err)
	}
	got, _ := io.ReadAll(f)
	_ = f.Close()
	if string(got) != "keep" {
		t.Fatalf("read token: read %q", got)
	}
	if err := ro.Mkdir("/main/ro-dir"); err == nil || exists("ro-dir") {
		t.Errorf("read token created a folder (err=%v)", err)
	}
	if w, err := ro.Create("/main/ro-new.txt"); err == nil {
		_, _ = w.Write([]byte("x"))
		_ = w.Close()
	}
	if exists("ro-new.txt") {
		t.Error("read token wrote a file")
	}
	if err := ro.Rename("/main/keep.txt", "/main/moved.txt"); err == nil || !exists("keep.txt") {
		t.Errorf("read token moved a file (err=%v)", err)
	}
	if err := ro.Remove("/main/keep.txt"); err == nil || !exists("keep.txt") {
		t.Errorf("read token deleted a file (err=%v)", err)
	}

	// write without delete.
	rw := hz.mustDial(t, "sftp@example.com", hz.token(t, u, "read,write"))
	if err := rw.Mkdir("/main/rw-dir"); err != nil || !exists("rw-dir") {
		t.Errorf("read,write token: mkdir: %v", err)
	}
	if err := rw.Remove("/main/gone.txt"); err == nil || !exists("gone.txt") {
		t.Errorf("read,write token deleted a file (err=%v)", err)
	}

	// delete without write.
	rd := hz.mustDial(t, "sftp@example.com", hz.token(t, u, "read,delete"))
	if err := rd.Remove("/main/gone.txt"); err != nil || exists("gone.txt") {
		t.Errorf("read,delete token: remove: %v", err)
	}
	if err := rd.Mkdir("/main/rd-dir"); err == nil || exists("rd-dir") {
		t.Errorf("read,delete token created a folder (err=%v)", err)
	}

	// no read: nothing to open.
	blind := hz.mustDial(t, "sftp@example.com", hz.token(t, u, "write,delete"))
	if f, err := blind.Open("/main/keep.txt"); err == nil {
		b, rerr := io.ReadAll(f)
		_ = f.Close()
		if rerr == nil && len(b) > 0 {
			t.Error("a token without read downloaded a file")
		}
	}
}
