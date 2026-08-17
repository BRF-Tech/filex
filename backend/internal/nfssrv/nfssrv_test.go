package nfssrv_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	nfsclient "github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/auth/drivers/apitoken"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/nfssrv"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The NFSv3 endpoint, driven by a REAL NFS client over a real socket
// (willscott/go-nfs-client — the counterpart of the server library, and the
// same wire format the kernel's client speaks).
//
// What matters most here is the identity model: NFS carries no credential on a
// request, so everything rests on the export path being a secret and the mount
// being pinned to one principal.

const testPassword = "NfsPass!1"

type harness struct {
	srv   *nfssrv.Server
	store db.Store
	res   *protocolauth.Resolver
	addr  string
	roots map[int64]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	_, raw := testutil.NewTestDB(t)
	store := identitystore.New(raw)

	res := protocolauth.New(store, acl.New(store), false)
	res.Confine = protocolauth.ConfineHonor

	hz := &harness{store: store, res: res, roots: map[int64]string{}}
	srv, err := nfssrv.New(nfssrv.Config{
		Enabled:  true,
		Addr:     "127.0.0.1:0",
		Store:    store,
		Auth:     res,
		ACL:      acl.New(store),
		Body:     filebody.New(store, nil),
		Quota:    quota.New(store),
		SpoolDir: t.TempDir(),
		Resolver: func(id int64) (storage.Driver, error) {
			st, err := store.GetStorage(context.Background(), id)
			if err != nil {
				return nil, err
			}
			var cfg map[string]any
			if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
				return nil, err
			}
			drv := &local.Driver{}
			if err := drv.Init(context.Background(), cfg); err != nil {
				return nil, err
			}
			return drv, nil
		},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	hz.srv = srv

	go func() { _ = srv.ListenAndServe() }()
	for i := 0; i < 400 && srv.Addr() == ""; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if srv.Addr() == "" {
		t.Fatal("server never bound an address")
	}
	hz.addr = srv.Addr()
	t.Cleanup(func() { _ = srv.Close() })
	return hz
}

func (hz *harness) user(t *testing.T, email string) *model.User {
	t.Helper()
	hash, err := authlocal.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := hz.store.CreateUser(context.Background(), email, hash, model.RoleUser, "en", "UTC")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	return u
}

func (hz *harness) storage(t *testing.T, name string) *model.Storage {
	t.Helper()
	root := t.TempDir()
	st, err := hz.store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", Enabled: true,
		ConfigJSON: json.RawMessage(`{"path":` + strconv.Quote(filepath.ToSlash(root)) + `}`),
	})
	if err != nil {
		t.Fatalf("storage %s: %v", name, err)
	}
	hz.roots[st.ID] = root
	return st
}

func (hz *harness) rootOf(t *testing.T, st *model.Storage) string {
	t.Helper()
	root, ok := hz.roots[st.ID]
	if !ok {
		t.Fatalf("storage %d was not created here", st.ID)
	}
	return root
}

