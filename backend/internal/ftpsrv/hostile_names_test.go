package ftpsrv_test

// The shared hostile names (internal/testutil/hostilenames, task #238) over
// FTPS: every name a filex door must accept goes in and comes back with its
// own bytes, every name no door may create is refused with 553, a name the
// disk cannot hold is refused without the server's path in the reply, and a
// name an FTP listing cannot carry is left out of it.

import (
	"errors"
	"io"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	goftp "github.com/jlaffaye/ftp"

	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// replyCode is the FTP reply code an error carries, 0 when it carries none.
func replyCode(err error) int {
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return 0
}

// diskNames lists a folder of the storage's own disk: the names exactly as
// they were stored.
func diskNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.Name()] = true
	}
	return out
}

func TestHostileNamesSurvive(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	c := hz.mustLogin(t, "ftp@example.com", testPassword)
	for _, dir := range []string{"/main/f", "/main/d"} {
		if err := c.MakeDir(dir); err != nil {
			t.Fatalf("mkd %s: %v", dir, err)
		}
	}

	// Every name a door must accept travels in an FTP command: none holds a
	// line break (control characters are refused names, internal/leafname),
	// and a space at either end is kept by the command parser.
	all := hostilenames.ForLocalDisk()
	for _, n := range all {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			p := "/main/f/" + n.Name
			body := "body of " + n.ID
			if err := c.Stor(p, strings.NewReader(body)); err != nil {
				t.Fatalf("stor %q: %v", n.Name, err)
			}
			r, err := c.Retr(p)
			if err != nil {
				t.Fatalf("retr: %v", err)
			}
			got, err := io.ReadAll(r)
			r.Close()
			if err != nil || string(got) != body {
				t.Fatalf("read back %q (%v), want %q", got, err, body)
			}
			// Away and back: the name is a rename's target too. (A short
			// name in between: the 255-byte name plus a suffix would be
			// over the disk's limit.)
			away := "/main/f/renamed-" + n.ID
			if err := c.Rename(p, away); err != nil {
				t.Fatalf("rename away from %q: %v", n.Name, err)
			}
			if err := c.Rename(away, p); err != nil {
				t.Fatalf("rename back to %q: %v", n.Name, err)
			}
			if err := c.MakeDir("/main/d/" + n.Name); err != nil {
				t.Fatalf("mkd %q: %v", n.Name, err)
			}
		})
	}

	// Side by side in one folder, each with its own bytes: in MLSD (List),
	// in NLST, and on the disk the storage keeps.
	for _, dir := range []string{"f", "d"} {
		entries, err := c.List("/main/" + dir)
		if err != nil {
			t.Fatalf("mlsd %s: %v", dir, err)
		}
		listed := map[string]bool{}
		for _, e := range entries {
			listed[e.Name] = true
		}
		nlst, err := c.NameList("/main/" + dir)
		if err != nil {
			t.Fatalf("nlst %s: %v", dir, err)
		}
		// NLST answers paths relative to the working directory
		// ("main/f/<name>"); a name holds no slash, so its base is the name.
		inNlst := map[string]bool{}
		for _, line := range nlst {
			inNlst[path.Base(line)] = true
		}
		disk := diskNames(t, filepath.Join(root, dir))
		for _, n := range all {
			if !listed[n.Name] {
				t.Errorf("%s/%s: %q is not in MLSD", dir, n.ID, n.Name)
			}
			if !inNlst[n.Name] {
				t.Errorf("%s/%s: %q is not in NLST", dir, n.ID, n.Name)
			}
			if !disk[n.Name] {
				t.Errorf("%s/%s: %q is not on disk under its own bytes", dir, n.ID, n.Name)
			}
		}
	}
}

