package sftpsrv_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/brf-tech/filex/backend/internal/acl"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/sftpsrv"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The SFTP endpoint, driven by a REAL SSH client over a real socket.
//
// Nothing here calls a handler directly. The whole point of this surface is
// that somebody else's client talks to it, so the tests speak the wire: the
// transport, the authentication callbacks, the request server's worker pool and
// the handlers are all in the path, exactly as they are for WinSCP.

const testPassword = "SftpPass!1"

type harness struct {
	srv   *sftpsrv.Server
	store db.Store
	// res is the shared credential resolver. Kept so a test can revoke a
	// credential the way the API handlers do, on a session that is already open.
	res   *protocolauth.Resolver
	addr  string
	roots map[int64]string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	_, raw := testutil.NewTestDB(t)
	store := identitystore.New(raw)

	res := protocolauth.New(store, acl.New(store), false)
	// SFTP can enforce a confinement on every path, so it HONOURS a confined
	// credential rather than refusing it — the opposite of /dav, which has no
	// place to enforce one.
	res.Confine = protocolauth.ConfineHonor

	hz := &harness{store: store, res: res, roots: map[int64]string{}}
	srv, err := sftpsrv.New(sftpsrv.Config{
		Enabled:    true,
		Addr:       "127.0.0.1:0",
		HostKeyDir: t.TempDir(),
		Store:      store,
		Auth:       res,
		ACL:        acl.New(store),
		Body:       filebody.New(store, nil),
		Quota:      quota.New(store),
		SpoolDir:   t.TempDir(),
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

	ready := make(chan struct{})
	go func() {
		close(ready)
		_ = srv.ListenAndServe()
	}()
	<-ready
	// The listener is bound inside ListenAndServe; wait for the address rather
	// than sleeping a fixed amount, which is how a suite becomes flaky on a
	// loaded machine.
	for i := 0; i < 200 && srv.Addr() == ""; i++ {
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

// dial opens a real SSH connection and starts the SFTP subsystem on it.
func (hz *harness) dial(t *testing.T, login, password string) (*sftp.Client, error) {
	t.Helper()
	return hz.dialAuth(t, login, ssh.Password(password))
}

func (hz *harness) dialAuth(t *testing.T, login string, auth ssh.AuthMethod) (*sftp.Client, error) {
	t.Helper()
	conn, err := ssh.Dial("tcp", hz.addr, &ssh.ClientConfig{
		User:            login,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // the key is generated per test
		Timeout:         10 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	cl, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	t.Cleanup(func() {
		_ = cl.Close()
		_ = conn.Close()
	})
	return cl, nil
}

func (hz *harness) mustDial(t *testing.T, login, password string) *sftp.Client {
	t.Helper()
	cl, err := hz.dial(t, login, password)
	if err != nil {
		t.Fatalf("dial as %s: %v", login, err)
	}
	return cl
}

// ─────────────────────────── authentication ───────────────────────────

func TestPasswordLoginAndRefusals(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	hz.storage(t, "main")

	if _, err := hz.dial(t, "sftp@example.com", testPassword); err != nil {
		t.Fatalf("correct password refused: %v", err)
	}
	if _, err := hz.dial(t, "sftp@example.com", "wrong"); err == nil {
		t.Fatal("a wrong password was accepted")
	}
	if _, err := hz.dial(t, "nobody@example.com", testPassword); err == nil {
		t.Fatal("an unknown account was accepted")
	}
}

// ⚠ The username is a real login here, not decoration: `sftp user@host` puts an
// e-mail in a place clients quote badly, which is why the account has a short
// name at all.
func TestUsernameIsAValidLogin(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "grace@example.com")
	hz.storage(t, "main")
	if u.Username == "" {
		t.Fatal("the account was created without a username")
	}

	if _, err := hz.dial(t, u.Username, testPassword); err != nil {
		t.Fatalf("login with the username was refused: %v", err)
	}
	if _, err := hz.dial(t, u.Email, testPassword); err != nil {
		t.Fatalf("login with the e-mail was refused: %v", err)
	}
}

// ⚠⚠ An account with a second factor must not be reachable with the password
// alone: SSH has no channel to ask for the code here, so accepting it would
// make this endpoint a documented 2FA bypass.
func TestTOTPAccountCannotUseItsPassword(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "twofa@example.com")
	hz.storage(t, "main")
	if err := hz.store.SetTotpPendingSecret(context.Background(), u.ID, "JBSWY3DPEHPK3PXP", []string{"a"}); err != nil {
		t.Fatalf("totp secret: %v", err)
	}
	if err := hz.store.ActivateTotp(context.Background(), u.ID); err != nil {
		t.Fatalf("activate totp: %v", err)
	}

	if _, err := hz.dial(t, "twofa@example.com", testPassword); err == nil {
		t.Fatal("a 2FA account logged in with its password")
	}
}

func TestPublicKeyLogin(t *testing.T) {
	hz := newHarness(t)
	owner := hz.user(t, "keys@example.com")
	other := hz.user(t, "other@example.com")
	hz.storage(t, "main")

	signer, pub := testKey(t)
	fp, wire, _, err := sftpsrv.ParseAuthorizedKey(string(ssh.MarshalAuthorizedKey(pub)))
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	if _, err := hz.store.CreateSSHPublicKey(context.Background(), &model.SSHPublicKey{
		UserID: owner.ID, Name: "laptop", Fingerprint: fp, PublicKey: wire,
	}); err != nil {
		t.Fatalf("register key: %v", err)
	}

	if _, err := hz.dialAuth(t, "keys@example.com", ssh.PublicKeys(signer)); err != nil {
		t.Fatalf("registered key refused: %v", err)
	}
	// ⚠ The key belongs to ONE account. Logging in as somebody else with it —
	// with a perfectly valid signature — is impersonation.
	if _, err := hz.dialAuth(t, "other@example.com", ssh.PublicKeys(signer)); err == nil {
		t.Fatal("a key registered by one account logged in as another")
	}
	_ = other

	// An unregistered key is refused even though its signature is valid.
	stranger, _ := testKey(t)
	if _, err := hz.dialAuth(t, "keys@example.com", ssh.PublicKeys(stranger)); err == nil {
		t.Fatal("an unregistered key was accepted")
	}
}

func TestDisabledKeyIsRefused(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "keys@example.com")
	hz.storage(t, "main")
	signer, pub := testKey(t)
	fp, wire, _, _ := sftpsrv.ParseAuthorizedKey(string(ssh.MarshalAuthorizedKey(pub)))
	k, err := hz.store.CreateSSHPublicKey(context.Background(), &model.SSHPublicKey{
		UserID: u.ID, Fingerprint: fp, PublicKey: wire,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := hz.store.SetSSHPublicKeyDisabled(context.Background(), k.ID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if _, err := hz.dialAuth(t, "keys@example.com", ssh.PublicKeys(signer)); err == nil {
		t.Fatal("a disabled key still logged in")
	}
}

func testKey(t *testing.T) (ssh.Signer, ssh.PublicKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return signer, signer.PublicKey()
}

// ─────────────────────────── the tree ───────────────────────────

func TestRootListsTheStoragesTheCallerCanSee(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "sftp@example.com")
	hz.storage(t, "main")
	locked := hz.storage(t, "locked")
	locked.RBACEnabled = true
	if err := hz.store.UpdateStorage(context.Background(), locked); err != nil {
		t.Fatalf("update storage: %v", err)
	}
	_ = u

	cl := hz.mustDial(t, "sftp@example.com", testPassword)
	entries, err := cl.ReadDir("/")
	if err != nil {
		t.Fatalf("readdir /: %v", err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != "main" {
		t.Fatalf("root = %v, want only main — an RBAC storage with no grant is invisible", names)
	}
}

// ⚠⚠ REALPATH is mandatory and must answer an ABSOLUTE path: OpenSSH calls
// fatal("Need cwd") without it, and a relative answer gives every client a
// `cd ..` that never terminates.
func TestRealPathIsAbsolute(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	hz.storage(t, "main")
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	for in, want := range map[string]string{
		".":            "/",
		"/":            "/",
		"main":         "/main",
		"/main/../":    "/",
		"/main/a/../b": "/main/b",
	} {
		got, err := cl.RealPath(in)
		if err != nil {
			t.Fatalf("realpath %q: %v", in, err)
		}
		if got != want {
			t.Errorf("realpath %q = %q, want %q", in, got, want)
		}
	}
}

func TestReadAndWriteRoundTrip(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "docs/report.txt", []byte("the quick brown fox"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	// Read what was already there.
	f, err := cl.Open("/main/docs/report.txt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	got, err := io.ReadAll(f)
	f.Close()
	if err != nil || string(got) != "the quick brown fox" {
		t.Fatalf("read = %q (%v)", got, err)
	}

	// Write something new.
	w, err := cl.Create("/main/docs/new.txt")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := w.Write([]byte("written over sftp")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "docs", "new.txt"))
	if err != nil || string(onDisk) != "written over sftp" {
		t.Fatalf("on disk = %q (%v)", onDisk, err)
	}
	// ⚠ And the bookkeeping every other surface does: without the node row the
	// file is invisible in the explorer, unsearchable and unthumbnailed until
	// some later sync happens to notice it.
	node, err := hz.store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/docs/new.txt"))
	if err != nil || node == nil {
		t.Fatalf("no node row for an SFTP upload: %v", err)
	}
	if node.Size != int64(len("written over sftp")) {
		t.Errorf("node size = %d", node.Size)
	}
}

// ⚠⚠ The request server hands ONE handle to eight workers with no lock, so a
// plain sequential put arrives as concurrent WriteAt calls at unordered
// offsets. This is the test that fails when that is not accounted for — and it
// fails at size, not on a 1 MB fixture.
func TestLargeFileRoundTripIsByteIdentical(t *testing.T) {
	if testing.Short() {
		t.Skip("large transfer")
	}
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	// 12 MiB of non-repeating data: a pattern that repeats would hide an
	// offset mix-up, which is exactly the bug this exists to catch.
	body := make([]byte, 12<<20)
	for i := range body {
		body[i] = byte(i*31 + i/251)
	}

	w, err := cl.Create("/main/big.bin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "big.bin"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(onDisk, body) {
		t.Fatalf("upload differs: %d bytes stored, want %d", len(onDisk), len(body))
	}

	// …and back down again, through the block cache and the worker pool.
	r, err := cl.Open("/main/big.bin")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer r.Close()
	var buf bytes.Buffer
	if _, err := r.WriteTo(&buf); err != nil {
		t.Fatalf("download: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), body) {
		t.Fatalf("download differs: %d bytes, want %d", buf.Len(), len(body))
	}

	// ⚠⚠ And now the part a sequential transfer does NOT prove. The first
	// version of this test passed with a server that ignored the offset
	// entirely and appended instead — because a fast local socket happened to
	// deliver the packets in order. Writing the halves BACKWARDS is what makes
	// the offset load-bearing, and it is also what a resumed transfer and the
	// library's own eight workers produce.
	rev, err := cl.OpenFile("/main/reversed.bin", os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		t.Fatalf("open for writing: %v", err)
	}
	half := len(body) / 2
	if _, err := rev.WriteAt(body[half:], int64(half)); err != nil {
		t.Fatalf("write second half: %v", err)
	}
	if _, err := rev.WriteAt(body[:half], 0); err != nil {
		t.Fatalf("write first half: %v", err)
	}
	if err := rev.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	outOfOrder, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "reversed.bin"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(outOfOrder, body) {
		t.Fatalf("out-of-order upload differs: %d bytes stored, want %d — the offset was not honoured",
			len(outOfOrder), len(body))
	}

	// The same for reads: a ranged read at an arbitrary offset must return the
	// bytes at THAT offset, not the ones a sequential reader happened to be at.
	rr, err := cl.Open("/main/reversed.bin")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rr.Close()
	for _, off := range []int64{int64(len(body)) - 4096, 7 << 20, 4096, 0} {
		want := body[off : off+4096]
		got := make([]byte, 4096)
		if _, err := rr.ReadAt(got, off); err != nil && err != io.EOF {
			t.Fatalf("ReadAt(%d): %v", off, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("ReadAt(%d) returned the wrong bytes", off)
		}
	}
}

// A Turkish name has to survive the round trip. SFTP v3 has no defined
// filename charset, and a client that sniffs badly shows mojibake — so the
// bytes filex emits must be plain UTF-8 and must come back unchanged.
func TestTurkishFilenamesSurvive(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	const name = "/main/gölge dosya şğüöç.txt"
	w, err := cl.Create(name)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w.Write([]byte("türkçe"))
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	entries, err := cl.ReadDir("/main")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name() == "gölge dosya şğüöç.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the Turkish name is not in the listing: %v", names(entries))
	}
	if _, err := cl.Stat(name); err != nil {
		t.Fatalf("stat by the same name failed: %v", err)
	}
	// And on disk with the same bytes, not a re-encoded approximation.
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "gölge dosya şğüöç.txt")); err != nil {
		t.Fatalf("the file is not on disk under its own name: %v", err)
	}
}

func names(entries []os.FileInfo) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// ─────────────────────────── the verbs ───────────────────────────

func TestMkdirAndRemoveGoThroughTheTrash(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "gone.txt", []byte("bye"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	if err := cl.Mkdir("/main/newdir"); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(hz.rootOf(t, st), "newdir")); err != nil || !fi.IsDir() {
		t.Fatalf("no directory was created: %v", err)
	}

	if err := cl.Remove("/main/gone.txt"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "gone.txt")); err == nil {
		t.Fatal("the file is still at its old path")
	}
	// ⚠ To the TRASH, like every other surface. "It was gone forever because I
	// used WinSCP" is not a rule anybody can hold in their head.
	trashed, _ := filepath.Glob(filepath.Join(hz.rootOf(t, st), ".filex-trash", "*gone.txt"))
	if len(trashed) == 0 {
		t.Fatal("a deleted file did not reach the trash")
	}
}

// ⚠⚠ POSIX rename is the one that matters: every write-temp-then-rename tool
// (rclone, backups, the desktop sync) breaks on its SECOND run without an
// overwriting rename, because the destination now exists.
func TestRenameSemantics(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "a.txt", []byte("first"))
	hz.writeFile(t, st, "b.txt", []byte("second"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	// Plain v3 RENAME refuses an existing destination.
	if err := cl.Rename("/main/a.txt", "/main/b.txt"); err == nil {
		t.Fatal("v3 rename overwrote an existing file")
	}
	// PosixRename replaces it.
	if err := cl.PosixRename("/main/a.txt", "/main/b.txt"); err != nil {
		t.Fatalf("posix-rename: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "b.txt"))
	if err != nil || string(got) != "first" {
		t.Fatalf("after posix-rename b.txt = %q (%v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "a.txt")); err == nil {
		t.Error("the source survived a rename")
	}
}

// ⚠ Clients chmod and utime after EVERY upload. An error here gives WinSCP a
// warning dialog on every single file, which is how a working server gets
// reported as broken.
func TestSetstatSucceedsAndCarriesTheTimestamp(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "t.txt", []byte("x"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	if err := cl.Chmod("/main/t.txt", 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	want := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := cl.Chtimes("/main/t.txt", want, want); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	fi, err := os.Stat(filepath.Join(hz.rootOf(t, st), "t.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// The one attribute filex can genuinely keep — and the one a sync tool
	// needs kept, or it copies everything again next run.
	if got := fi.ModTime().UTC(); !got.Equal(want) {
		t.Errorf("mtime = %s, want %s", got, want)
	}
}

// filex has no symlinks. Refusing is not a gap: `Symlink` is the ONE request
// whose path the library does not clean, so answering it is also the one place
// a traversal could be smuggled in.
func TestSymlinkIsRefused(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "a.txt", []byte("x"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	if err := cl.Symlink("/main/a.txt", "/main/link.txt"); err == nil {
		t.Fatal("a symlink was created")
	}
	if _, err := os.Lstat(filepath.Join(hz.rootOf(t, st), "link.txt")); err == nil {
		t.Fatal("something was created for the refused symlink")
	}
}

// ─────────────────────────── permission ───────────────────────────

func TestReadOnlyStorageRefusesWrites(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	st.ReadOnly = true
	if err := hz.store.UpdateStorage(context.Background(), st); err != nil {
		t.Fatalf("update: %v", err)
	}
	hz.writeFile(t, st, "a.txt", []byte("readable"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	if _, err := cl.Create("/main/nope.txt"); err == nil {
		t.Fatal("a read-only storage accepted a write")
	}
	// …and it is still readable, which is the point of read-only.
	f, err := cl.Open("/main/a.txt")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.Close()
}

// A confined credential is honoured here rather than refused — SFTP can
// enforce it on every path, which turns a subtree-scoped token into a
// legitimate scoped login.
func TestConfinedCredentialSeesOnlyItsSubtree(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.storage(t, "other")
	hz.writeFile(t, st, "projects/acme/ok.txt", []byte("mine"))
	hz.writeFile(t, st, "secrets/theirs.txt", []byte("not mine"))

	tok := hz.token(t, u, "read,write,root:main://projects/acme")
	cl, err := hz.dial(t, "sftp@example.com", tok)
	if err != nil {
		t.Fatalf("dial with a confined token: %v", err)
	}

	// Only the storage the confinement names.
	entries, err := cl.ReadDir("/")
	if err != nil {
		t.Fatalf("readdir /: %v", err)
	}
	if got := names(entries); len(got) != 1 || got[0] != "main" {
		t.Fatalf("root = %v, want only main", got)
	}
	// Inside the root: fine.
	if _, err := cl.Stat("/main/projects/acme/ok.txt"); err != nil {
		t.Fatalf("stat inside the confinement: %v", err)
	}
	// Outside it: absent, not forbidden — a permission error would confirm the
	// path exists.
	if _, err := cl.Stat("/main/secrets/theirs.txt"); err == nil {
		t.Fatal("a confined credential reached outside its root")
	}
	if _, err := cl.Create("/main/secrets/new.txt"); err == nil {
		t.Fatal("a confined credential wrote outside its root")
	}
}

// token mints an API token for this user. The helper is shared with the FTPS
// suite — three protocols needing the same thing is three chances to drift
// from how the product actually hashes a token.
func (hz *harness) token(t *testing.T, u *model.User, scopes string) string {
	t.Helper()
	return testutil.NewAPIToken(t, hz.store, u.ID, scopes)
}

func TestQuotaIsEnforcedBeforeTheBytesLand(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	if err := quota.New(hz.store).SetQuota(context.Background(), u.ID, 64); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	w, err := cl.Create("/main/too-big.bin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, _ = w.Write(bytes.Repeat([]byte("x"), 4096))
	if err := w.Close(); err == nil {
		t.Fatal("an over-quota upload was accepted")
	}
	// ⚠ And it is not on disk. A quota checked after the write means the disk
	// already holds what the quota was meant to prevent.
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "too-big.bin")); err == nil {
		t.Fatal("the over-quota object was written anyway")
	}
}

func TestStatVFSAnswers(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "sftp@example.com")
	hz.storage(t, "main")
	if err := quota.New(hz.store).SetQuota(context.Background(), u.ID, 10<<20); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	vfs, err := cl.StatVFS("/main")
	if err != nil {
		t.Fatalf("statvfs: %v", err)
	}
	if vfs.Blocks == 0 || vfs.Bavail == 0 {
		t.Fatalf("statvfs reported no space (blocks=%d avail=%d) — WinSCP shows a full disk and refuses to transfer",
			vfs.Blocks, vfs.Bavail)
	}
	if got := vfs.Blocks * uint64(vfs.Bsize); got != 10<<20 {
		t.Errorf("total = %d bytes, want the account quota (%d)", got, 10<<20)
	}
}

func TestHiddenBucketsAreInvisible(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, ".filex-trash/old.txt", []byte("trashed"))
	hz.writeFile(t, st, "visible.txt", []byte("x"))
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	entries, err := cl.ReadDir("/main")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() == ".filex-trash" {
			t.Fatal("the trash bucket is listed")
		}
	}
	if _, err := cl.Stat("/main/.filex-trash/old.txt"); err == nil {
		t.Fatal("a path inside the trash bucket is reachable")
	}
}

func TestUnknownStorageIsAbsentNotForbidden(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	hz.storage(t, "main")
	cl := hz.mustDial(t, "sftp@example.com", testPassword)

	_, err := cl.Stat("/nope/file.txt")
	if err == nil {
		t.Fatal("an unknown storage answered")
	}
	// ⚠ NoSuchFile, not PermissionDenied: "forbidden" confirms the path
	// exists, and across tenants that is an existence oracle.
	if !os.IsNotExist(err) {
		t.Errorf("error = %v, want a not-exist error", err)
	}
	if fmt.Sprint(err) == "permission denied" {
		t.Error("permission denied tells a stranger the storage is real")
	}
}

// ⚠⚠ The subsystem must report an exit status when it ends.
//
// OpenSSH's `scp` has spoken SFTP rather than the old protocol since 9.0, and
// it takes the CHANNEL's exit status as its own. Without one, every scp against
// filex ended in a silent `exit 1` — with the bytes already transferred, no
// error printed anywhere, and every script around it deciding the copy had
// failed. (Measured 2026-08-16 against OpenSSH 9.6: the file was on the server
// and scp still said no.)
func TestSubsystemReportsAnExitStatus(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "sftp@example.com")
	hz.storage(t, "main")

	conn, err := ssh.Dial("tcp", hz.addr, &ssh.ClientConfig{
		User:            "sftp@example.com",
		Auth:            []ssh.AuthMethod{ssh.Password(testPassword)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// The low-level API on purpose: ssh.Session.Wait refuses a session that was
	// started as a SUBSYSTEM rather than a command, so it cannot see the very
	// request this test is about. Reading the channel's requests directly is
	// what scp effectively does.
	ch, reqs, err := conn.OpenChannel("session", nil)
	if err != nil {
		t.Fatalf("open channel: %v", err)
	}
	defer ch.Close()

	type subsystemMsg struct{ Subsystem string }
	ok, err := ch.SendRequest("subsystem", true, ssh.Marshal(&subsystemMsg{Subsystem: "sftp"}))
	if err != nil || !ok {
		t.Fatalf("subsystem request: ok=%v err=%v", ok, err)
	}

	status := make(chan uint32, 1)
	go func() {
		for req := range reqs {
			if req.Type == "exit-status" && len(req.Payload) >= 4 {
				var msg struct{ Status uint32 }
				if ssh.Unmarshal(req.Payload, &msg) == nil {
					status <- msg.Status
					return
				}
			}
		}
		close(status)
	}()

	// End the session the way a client that is done does.
	_ = ch.CloseWrite()

	select {
	case st, got := <-status:
		if !got {
			t.Fatal("the subsystem ended without an exit status — scp reads that as a failed copy")
		}
		if st != 0 {
			t.Fatalf("exit status = %d, want 0", st)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("no exit status arrived — scp waits for it and then reports failure")
	}
}

// ⚠⚠ The headline of the live-session registry: "I deleted the token" must not
// mean "the session that token opened is still copying files".
//
// An SFTP connection authenticates ONCE and then serves for hours, so before
// this existed the delete was true for the next login and false for the one in
// flight. Asserted against a real client on a real connection, because the
// thing being tested is precisely that the socket goes away.
func TestDeletingATokenClosesTheSessionItOpened(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "sftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "notlar.txt", []byte("gölge"))

	secret := hz.token(t, u, "read,write")
	cl := hz.mustDial(t, "sftp@example.com", secret)
	defer cl.Close()

	if _, err := cl.ReadDir("/main"); err != nil {
		t.Fatalf("readdir before revocation: %v", err)
	}

	tokens, err := hz.store.ListAPITokensByUser(context.Background(), u.ID)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("list tokens: %v (%d)", err, len(tokens))
	}
	if err := hz.store.DeleteAPIToken(context.Background(), tokens[0].ID); err != nil {
		t.Fatalf("delete token: %v", err)
	}
	if n := hz.res.Kick(protocolauth.KickToken, tokens[0].ID); n != 1 {
		t.Fatalf("kick cut %d sessions, want 1", n)
	}

	// The hang-up happens on another goroutine (a closer must never block the
	// sweep), so the client sees it on its next request rather than instantly.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := cl.ReadDir("/main"); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the session kept listing files after its token was deleted")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ⚠⚠ The folder-name leak. A grant is per-FOLDER, so a caller can hold a
// viewer grant on `/main/mine` while `/main/theirs` is somebody else's — and a
// listing of `/main` must not mention `theirs` AT ALL.
//
// The name alone is the leak: "acme-acquisition" in a directory listing is a
// fact the caller was never given. Reducing the mode bits is not enough, and
// failing the whole listing because one entry is out of reach is worse — it
// hides the directory the caller legitimately has.
func TestUnreachableSiblingsAreAbsentFromTheListing(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "sftp@example.com")
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

	cl := hz.mustDial(t, "sftp@example.com", testPassword)
	defer cl.Close()

	entries, err := cl.ReadDir("/main")
	if err != nil {
		t.Fatalf("readdir /main: %v", err)
	}
	for _, e := range entries {
		if e.Name() == "acme-acquisition" {
			t.Fatal("a folder the caller has no grant on appeared in the listing by name")
		}
	}
	found := false
	for _, e := range entries {
		if e.Name() == "mine" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the granted folder is missing from the listing: %v", entries)
	}

	// And it is genuinely out of reach, not merely hidden from `ls`.
	if _, err := cl.Open("/main/acme-acquisition/plan.txt"); err == nil {
		t.Fatal("a file under an ungranted folder was readable")
	}

	// ⚠ The write denial has the OPPOSITE shape on purpose: a viewer grant is
	// a path the caller can see, so answering "no such file" to their write
	// would make a client retry forever against a permission problem.
	if _, err := cl.Create("/main/mine/nope.txt"); err == nil {
		t.Fatal("a viewer grant was allowed to create a file")
	}
}