func (hz *harness) writeFile(t *testing.T, st *model.Storage, rel string, body []byte) {
	t.Helper()
	p := filepath.Join(hz.rootOf(t, st), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// export mints an export and returns its path — the credential.
func (hz *harness) export(t *testing.T, u *model.User, req protocolauth.IssueExportRequest) string {
	t.Helper()
	req.User = u
	issued, err := hz.res.IssueExport(context.Background(), req)
	if err != nil {
		t.Fatalf("issue export: %v", err)
	}
	return issued.Path
}

// mount performs the real mount handshake.
//
// ⚠ It dials the server's port DIRECTLY rather than going through
// `nfsclient.DialMount`, which asks a portmapper on :111 where the mount
// service lives. filex serves mount and NFS on one port and runs no
// portmapper — the same shape `rclone serve nfs` has, and the reason its
// documented mount line carries `-o port=…,mountport=…`. Binding 111 would mean
// a privileged port and a second listener for a lookup whose answer is already
// in the client's configuration.
//
// ⚠⚠ The retry is not flake-hiding, it is working around a documented choice
// in the client library: rpc.Client.pickLdr picks its own LOCAL port at random
// out of 49152-65535 and binds it explicitly, because NFS servers historically
// judged clients by their source port. Nothing reserves that port first, so a
// package that mounts a few dozen times collides with itself — and with every
// ephemeral port the OS has already handed out. Measured before this retry: 2
// failures in 5 runs of this package, landing on a different test each time.
// A caller cannot pass its own dialer, so the collision has to be absorbed
// here; without it the suite is red at random and the release CI with it.
func (hz *harness) mount(t *testing.T, path string) (*nfsclient.Target, error) {
	t.Helper()
	var target *nfsclient.Target
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		target, err = hz.mountOnce(t, path)
		if err == nil || !errors.Is(err, syscall.EADDRINUSE) {
			return target, err
		}
	}
	return nil, err
}

func (hz *harness) mountOnce(t *testing.T, path string) (*nfsclient.Target, error) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(hz.addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}
	client, err := nfsclient.DialServiceAtPort(host, port)
	if err != nil {
		return nil, err
	}
	// ⚠ Addr deliberately EMPTY. With one set, the client re-dials through a
	// portmapper on :111 to find the NFS program after mounting; with it empty
	// it reuses this connection, which is the portmapper-free path.
	m := &nfsclient.Mount{Client: client}
	target, err := m.Mount(path, rpc.AuthNull)
	if err != nil {
		m.Close()
		return nil, err
	}
	t.Cleanup(func() {
		target.Close()
		m.Close()
	})
	return target, nil
}

func (hz *harness) mustMount(t *testing.T, path string) *nfsclient.Target {
	t.Helper()
	target, err := hz.mount(t, path)
	if err != nil {
		t.Fatalf("mount %s: %v", path, err)
	}
	return target
}

// ─────────────────────────── the identity model ───────────────────────────

// ⚠⚠ The export path IS the credential. This is the test that says so: a path
// nobody minted must not mount, and neither must a plausible-looking guess.
func TestOnlyAMintedExportPathMounts(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	hz.storage(t, "main")

	good := hz.export(t, u, protocolauth.IssueExportRequest{Label: "media player"})
	if _, err := hz.mount(t, good); err != nil {
		t.Fatalf("a minted export did not mount: %v", err)
	}

	for _, bad := range []string{
		"/",
		"/main",
		"/x/",
		"/x/" + strings.Repeat("0", 64),
		good + "x",
		strings.TrimSuffix(good, "a") + "b",
	} {
		if _, err := hz.mount(t, bad); err == nil {
			t.Fatalf("a path nobody minted mounted: %q", bad)
		}
	}
}

// A disabled or expired export stops working — that is what revocation IS here,
// since there is no session to tear down.
func TestDisabledAndExpiredExportsAreRefused(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	hz.storage(t, "main")

	path := hz.export(t, u, protocolauth.IssueExportRequest{Label: "off"})
	list, err := hz.store.ListNFSExports(context.Background(), u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list exports: %v", err)
	}
	if err := hz.store.SetNFSExportDisabled(context.Background(), list[0].ID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := hz.mount(t, path); err == nil {
		t.Fatal("a disabled export still mounted")
	}

	past := time.Now().Add(-time.Hour)
	expired := hz.export(t, u, protocolauth.IssueExportRequest{Label: "expired", ExpiresAt: &past})
	if _, err := hz.mount(t, expired); err == nil {
		t.Fatal("an expired export still mounted")
	}
}

// ⚠ An allow-list that cannot be evaluated must DENY. The direction matters:
// the other way round, a typo in a CIDR opens the mount to everybody.
func TestCIDRAllowListGates(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	hz.storage(t, "main")

	// The test client connects from loopback.
	ok := hz.export(t, u, protocolauth.IssueExportRequest{Label: "lan", AllowCIDRs: "127.0.0.0/8"})
	if _, err := hz.mount(t, ok); err != nil {
		t.Fatalf("an allowed address was refused: %v", err)
	}

	blocked := hz.export(t, u, protocolauth.IssueExportRequest{Label: "elsewhere", AllowCIDRs: "10.0.0.0/8"})
	if _, err := hz.mount(t, blocked); err == nil {
		t.Fatal("an address outside the allow-list mounted")
	}

	// A list that does not parse is refused at ISSUE time rather than stored —
	// an allow-list nobody can read is an allow-list that allows everything.
	if _, err := hz.res.IssueExport(context.Background(), protocolauth.IssueExportRequest{
		User: u, Label: "broken", AllowCIDRs: "not-a-cidr",
	}); err == nil {
		t.Fatal("an unparseable allow-list was accepted")
	}
}

// ─────────────────────────── the tree ───────────────────────────

func TestRootListsTheStoragesTheCallerCanSee(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	hz.storage(t, "main")
	locked := hz.storage(t, "locked")
	locked.RBACEnabled = true
	if err := hz.store.UpdateStorage(context.Background(), locked); err != nil {
		t.Fatalf("update storage: %v", err)
	}

	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{}))
	entries, err := target.ReadDirPlus("/")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	names := entryNames(entries)
	if len(names) != 1 || names[0] != "main" {
		t.Fatalf("root = %v, want only main", names)
	}
}