// A name holding a line break - legal on a Linux disk, made on the storage
// itself (no filex door gives one) - is left out of every listing: written
// into one, it would end its entry early and start another line (a stray
// entry, or on STAT's control connection a line read as a reply).
func TestNamesWithALineBreakAreLeftOutOfListings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a line break cannot be in a Windows file name")
	}
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	n := hostilenames.Get("control-newline")
	hz.writeFile(t, st, n.Name, []byte("planted"))
	hz.writeFile(t, st, "visible.txt", []byte("seen"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	fragments := strings.Split(n.Name, "\n")
	check := func(how string, got []string) {
		t.Helper()
		seen := false
		for _, name := range got {
			name = path.Base(name)
			if name == "visible.txt" {
				seen = true
			}
			for _, f := range fragments {
				if name == f || strings.ContainsAny(name, "\r\n") {
					t.Errorf("%s: a piece of the line-broken name is an entry: %q", how, name)
				}
			}
		}
		if !seen {
			t.Errorf("%s: the ordinary file is missing: %v", how, got)
		}
	}
	entries, err := c.List("/main")
	if err != nil {
		t.Fatalf("mlsd: %v", err)
	}
	check("MLSD", entryNames(entries))
	nlst, err := c.NameList("/main")
	if err != nil {
		t.Fatalf("nlst: %v", err)
	}
	check("NLST", nlst)
	// The control connection is still in step: the next command answers as
	// itself.
	if err := c.NoOp(); err != nil {
		t.Fatalf("the reply stream is out of step: %v", err)
	}
}

// The names no door may create (internal/leafname): STOR and RNTO answer 553
// (file name not allowed), MKD refuses, nothing reaches the disk.
func TestHostileNamesRefused(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "src.txt", []byte("source"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	for _, n := range hostilenames.Refused() {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			if strings.ContainsAny(n.Name, "\r\n") {
				// A CR or a LF ends an FTP command (RFC 959): no client can
				// send this name at all. A tab travels, and is refused below.
				// What FTP does with such a name that already exists is
				// TestNamesWithALineBreakAreLeftOutOfListings.
				t.Skip("a line break cannot travel in an FTP command")
			}
			p := "/main/" + n.Name
			// `..` never reaches the name rule: the library resolves the path
			// to the virtual root first, which refuses every change with 550.
			want := goftp.StatusBadFileName
			if n.ID == "dot-dot" {
				want = goftp.StatusFileUnavailable
			}
			if err := c.Stor(p, strings.NewReader("x")); replyCode(err) != want {
				t.Errorf("stor: %v, want %d", err, want)
			}
			if err := c.MakeDir(p); err == nil {
				t.Error("mkd: accepted")
			}
			if err := c.Rename("/main/src.txt", p); replyCode(err) != want {
				t.Errorf("rnto: %v, want %d", err, want)
			}
			if disk := diskNames(t, root); disk[n.Name] || !disk["src.txt"] {
				t.Errorf("the disk holds %v", disk)
			}
		})
	}
}

// A name the disk cannot hold (over 255 bytes on ext4 and APFS): 553 for a
// transfer and a rename, and no reply carries the server's path - ftpserverlib
// prints the error's text into the reply word for word.
func TestHostileNamesTooLong(t *testing.T) {
	long := hostilenames.TooLongForLocalDisk()
	if len(long) == 0 {
		t.Skip("this OS's disk holds every name in the list")
	}
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "src.txt", []byte("source"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	leaks := func(err error) bool {
		msg := err.Error()
		return strings.Contains(msg, root) || strings.Contains(msg, filepath.ToSlash(root))
	}
	for _, n := range long {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			p := "/main/" + n.Name
			err := c.Stor(p, strings.NewReader("x"))
			if replyCode(err) != goftp.StatusBadFileName || !strings.Contains(err.Error(), "name too long") || leaks(err) {
				t.Errorf("stor: %v, want 553 \"name too long\" and no server path", err)
			}
			if err := c.MakeDir(p); err == nil || leaks(err) {
				t.Errorf("mkd: %v, want a refusal without the server's path", err)
			}
			if err := c.Rename("/main/src.txt", p); replyCode(err) != goftp.StatusBadFileName || leaks(err) {
				t.Errorf("rnto: %v, want 553 without the server's path", err)
			}
		})
	}
}

