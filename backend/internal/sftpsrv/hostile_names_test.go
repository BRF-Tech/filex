package sftpsrv_test

// The shared hostile names (internal/testutil/hostilenames, task #238) over
// SFTP: every name a filex door must accept goes in and comes back with its
// own bytes, every name no door may create is refused, a name the disk cannot
// hold is refused as "name too long" without the server's path in the
// answer.

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/brf-tech/filex/backend/internal/sftpsrv"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// onDisk lists a folder of the storage's own disk: the names exactly as they
// were stored, not as a client reads them back.
func onDisk(t *testing.T, dir string) map[string]bool {
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

// putFile uploads body to p and reports the first error of the open, the
// write or the close (an upload lands on the storage at the close).
func putFile(cl *sftp.Client, p, body string) error {
	w, err := cl.Create(p)
	if err != nil {
		return err
	}
	_, werr := w.Write([]byte(body))
	if cerr := w.Close(); cerr != nil {
		return cerr
	}
	return werr
}

func TestHostileNamesSurvive(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	cl := hz.mustDial(t, "sftp@example.com", testPassword)
	for _, dir := range []string{"/main/f", "/main/d"} {
		if err := cl.Mkdir(dir); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	all := hostilenames.ForLocalDisk()
	for _, n := range all {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			p := "/main/f/" + n.Name
			body := "body of " + n.ID
			if err := putFile(cl, p, body); err != nil {
				t.Fatalf("upload %q: %v", n.Name, err)
			}
			if _, err := cl.Stat(p); err != nil {
				t.Fatalf("stat by the same name: %v", err)
			}
			r, err := cl.Open(p)
			if err != nil {
				t.Fatalf("open: %v", err)
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
			if err := cl.Rename(p, away); err != nil {
				t.Fatalf("rename away from %q: %v", n.Name, err)
			}
			if err := cl.Rename(away, p); err != nil {
				t.Fatalf("rename back to %q: %v", n.Name, err)
			}
			if err := cl.Mkdir("/main/d/" + n.Name); err != nil {
				t.Fatalf("mkdir %q: %v", n.Name, err)
			}
		})
	}

	// Side by side in one folder, each with its own bytes: in the listing a
	// client reads, and on the disk the storage keeps.
	for _, dir := range []string{"f", "d"} {
		entries, err := cl.ReadDir("/main/" + dir)
		if err != nil {
			t.Fatalf("readdir %s: %v", dir, err)
		}
		listed := map[string]bool{}
		for _, e := range entries {
			listed[e.Name()] = true
		}
		disk := onDisk(t, filepath.Join(root, dir))
		for _, n := range all {
			if !listed[n.Name] {
				t.Errorf("%s/%s: %q is not in the listing", dir, n.ID, n.Name)
			}
			if !disk[n.Name] {
				t.Errorf("%s/%s: %q is not on disk under its own bytes", dir, n.ID, n.Name)
			}
		}
	}
}

// The deep path: 64 folders made one by one, the file at the bottom written
// and read back.
func TestHostileDeepPathRoundTrips(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	d := hostilenames.Deep()
	for _, dir := range d.Dirs() {
		if err := cl.Mkdir("/main/" + dir); err != nil {
			t.Fatalf("mkdir at depth %d: %v", strings.Count(dir, "/")+1, err)
		}
	}
	if err := putFile(cl, "/main/"+d.Path, "deep"); err != nil {
		t.Fatalf("upload: %v", err)
	}
	entries, err := cl.ReadDir("/main/" + path.Dir(d.Path))
	if err != nil || len(entries) != 1 || entries[0].Name() != d.Leaf {
		t.Fatalf("the deepest folder lists %v (%v), want %q", names(entries), err, d.Leaf)
	}
	got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), filepath.FromSlash(d.Path)))
	if err != nil || string(got) != "deep" {
		t.Fatalf("on disk %q (%v)", got, err)
	}
}