func entryNames(entries []*nfsclient.EntryPlus) []string {
	out := []string{}
	for _, e := range entries {
		n := e.FileName
		if n == "." || n == ".." {
			continue
		}
		out = append(out, n)
	}
	return out
}

// ⚠ An export confined to one storage is rooted INSIDE it: `ls /mnt` must show
// what is in the folder, not a directory named after the storage. Anything else
// makes every path in the client's config carry a level nobody asked for.
func TestConfinedExportIsRootedInsideItsFolder(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.storage(t, "other")
	hz.writeFile(t, st, "projects/acme/report.txt", []byte("inside"))
	hz.writeFile(t, st, "secrets/theirs.txt", []byte("outside"))

	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{
		Storage: "main", Prefix: "projects/acme",
	}))

	entries, err := target.ReadDirPlus("/")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if got := entryNames(entries); len(got) != 1 || got[0] != "report.txt" {
		t.Fatalf("root of a confined export = %v, want report.txt", got)
	}
	// And there is no way up and out of it.
	for _, escape := range []string{"/../secrets/theirs.txt", "/../../main/secrets/theirs.txt", "/secrets/theirs.txt"} {
		if _, _, err := target.Lookup(escape); err == nil {
			t.Fatalf("a confined export reached %q", escape)
		}
	}
}

func TestReadWriteRoundTrip(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "docs/report.txt", []byte("already here"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	// Read what is there.
	rd, err := target.Open("/docs/report.txt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, err := io.ReadAll(rd)
	rd.Close()
	if err != nil || string(got) != "already here" {
		t.Fatalf("read = %q (%v)", got, err)
	}

	// Write something new, over more than one NFS write (the default is 64 KiB
	// per request, so this is several).
	body := bytes.Repeat([]byte("nfs payload. "), 40000) // ~520 KB
	wr, err := target.OpenFile("/docs/new.bin", 0o644)
	if err != nil {
		t.Fatalf("openfile: %v", err)
	}
	if _, err := wr.Write(body); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := wr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "docs", "new.bin"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(onDisk, body) {
		t.Fatalf("stored %d bytes, want %d", len(onDisk), len(body))
	}
	// ⚠ And the bookkeeping every other surface does.
	node, err := hz.store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/docs/new.bin"))
	if err != nil || node == nil {
		t.Fatalf("no node row for an NFS write: %v", err)
	}
}

func TestTurkishFilenamesSurvive(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	const name = "/gölge dosya şğüöç.txt"
	wr, err := target.OpenFile(name, 0o644)
	if err != nil {
		t.Fatalf("openfile: %v", err)
	}
	wr.Write([]byte("türkçe"))
	if err := wr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "gölge dosya şğüöç.txt")); err != nil {
		t.Fatalf("not on disk under its own name: %v", err)
	}
	entries, err := target.ReadDirPlus("/")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	found := false
	for _, n := range entryNames(entries) {
		if n == "gölge dosya şğüöç.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the Turkish name is not in the listing: %v", entryNames(entries))
	}
}