// A file that ALREADY has a name no door may give - a tab - made on the
// storage itself, or over a protocol before the rule, stays a file: listed,
// read, overwritten and deleted like any other (a tab travels in an FTP
// command and in a listing line). Only a NEW name is refused.
func TestExistingRefusedNamesStayUsable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a tab cannot be in a Windows file name")
	}
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	n := hostilenames.Get("control-tab")
	hz.writeFile(t, st, n.Name, []byte("old"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)
	p := "/main/" + n.Name

	entries, err := c.List("/main")
	if err != nil {
		t.Fatalf("mlsd: %v", err)
	}
	listed := false
	for _, name := range entryNames(entries) {
		listed = listed || name == n.Name
	}
	if !listed {
		t.Fatalf("%q is not in the listing: %v", n.Name, entryNames(entries))
	}
	r, err := c.Retr(p)
	if err != nil {
		t.Fatalf("retr: %v", err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(got) != "old" {
		t.Fatalf("read %q (%v)", got, err)
	}
	if err := c.Stor(p, strings.NewReader("new")); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, n.Name)); err != nil || string(got) != "new" {
		t.Fatalf("on disk after the overwrite %q (%v)", got, err)
	}
	if err := c.Delete(p); err != nil {
		t.Fatalf("dele: %v", err)
	}
	if diskNames(t, root)[n.Name] {
		t.Fatal("still on disk after the delete")
	}
}

// RMD removes an EMPTY folder (to the trash, as before). A folder with
// something in it answers 550 "directory not empty" and loses nothing; RMD on
// a file is refused too. DELE of a file is unchanged.
func TestRmdRemovesOnlyAnEmptyFolder(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "full/a.txt", []byte("a"))
	hz.writeFile(t, st, "full/sub/b.txt", []byte("b"))
	hz.writeFile(t, st, "file.txt", []byte("f"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)
	if err := c.MakeDir("/main/empty"); err != nil {
		t.Fatalf("mkd: %v", err)
	}

	err := c.RemoveDir("/main/full")
	if replyCode(err) != goftp.StatusFileUnavailable || !strings.Contains(err.Error(), "directory not empty") {
		t.Errorf("rmd of a full folder: %v, want 550 \"directory not empty\"", err)
	}
	for _, rel := range []string{"full/a.txt", "full/sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s went: %v", rel, err)
		}
	}
	if err := c.RemoveDir("/main/file.txt"); err == nil {
		t.Error("rmd of a file went through")
	}
	if _, err := os.Stat(filepath.Join(root, "file.txt")); err != nil {
		t.Errorf("the file went: %v", err)
	}
	if err := c.RemoveDir("/main/empty"); err != nil {
		t.Fatalf("rmd of an empty folder: %v", err)
	}
	if diskNames(t, root)["empty"] {
		t.Error("the empty folder is still there")
	}
	if err := c.Delete("/main/file.txt"); err != nil {
		t.Fatalf("dele of a file: %v", err)
	}
}

// DELE is a FILE's delete. On a folder, empty or not, it answers 550 "is a
// directory" and removes nothing: a folder goes only by RMD, and only empty
// (the owner's decision for 0.57). DELE of a file is unchanged.
func TestDeleOnAFolderIsRefused(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "full/a.txt", []byte("a"))
	hz.writeFile(t, st, "full/sub/b.txt", []byte("b"))
	hz.writeFile(t, st, "file.txt", []byte("f"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)
	if err := c.MakeDir("/main/empty"); err != nil {
		t.Fatalf("mkd: %v", err)
	}

	for _, dir := range []string{"full", "empty"} {
		err := c.Delete("/main/" + dir)
		if replyCode(err) != goftp.StatusFileUnavailable || !strings.Contains(err.Error(), "is a directory") {
			t.Errorf("dele of the folder %s: %v, want 550 \"is a directory\"", dir, err)
		}
		if !diskNames(t, root)[dir] {
			t.Errorf("the folder %s went", dir)
		}
	}
	for _, rel := range []string{"full/a.txt", "full/sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s went: %v", rel, err)
		}
	}
	if err := c.Delete("/main/file.txt"); err != nil {
		t.Fatalf("dele of a file: %v", err)
	}
	if diskNames(t, root)["file.txt"] {
		t.Error("the file is still there")
	}
}