// The names no door may create (internal/leafname): refused at the open, the
// mkdir and the rename, with the reason as the status message, and nothing
// on disk.
func TestHostileNamesRefused(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "src.txt", []byte("source"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	for _, n := range hostilenames.Refused() {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			// A backslash is refused on a Windows host too: the request
			// paths are escaped before pkg/sftp's host-dependent Clean
			// (rawpath.go), so it never becomes a separator there.
			p := "/main/" + n.Name
			// `..` never reaches the name rule: the library resolves it to
			// the virtual root, which refuses every change. Its message is
			// not the rule's.
			byRule := n.ID != "dot-dot"
			for verb, err := range map[string]error{
				"upload": putFile(cl, p, "x"),
				"mkdir":  cl.Mkdir(p),
				"rename": cl.Rename("/main/src.txt", p),
			} {
				if err == nil {
					t.Errorf("%s: %q was accepted", verb, n.Name)
					continue
				}
				var se *sftp.StatusError
				if byRule && (!errors.As(err, &se) || !strings.Contains(err.Error(), "a name cannot")) {
					t.Errorf("%s: %v, want the name rule's reason", verb, err)
				}
			}
			if disk := onDisk(t, root); disk[n.Name] {
				t.Errorf("%q is on disk", n.Name)
			}
			if _, err := os.Stat(filepath.Join(root, "src.txt")); err != nil {
				t.Errorf("the rename's source went: %v", err)
			}
		})
	}
}

// A name the disk cannot hold (over 255 bytes on ext4 and APFS): "name too
// long", never the driver's text with the server's path in it.
func TestHostileNamesTooLong(t *testing.T) {
	long := hostilenames.TooLongForLocalDisk()
	if len(long) == 0 {
		t.Skip("this OS's disk holds every name in the list")
	}
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	for _, n := range long {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			for verb, err := range map[string]error{
				"upload": putFile(cl, "/main/"+n.Name, "x"),
				"mkdir":  cl.Mkdir("/main/" + n.Name),
			} {
				if err == nil {
					t.Errorf("%s: a %d-byte name was accepted", verb, n.Bytes())
					continue
				}
				msg := err.Error()
				if !strings.Contains(msg, "name too long") {
					t.Errorf("%s: %q, want \"name too long\"", verb, msg)
				}
				if strings.Contains(msg, root) || strings.Contains(msg, filepath.ToSlash(root)) {
					t.Errorf("%s: the answer carries the server's path: %q", verb, msg)
				}
			}
		})
	}
}

// foldingDriver is the local disk as a case-insensitive file system (Windows,
// macOS) answers Stat: a name in any case finds the entry that is there.
type foldingDriver struct{ *local.Driver }

func (d foldingDriver) Stat(ctx context.Context, p string) (storage.Object, error) {
	dir, base := path.Split(strings.Trim(p, "/"))
	if base == "" {
		return d.Driver.Stat(ctx, p)
	}
	objs, err := d.Driver.List(ctx, dir)
	if err != nil {
		return storage.Object{}, err
	}
	for _, o := range objs {
		if strings.EqualFold(o.Name, base) {
			return d.Driver.Stat(ctx, dir+o.Name)
		}
	}
	return storage.Object{}, storage.ErrNotFound
}