func TestMkdirRenameAndDeleteToTheTrash(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "old.txt", []byte("bye"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	if _, err := target.Mkdir("/newdir", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(hz.rootOf(t, st), "newdir")); err != nil || !fi.IsDir() {
		t.Fatalf("no directory: %v", err)
	}

	if err := target.Rename("/old.txt", "/newdir/renamed.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "newdir", "renamed.txt")); err != nil {
		t.Fatalf("rename did not land: %v", err)
	}

	if err := target.Remove("/newdir/renamed.txt"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// ⚠ To the TRASH, like every other surface.
	trashed, _ := filepath.Glob(filepath.Join(hz.rootOf(t, st), ".filex-trash", "*renamed.txt"))
	if len(trashed) == 0 {
		t.Fatal("a deleted file did not reach the trash")
	}
}

// ─────────────────────────── permission ───────────────────────────

// ⚠ A read-only EXPORT refuses writes regardless of what the account may do:
// the operator said something about this mount, not about the person.
func TestReadOnlyExportRefusesWrites(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "a.txt", []byte("readable"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{
		Storage: "main", ReadOnly: true,
	}))

	// Still readable — that is the point of read-only.
	rd, err := target.Open("/a.txt")
	if err != nil {
		t.Fatalf("read-only export refused a READ: %v", err)
	}
	rd.Close()

	if _, err := target.OpenFile("/nope.txt", 0o644); err == nil {
		t.Fatal("a read-only export accepted a write")
	}
	if _, err := target.Mkdir("/nope", 0o755); err == nil {
		t.Fatal("a read-only export accepted a mkdir")
	}
	if err := target.Remove("/a.txt"); err == nil {
		t.Fatal("a read-only export accepted a delete")
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "a.txt")); err != nil {
		t.Fatal("the file was deleted through a read-only export")
	}
}

func TestHiddenBucketsAreInvisible(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, ".filex-trash/old.txt", []byte("trashed"))
	hz.writeFile(t, st, "visible.txt", []byte("x"))
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	entries, err := target.ReadDirPlus("/")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, n := range entryNames(entries) {
		if n == ".filex-trash" {
			t.Fatal("the trash bucket is listed")
		}
	}
	if _, _, err := target.Lookup("/.filex-trash/old.txt"); err == nil {
		t.Fatal("a path inside the trash bucket is reachable")
	}
}

func TestQuotaIsEnforcedBeforeTheBytesLand(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	if err := quota.New(hz.store).SetQuota(context.Background(), u.ID, 64); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))

	wr, err := target.OpenFile("/too-big.bin", 0o644)
	if err != nil {
		t.Fatalf("openfile: %v", err)
	}
	// ⚠ The error can surface on either call, and which one is not filex's to
	// decide: NFS has no close, so the library commits when it is finished with
	// the handle — which may be during the WRITE that filled it. What the
	// product promises is that the caller is told and the bytes do not land;
	// pinning it to Close() would be pinning the library's scheduling.
	_, writeErr := wr.Write(bytes.Repeat([]byte("x"), 4096))
	closeErr := wr.Close()
	if writeErr == nil && closeErr == nil {
		t.Fatal("an over-quota write was accepted silently")
	}
	// ⚠ The FILE may exist and must be EMPTY. NFS creates first and writes
	// afterwards, so a CREATE that was within quota legitimately leaves a
	// zero-byte file — which is what a real filesystem does when a write then
	// hits EDQUOT. What must not happen is the BYTES landing.
	if fi, err := os.Stat(filepath.Join(hz.rootOf(t, st), "too-big.bin")); err == nil && fi.Size() > 0 {
		t.Fatalf("the over-quota bytes were written anyway (%d bytes on disk)", fi.Size())
	}
}

