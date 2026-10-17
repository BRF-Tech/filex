package nfssrv_test

// The shared hostile names (internal/testutil/hostilenames, task #238) over
// NFSv3: every name a filex door must accept goes in and comes back with its
// own bytes, every name no door may create is refused, a name over NFSv3's
// 255 bytes is refused as too long and left out of a listing, and the
// export's own root is never removed or renamed by `.` or `..`.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	nfsclient "github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/willscott/go-nfs-client/nfs/xdr"

	"github.com/brf-tech/filex/backend/internal/nfssrv"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil/hostilenames"
)

// storedNames lists a folder of the storage's own disk: the names exactly as
// they were stored.
func storedNames(t *testing.T, dir string) map[string]bool {
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
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	for _, dir := range []string{"/f", "/d"} {
		if _, err := target.Mkdir(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	all := hostilenames.ForLocalDisk()
	for _, n := range all {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			p := "/f/" + n.Name
			body := "body of " + n.ID
			wr, err := target.OpenFile(p, 0o644)
			if err != nil {
				t.Fatalf("create %q: %v", n.Name, err)
			}
			if _, err := wr.Write([]byte(body)); err != nil {
				t.Fatalf("write: %v", err)
			}
			if err := wr.Close(); err != nil {
				t.Fatalf("close: %v", err)
			}
			rd, err := target.Open(p)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			got, err := io.ReadAll(rd)
			rd.Close()
			if err != nil || string(got) != body {
				t.Fatalf("read back %q (%v), want %q", got, err, body)
			}
			// Away and back: the name is a rename's target too. (A short
			// name in between: the 255-byte name plus a suffix would be
			// over NFSv3's limit.)
			away := "/f/renamed-" + n.ID
			if err := target.Rename(p, away); err != nil {
				t.Fatalf("rename away from %q: %v", n.Name, err)
			}
			if err := target.Rename(away, p); err != nil {
				t.Fatalf("rename back to %q: %v", n.Name, err)
			}
			if _, err := target.Mkdir("/d/"+n.Name, 0o755); err != nil {
				t.Fatalf("mkdir %q: %v", n.Name, err)
			}
		})
	}

	// Side by side in one folder, each with its own bytes: in READDIRPLUS,
	// and on the disk the storage keeps.
	for _, dir := range []string{"f", "d"} {
		entries, err := target.ReadDirPlus("/" + dir)
		if err != nil {
			t.Fatalf("readdirplus %s: %v", dir, err)
		}
		listed := map[string]bool{}
		for _, name := range entryNames(entries) {
			listed[name] = true
		}
		disk := storedNames(t, filepath.Join(root, dir))
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

// The names no door may create (internal/leafname): refused at the CREATE,
// the MKDIR and the RENAME (NFS3ERR_ACCES, the one refusal go-nfs passes
// through for all three), and nothing on disk.
func TestHostileNamesRefused(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "src.txt", []byte("source"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	for _, n := range hostilenames.Refused() {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			switch {
			case n.ID == "dot-dot":
				// The client resolves `..` itself and never sends it as a
				// name to create; what the server does with a `..` that does
				// arrive is TestExportRootIsNeverRemovedOrRenamed.
				t.Skip("the NFS client resolves .. before it reaches the server")
			case runtime.GOOS == "windows" && n.Has("separator"):
				t.Skip("the NFS test client splits paths with the host's separator")
			}
			p := "/" + n.Name
			if _, err := target.OpenFile(p, 0o644); err == nil {
				t.Error("create: accepted")
			}
			if _, err := target.Mkdir(p, 0o755); err == nil {
				t.Error("mkdir: accepted")
			}
			if err := target.Rename("/src.txt", p); nfsStatus(err) != nfsclient.NFS3ErrAcces {
				t.Errorf("rename: %v, want NFS3ERR_ACCES", err)
			}
			if disk := storedNames(t, root); disk[n.Name] || !disk["src.txt"] {
				t.Errorf("the disk holds %v", disk)
			}
		})
	}
}

// A name over 255 bytes: NFSv3 refuses it on every verb that takes a name
// (go-nfs answers NFS3ERR_NAMETOOLONG before the name reaches filex).
func TestHostileNamesTooLong(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "src.txt", []byte("source"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	for _, n := range hostilenames.Filter(func(n hostilenames.Name) bool { return n.Has("over-255-bytes") }) {
		n := n
		t.Run(n.ID, func(t *testing.T) {
			p := "/" + n.Name
			if _, err := target.OpenFile(p, 0o644); nfsStatus(err) != nfsclient.NFS3ErrNameTooLong {
				t.Errorf("create: %v, want NFS3ERR_NAMETOOLONG", err)
			}
			if _, err := target.Mkdir(p, 0o755); nfsStatus(err) != nfsclient.NFS3ErrNameTooLong {
				t.Errorf("mkdir: %v, want NFS3ERR_NAMETOOLONG", err)
			}
			if err := target.Rename("/src.txt", p); nfsStatus(err) != nfsclient.NFS3ErrNameTooLong {
				t.Errorf("rename: %v, want NFS3ERR_NAMETOOLONG", err)
			}
		})
	}
}

// longNameDriver is a storage that holds a name longer than NFSv3 carries, as
// an object store does (it holds names a local disk refuses).
type longNameDriver struct {
	*local.Driver
	extra string
}

func (d longNameDriver) List(ctx context.Context, p string) ([]storage.Object, error) {
	objs, err := d.Driver.List(ctx, p)
	if err == nil && strings.Trim(p, "/") == "" {
		objs = append(objs, storage.Object{Path: d.extra, Name: d.extra, Kind: storage.KindFile, Size: 1})
	}
	return objs, err
}

// One name over 255 bytes in a folder must not cost the folder: the entry is
// left out of READDIR, the rest of the folder is listed.
func TestNamesOverNFSLimitAreLeftOutOfListings(t *testing.T) {
	long := hostilenames.Get("bytes-256")
	hz := newHarnessCfg(t, func(c *nfssrv.Config) {
		inner := c.Resolver
		c.Resolver = func(id int64) (storage.Driver, error) {
			d, err := inner(id)
			if err != nil {
				return nil, err
			}
			return longNameDriver{Driver: d.(*local.Driver), extra: long.Name}, nil
		}
	})
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "visible.txt", []byte("seen"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	entries, err := target.ReadDirPlus("/")
	if err != nil {
		t.Fatalf("readdirplus: %v", err)
	}
	got := entryNames(entries)
	seen := false
	for _, name := range got {
		if len(name) > 255 {
			t.Errorf("a %d-byte name is in the listing", len(name))
		}
		if name == "visible.txt" {
			seen = true
		}
	}
	if !seen {
		t.Fatalf("the ordinary file is missing: %v", got)
	}
}

// `.` and `..` on the mount's root name the export's own folder once go-nfs
// has joined them on: never removed, never renamed - for an export of a
// folder inside a storage too, where that folder is an ordinary path to the
// storage.
func TestExportRootIsNeverRemovedOrRenamed(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "projects/acme/report.txt", []byte("inside"))
	hz.writeFile(t, st, "projects/acme/old.txt", []byte("goes"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{
		Storage: "main", Prefix: "projects/acme",
	}))

	report := filepath.Join(root, "projects", "acme", "report.txt")
	for _, name := range []string{".", ".."} {
		if err := target.Remove("/" + name); err == nil {
			t.Errorf("REMOVE %q on the mount's root went through", name)
		}
		if err := target.RmDir("/" + name); err == nil {
			t.Errorf("RMDIR %q on the mount's root went through", name)
		}
		if err := target.Rename("/"+name, "/moved"); err == nil {
			t.Errorf("RENAME %q on the mount's root went through", name)
		}
		if _, err := os.Stat(report); err != nil {
			t.Fatalf("after %q the export's folder is gone: %v", name, err)
		}
	}
	// What is IN the export still goes as usual.
	if err := target.Remove("/old.txt"); err != nil {
		t.Fatalf("an ordinary remove was refused: %v", err)
	}
}

// A file that ALREADY has a name no door may give - a tab - made on the
// storage itself, or over a protocol before the rule, stays a file: listed,
// read, overwritten and removed like any other. Only a NEW name is refused.
func TestExistingRefusedNamesStayUsable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a tab cannot be in a Windows file name")
	}
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	n := hostilenames.Get("control-tab")
	hz.writeFile(t, st, n.Name, []byte("old"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	p := "/" + n.Name

	entries, err := target.ReadDirPlus("/")
	if err != nil {
		t.Fatalf("readdirplus: %v", err)
	}
	listed := false
	for _, name := range entryNames(entries) {
		listed = listed || name == n.Name
	}
	if !listed {
		t.Fatalf("%q is not in the listing: %v", n.Name, entryNames(entries))
	}
	rd, err := target.Open(p)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, err := io.ReadAll(rd)
	rd.Close()
	if err != nil || string(got) != "old" {
		t.Fatalf("read %q (%v)", got, err)
	}
	wr, err := target.OpenFile(p, 0o644)
	if err != nil {
		t.Fatalf("open for writing: %v", err)
	}
	if _, err := wr.Write([]byte("new")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := wr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, n.Name)); err != nil || string(got) != "new" {
		t.Fatalf("on disk after the overwrite %q (%v)", got, err)
	}
	if err := target.Remove(p); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if storedNames(t, root)[n.Name] {
		t.Fatal("still on disk after the remove")
	}
}

// rawDirop is one NFSv3 request that names an entry in a folder by handle and
// name - CREATE, MKDIR, REMOVE - sent as it is, the way a client that does
// not split paths sends it (go-nfs-client's own calls split on `/` first).
type rawDirop struct {
	rpc.Header
	Where nfsclient.Diropargs3
}

type rawCreate struct {
	rpc.Header
	Where nfsclient.Diropargs3
	How   struct {
		Mode uint32
		Attr nfsclient.Sattr3
	}
}

type rawMkdir struct {
	rpc.Header
	Where nfsclient.Diropargs3
	Attrs nfsclient.Sattr3
}

type rawRename struct {
	rpc.Header
	From nfsclient.Diropargs3
	To   nfsclient.Diropargs3
}

func nfsHeader(proc uint32) rpc.Header {
	return rpc.Header{Rpcvers: 2, Prog: nfsclient.Nfs3Prog, Vers: nfsclient.Nfs3Vers, Proc: proc, Cred: rpc.AuthNull, Verf: rpc.AuthNull}
}

// rawStatus sends call and reads the NFS3 status it answers.
func rawStatus(t *testing.T, target *nfsclient.Target, call any) uint32 {
	t.Helper()
	res, err := target.Call(call)
	if err != nil {
		t.Fatalf("rpc: %v", err)
	}
	status, err := xdr.ReadUint32(res)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	return status
}

// A NAME holding a `/` - which go-nfs joins onto the folder's path before
// filex sees it - made the folders on the way (a CREATE of `x/y.txt` made
// `x`); `..` named the parent. Both are refused now (NFS3ERR_ACCES, the one
// refusal go-nfs passes through), and nothing is made, moved or removed.
func TestNamesWithASlashOrDotsAreRefused(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "src.txt", []byte("source"))
	hz.writeFile(t, st, "a2/b2/keep.txt", []byte("kept"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	_, rootFH, err := target.Lookup("/")
	if err != nil {
		t.Fatalf("lookup /: %v", err)
	}
	_, deepFH, err := target.Lookup("/a2/b2")
	if err != nil {
		t.Fatalf("lookup /a2/b2: %v", err)
	}

	create := &rawCreate{Header: nfsHeader(nfsclient.NFSProc3Create), Where: nfsclient.Diropargs3{FH: rootFH, Filename: "x/y.txt"}}
	if s := rawStatus(t, target, create); s != nfsclient.NFS3ErrAcces {
		t.Errorf("CREATE x/y.txt: status %d, want NFS3ERR_ACCES", s)
	}
	mkdir := &rawMkdir{Header: nfsHeader(nfsclient.NFSProc3Mkdir), Where: nfsclient.Diropargs3{FH: rootFH, Filename: "m/n"}}
	mkdir.Attrs.Mode.SetIt, mkdir.Attrs.Mode.Mode = true, 0o755
	if s := rawStatus(t, target, mkdir); s != nfsclient.NFS3ErrAcces {
		t.Errorf("MKDIR m/n: status %d, want NFS3ERR_ACCES", s)
	}
	rename := &rawRename{
		Header: nfsHeader(nfsclient.NFSProc3Rename),
		From:   nfsclient.Diropargs3{FH: rootFH, Filename: "src.txt"},
		To:     nfsclient.Diropargs3{FH: rootFH, Filename: "z/moved.txt"},
	}
	if s := rawStatus(t, target, rename); s != nfsclient.NFS3ErrAcces {
		t.Errorf("RENAME to z/moved.txt: status %d, want NFS3ERR_ACCES", s)
	}
	remove := &rawDirop{Header: nfsHeader(nfsclient.NFSProc3Remove), Where: nfsclient.Diropargs3{FH: deepFH, Filename: ".."}}
	if s := rawStatus(t, target, remove); s != nfsclient.NFS3ErrAcces {
		t.Errorf("REMOVE .. in a2/b2: status %d, want NFS3ERR_ACCES", s)
	}

	disk := storedNames(t, root)
	for _, made := range []string{"x", "m", "z"} {
		if disk[made] {
			t.Errorf("a folder %q was made out of a name", made)
		}
	}
	if !disk["src.txt"] {
		t.Error("the rename's source went")
	}
	if _, err := os.Stat(filepath.Join(root, "a2", "b2", "keep.txt")); err != nil {
		t.Errorf("REMOVE .. took the parent: %v", err)
	}
}

// RMDIR removes an EMPTY folder (to the trash, as before). A folder with
// something in it is refused and loses nothing. ⚠ The client reads
// NFS3ERR_IO, not NFS3ERR_NOTEMPTY: go-nfs answers every error of a REMOVE or
// RMDIR but "not there" and "not allowed" with IO. REMOVE of a file is
// unchanged.
func TestRmdirRemovesOnlyAnEmptyFolder(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	root := hz.rootOf(t, st)
	hz.writeFile(t, st, "full/a.txt", []byte("a"))
	hz.writeFile(t, st, "full/sub/b.txt", []byte("b"))
	hz.writeFile(t, st, "file.txt", []byte("f"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	if _, err := target.Mkdir("/empty", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := target.RmDir("/full"); nfsStatus(err) != nfsclient.NFS3ErrIO {
		t.Errorf("rmdir of a full folder: %v, want a refusal (NFS3ERR_IO)", err)
	}
	for _, rel := range []string{"full/a.txt", "full/sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s went: %v", rel, err)
		}
	}
	if err := target.RmDir("/empty"); err != nil {
		t.Fatalf("rmdir of an empty folder: %v", err)
	}
	if storedNames(t, root)["empty"] {
		t.Error("the empty folder is still there")
	}
	if err := target.Remove("/file.txt"); err != nil {
		t.Fatalf("remove of a file: %v", err)
	}
}