// A plain v3 RENAME that changes nothing but the case (WinSCP sends one) is a
// rename, not a collision with itself - while a different file of the same
// name in another case is still in the way.
func TestCaseOnlyRenameOnAStorageThatFoldsCase(t *testing.T) {
	hz := newHarnessCfg(t, func(c *sftpsrv.Config) {
		inner := c.Resolver
		c.Resolver = func(id int64) (storage.Driver, error) {
			d, err := inner(id)
			if err != nil {
				return nil, err
			}
			return foldingDriver{d.(*local.Driver)}, nil
		}
	})
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	upper, lower := hostilenames.Get("case-upper"), hostilenames.Get("case-lower")
	hz.writeFile(t, st, upper.Name, []byte("rapor"))
	hz.writeFile(t, st, "Other.txt", []byte("other"))
	hz.writeFile(t, st, "x.txt", []byte("x"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	if err := cl.Rename("/main/"+upper.Name, "/main/"+lower.Name); err != nil {
		t.Fatalf("a rename that only changes the case was refused: %v", err)
	}
	disk := onDisk(t, root)
	if !disk[lower.Name] || disk[upper.Name] {
		t.Fatalf("after the rename the disk holds %v", disk)
	}
	if err := cl.Rename("/main/x.txt", "/main/other.txt"); err == nil {
		t.Fatal("a rename onto another file's name in another case went through")
	}
	if got, err := os.ReadFile(filepath.Join(root, "Other.txt")); err != nil || string(got) != "other" {
		t.Fatalf("the file in the way changed: %q (%v)", got, err)
	}
}

// A file that ALREADY has a name no door may give - a tab, a backslash - made
// on the storage itself, or over a protocol before the rule, stays a file:
// listed, read, overwritten and removed like any other. Only a NEW name is
// refused (the owner's decision for 0.57). The backslash one also measures
// the path escape (rawpath.go): the name reaches filex whole, never split.
func TestExistingRefusedNamesStayUsable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a tab or a backslash cannot be in a Windows file name")
	}
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	for _, id := range []string{"control-tab", "backslash"} {
		n := hostilenames.Get(id)
		t.Run(id, func(t *testing.T) {
			hz.writeFile(t, st, n.Name, []byte("old"))
			p := "/main/" + n.Name
			entries, err := cl.ReadDir("/main")
			if err != nil {
				t.Fatalf("readdir: %v", err)
			}
			listed := false
			for _, e := range entries {
				listed = listed || e.Name() == n.Name
			}
			if !listed {
				t.Fatalf("%q is not in the listing: %v", n.Name, names(entries))
			}
			r, err := cl.Open(p)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			got, err := io.ReadAll(r)
			r.Close()
			if err != nil || string(got) != "old" {
				t.Fatalf("read %q (%v)", got, err)
			}
			if err := putFile(cl, p, "new"); err != nil {
				t.Fatalf("overwrite: %v", err)
			}
			if got, err := os.ReadFile(filepath.Join(root, n.Name)); err != nil || string(got) != "new" {
				t.Fatalf("on disk after the overwrite %q (%v)", got, err)
			}
			if err := cl.Remove(p); err != nil {
				t.Fatalf("remove: %v", err)
			}
			if onDisk(t, root)[n.Name] {
				t.Fatal("still on disk after the remove")
			}
		})
	}
}

// RMDIR removes an EMPTY folder (to the trash, as before). A folder with
// something in it answers "directory not empty" and loses nothing; RMDIR on a
// file is "not a directory".
func TestRmdirRemovesOnlyAnEmptyFolder(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "full/a.txt", []byte("a"))
	hz.writeFile(t, st, "full/sub/b.txt", []byte("b"))
	hz.writeFile(t, st, "file.txt", []byte("f"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)
	if err := cl.Mkdir("/main/empty"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := cl.RemoveDirectory("/main/full"); err == nil || !strings.Contains(err.Error(), "directory not empty") {
		t.Errorf("rmdir of a full folder: %v, want \"directory not empty\"", err)
	}
	for _, rel := range []string{"full/a.txt", "full/sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s went: %v", rel, err)
		}
	}
	if err := cl.RemoveDirectory("/main/file.txt"); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("rmdir of a file: %v, want \"not a directory\"", err)
	}
	if _, err := os.Stat(filepath.Join(root, "file.txt")); err != nil {
		t.Errorf("the file went: %v", err)
	}
	if err := cl.RemoveDirectory("/main/empty"); err != nil {
		t.Fatalf("rmdir of an empty folder: %v", err)
	}
	if onDisk(t, root)["empty"] {
		t.Error("the empty folder is still there")
	}
}