// An export minted FROM a token cannot widen it — the same rule the access keys
// follow, checked here because the export is a second way to reach the same
// permission.
func TestExportCannotWidenItsParentToken(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	hz.storage(t, "main")

	secret := testutil.NewAPIToken(t, hz.store, u.ID, "read,write,root:main://projects/acme")
	tok, err := hz.store.GetAPITokenByHash(context.Background(), hashOf(secret))
	if err != nil || tok == nil {
		t.Fatalf("token lookup: %v", err)
	}

	// Widening is refused…
	if _, err := hz.res.IssueExport(context.Background(), protocolauth.IssueExportRequest{
		User: u, Token: tok, Storage: "main", Prefix: "",
	}); err == nil {
		t.Fatal("an export widened its parent token to the whole storage")
	}
	// …and asking for nothing inherits the parent's confinement rather than
	// leaving the export unconfined.
	issued, err := hz.res.IssueExport(context.Background(), protocolauth.IssueExportRequest{
		User: u, Token: tok,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if issued.Export.StorageName != "main" || issued.Export.Prefix != "projects/acme" {
		t.Fatalf("inherited confinement = %q/%q, want main/projects/acme",
			issued.Export.StorageName, issued.Export.Prefix)
	}
}

func hashOf(secret string) string { return apitoken.HashToken(secret) }

// ⚠⚠ Revoking an export must reach the mount it already opened.
//
// This is the failure the live-session registry exists for, and NFS is its
// hardest case: there is no session and no connection the server owns — the
// client holds a file handle and keeps sending RPCs — so "delete the export"
// cannot hang up on anybody. What it can do is make every operation start
// refusing, and that is what is asserted here: the same client, on the same
// mount, reading the same directory it read a moment ago.
func TestRevokingAnExportStopsAMountThatIsAlreadyOpen(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "notlar.txt", []byte("gölge"))

	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Label: "live"}))
	if _, err := target.ReadDirPlus("/"); err != nil {
		t.Fatalf("readdir before revocation: %v", err)
	}

	list, err := hz.store.ListNFSExports(context.Background(), u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list exports: %v", err)
	}
	if err := hz.store.DeleteNFSExport(context.Background(), list[0].ID, u.ID); err != nil {
		t.Fatalf("delete export: %v", err)
	}
	// Both halves of the contract: the instant kick the API handler performs,
	// and the sweep that covers every path nobody wired.
	if n := hz.res.Kick(protocolauth.KickExport, list[0].ID); n != 1 {
		t.Fatalf("kick cut %d mounts, want 1", n)
	}

	if _, err := target.ReadDirPlus("/"); err == nil {
		t.Fatal("the mount kept listing after its export was deleted")
	}
	if _, err := target.Open("main/notlar.txt"); err == nil {
		t.Fatal("the mount kept serving files after its export was deleted")
	}
}

// ⚠⚠ The folder-name leak, and the traversal bug underneath it. See the SFTP
// test of the same name.
func TestUnreachableSiblingsAreAbsentFromTheListing(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "nfs@example.com")
	st := hz.storage(t, "main")
	st.RBACEnabled = true
	if err := hz.store.UpdateStorage(context.Background(), st); err != nil {
		t.Fatalf("update storage: %v", err)
	}
	hz.writeFile(t, st, "mine/ok.txt", []byte("mine"))
	hz.writeFile(t, st, "acme-acquisition/plan.txt", []byte("theirs"))
	if _, err := hz.store.CreateFileGrant(context.Background(), &model.FileGrant{
		StorageID: st.ID, PathPrefix: "mine", IsDir: true, UserID: u.ID, Level: "viewer",
	}); err != nil {
		t.Fatalf("grant: %v", err)
	}

	target := hz.mustMount(t, hz.export(t, u, protocolauth.IssueExportRequest{Storage: "main"}))
	entries, err := target.ReadDirPlus("/")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	seen := map[string]bool{}
	for _, n := range entryNames(entries) {
		seen[n] = true
	}
	if seen["acme-acquisition"] {
		t.Fatal("a folder the caller has no grant on appeared in the listing by name")
	}
	if !seen["mine"] {
		t.Fatalf("the granted folder is missing from the listing: %v", seen)
	}
	if _, _, err := target.Lookup("/acme-acquisition/plan.txt"); err == nil {
		t.Fatal("a file under an ungranted folder was reachable")
	}
}