// rawRemove sends one SSH_FXP_REMOVE and nothing else - pkg/sftp's client
// Remove falls back to RMDIR when REMOVE fails, which would hide the answer -
// and returns the status code and message the server gives.
func rawRemove(t *testing.T, hz *harness, login, pass, p string) (uint32, string) {
	t.Helper()
	conn, err := ssh.Dial("tcp", hz.addr, &ssh.ClientConfig{
		User:            login,
		Auth:            []ssh.AuthMethod{ssh.Password(pass)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // the key is generated per test
		Timeout:         10 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	sess, err := conn.NewSession()
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	defer sess.Close()
	in, err := sess.StdinPipe()
	if err != nil {
		t.Fatalf("stdin: %v", err)
	}
	out, err := sess.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := sess.RequestSubsystem("sftp"); err != nil {
		t.Fatalf("subsystem: %v", err)
	}
	send := func(typ byte, body []byte) {
		pkt := binary.BigEndian.AppendUint32(nil, uint32(1+len(body)))
		pkt = append(append(pkt, typ), body...)
		if _, err := in.Write(pkt); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	recv := func() (byte, []byte) {
		var head [4]byte
		if _, err := io.ReadFull(out, head[:]); err != nil {
			t.Fatalf("recv: %v", err)
		}
		b := make([]byte, binary.BigEndian.Uint32(head[:]))
		if _, err := io.ReadFull(out, b); err != nil || len(b) == 0 {
			t.Fatalf("recv body: %v", err)
		}
		return b[0], b[1:]
	}
	send(1, binary.BigEndian.AppendUint32(nil, 3)) // INIT, version 3
	if typ, _ := recv(); typ != 2 {
		t.Fatalf("no VERSION, packet type %d", typ)
	}
	body := binary.BigEndian.AppendUint32(nil, 1) // request id
	body = binary.BigEndian.AppendUint32(body, uint32(len(p)))
	send(13, append(body, p...)) // REMOVE
	typ, b := recv()
	if typ != 101 || len(b) < 12 { // STATUS: id, code, message
		t.Fatalf("no STATUS, packet type %d (% x)", typ, b)
	}
	code := binary.BigEndian.Uint32(b[4:8])
	n := int(binary.BigEndian.Uint32(b[8:12]))
	if 12+n > len(b) {
		t.Fatalf("a short STATUS: % x", b)
	}
	return code, string(b[12 : 12+n])
}

// REMOVE is a FILE's delete. On a folder, empty or not, it answers "is a
// directory" and removes nothing: a folder goes only by RMDIR, and only empty
// (the owner's decision for 0.57). A file's REMOVE is unchanged.
func TestRemoveOnAFolderIsRefused(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "full/a.txt", []byte("a"))
	hz.writeFile(t, st, "full/sub/b.txt", []byte("b"))
	hz.writeFile(t, st, "file.txt", []byte("f"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)
	if err := cl.Mkdir("/main/empty"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for _, dir := range []string{"full", "empty"} {
		code, msg := rawRemove(t, hz, "sftp@example.com", testPassword, "/main/"+dir)
		if code == 0 || !strings.Contains(msg, "is a directory") {
			t.Errorf("REMOVE of the folder %s: status %d %q, want a refusal \"is a directory\"", dir, code, msg)
		}
		if !onDisk(t, root)[dir] {
			t.Errorf("the folder %s went", dir)
		}
	}
	for _, rel := range []string{"full/a.txt", "full/sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s went: %v", rel, err)
		}
	}
	if code, msg := rawRemove(t, hz, "sftp@example.com", testPassword, "/main/file.txt"); code != 0 {
		t.Fatalf("REMOVE of a file: status %d %q", code, msg)
	}
	if onDisk(t, root)["file.txt"] {
		t.Error("the file is still there")
	}
}
