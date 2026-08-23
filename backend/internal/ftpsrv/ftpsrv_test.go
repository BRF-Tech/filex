package ftpsrv_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	goftp "github.com/jlaffaye/ftp"

	"github.com/brf-tech/filex/backend/internal/acl"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/ftpsrv"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
)

// The FTPS endpoint, driven by a REAL FTP client over a real socket —
// jlaffaye/ftp, the same library filex uses as an FTP *client* for its own
// storage driver. Nothing here calls a handler directly.

const testPassword = "FtpPass!1"

type harness struct {
	srv     *ftpsrv.Server
	store   db.Store
	addr    string
	roots   map[int64]string
	certDir string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	_, raw := testutil.NewTestDB(t)
	store := identitystore.New(raw)

	res := protocolauth.New(store, acl.New(store), false)
	// FTPS enforces a confinement on every path, so it honours a confined
	// credential rather than refusing it — same as SFTP.
	res.Confine = protocolauth.ConfineHonor

	hz := &harness{store: store, roots: map[int64]string{}, certDir: t.TempDir()}
	srv, err := ftpsrv.New(ftpsrv.Config{
		Enabled:    true,
		Addr:       "127.0.0.1:0",
		PublicHost: "127.0.0.1",
		// A tiny range: the test only ever has one transfer in flight, and a
		// wide one on a shared machine is a good way to collide with something.
		PassivePortMin: 0,
		PassivePortMax: 0,
		CertDir:        hz.certDir,
		Store:          store,
		Auth:           res,
		ACL:            acl.New(store),
		Body:           filebody.New(store, nil),
		Quota:          quota.New(store),
		SpoolDir:       t.TempDir(),
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

// dial connects with explicit TLS — the only way in.
func (hz *harness) dial(t *testing.T) (*goftp.ServerConn, error) {
	t.Helper()
	// InsecureSkipVerify because the harness generates a fresh self-signed
	// certificate per run. What is being measured is that TLS is REQUIRED, not
	// that this particular certificate chains to anything.
	c, err := goftp.Dial(hz.addr,
		goftp.DialWithTimeout(10*time.Second),
		goftp.DialWithExplicitTLS(&tls.Config{InsecureSkipVerify: true}),
	)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = c.Quit() })
	return c, nil
}

func (hz *harness) login(t *testing.T, user, pass string) (*goftp.ServerConn, error) {
	t.Helper()
	c, err := hz.dial(t)
	if err != nil {
		return nil, err
	}
	if err := c.Login(user, pass); err != nil {
		return nil, err
	}
	return c, nil
}

func (hz *harness) mustLogin(t *testing.T, user, pass string) *goftp.ServerConn {
	t.Helper()
	c, err := hz.login(t, user, pass)
	if err != nil {
		t.Fatalf("login as %s: %v", user, err)
	}
	return c
}

// ─────────────────────────── TLS and authentication ───────────────────────────

// ⚠⚠ The one thing this endpoint must never do. Plain FTP sends the password
// in the clear and the file after it, so a cleartext login has to be refused
// BEFORE the password is read, not after.
func TestCleartextLoginIsRefused(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	hz.storage(t, "main")

	// No AUTH TLS: connect and try to log in in the clear.
	c, err := goftp.Dial(hz.addr, goftp.DialWithTimeout(10*time.Second))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Quit()

	if err := c.Login("ftp@example.com", testPassword); err == nil {
		t.Fatal("a cleartext login succeeded — the password crossed the wire in plaintext")
	}
}

func TestExplicitTLSLoginAndRefusals(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	hz.storage(t, "main")

	if _, err := hz.login(t, "ftp@example.com", testPassword); err != nil {
		t.Fatalf("correct password refused: %v", err)
	}
	if _, err := hz.login(t, "ftp@example.com", "wrong"); err == nil {
		t.Fatal("a wrong password was accepted")
	}
	if _, err := hz.login(t, "nobody@example.com", testPassword); err == nil {
		t.Fatal("an unknown account was accepted")
	}
}

func TestUsernameIsAValidLogin(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "grace@example.com")
	hz.storage(t, "main")
	if u.Username == "" {
		t.Fatal("the account was created without a username")
	}
	if _, err := hz.login(t, u.Username, testPassword); err != nil {
		t.Fatalf("login with the username was refused: %v", err)
	}
}

// ⚠ Same rule as every other protocol: no second-factor channel here, so the
// password alone must not get in.
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
	if _, err := hz.login(t, "twofa@example.com", testPassword); err == nil {
		t.Fatal("a 2FA account logged in with its password")
	}
}

// ─────────────────────────── the tree ───────────────────────────

func TestRootListsTheStoragesTheCallerCanSee(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	hz.storage(t, "main")
	locked := hz.storage(t, "locked")
	locked.RBACEnabled = true
	if err := hz.store.UpdateStorage(context.Background(), locked); err != nil {
		t.Fatalf("update storage: %v", err)
	}

	c := hz.mustLogin(t, "ftp@example.com", testPassword)
	entries, err := c.List("/")
	if err != nil {
		t.Fatalf("list /: %v", err)
	}
	names := entryNames(entries)
	if len(names) != 1 || names[0] != "main" {
		t.Fatalf("root = %v, want only main", names)
	}
}

func entryNames(entries []*goftp.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name == "." || e.Name == ".." {
			continue
		}
		out = append(out, e.Name)
	}
	return out
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "docs/report.txt", []byte("already here"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	// Read what was already there.
	r, err := c.Retr("/main/docs/report.txt")
	if err != nil {
		t.Fatalf("retr: %v", err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(got) != "already here" {
		t.Fatalf("retr = %q (%v)", got, err)
	}

	// Write something new, big enough to be more than one packet.
	body := bytes.Repeat([]byte("ftps payload. "), 200000) // ~2.8 MB
	if err := c.Stor("/main/docs/new.bin", bytes.NewReader(body)); err != nil {
		t.Fatalf("stor: %v", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "docs", "new.bin"))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(onDisk, body) {
		t.Fatalf("stored %d bytes, want %d", len(onDisk), len(body))
	}

	// ⚠ And the bookkeeping: without the node row the file is invisible in the
	// explorer, unsearchable and unthumbnailed.
	node, err := hz.store.GetNodeByPath(context.Background(), st.ID, pathkey.Hash(st.ID, "/docs/new.bin"))
	if err != nil || node == nil {
		t.Fatalf("no node row for an FTP upload: %v", err)
	}
	if node.Size != int64(len(body)) {
		t.Errorf("node size = %d, want %d", node.Size, len(body))
	}
}

// ⚠ Binary, always. FTP's ASCII mode rewrites line endings, and a client that
// guessed wrong silently corrupts somebody's file.
func TestTransfersAreByteExactEvenInASCIIMode(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	// CRLF, bare LF and a stray CR: the three things ASCII mode mangles.
	body := []byte("line one\r\nline two\nline three\rtail")
	if err := c.Type(goftp.TransferTypeASCII); err != nil {
		t.Fatalf("type A: %v", err)
	}
	if err := c.Stor("/main/mixed.txt", bytes.NewReader(body)); err != nil {
		t.Fatalf("stor: %v", err)
	}
	onDisk, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "mixed.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(onDisk, body) {
		t.Fatalf("ASCII mode rewrote the bytes: %q, want %q", onDisk, body)
	}
}

func TestTurkishFilenamesSurvive(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	const name = "/main/gölge dosya şğüöç.txt"
	if err := c.Stor(name, strings.NewReader("türkçe")); err != nil {
		t.Fatalf("stor: %v", err)
	}
	entries, err := c.List("/main")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Name == "gölge dosya şğüöç.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the Turkish name is not in the listing: %v", entryNames(entries))
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "gölge dosya şğüöç.txt")); err != nil {
		t.Fatalf("not on disk under its own name: %v", err)
	}
}

// REST is how a client resumes an interrupted download.
//
// ⚠ Answering it by starting at zero would be worse than refusing: the client
// believes it resumed and appends the whole file onto the partial one.
func TestResumeStartsAtTheRequestedOffset(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	body := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	hz.writeFile(t, st, "r.bin", body)
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	r, err := c.RetrFrom("/main/r.bin", 10)
	if err != nil {
		t.Fatalf("retr from: %v", err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, body[10:]) {
		t.Fatalf("resumed read = %q, want %q", got, body[10:])
	}
}

// APPE, and STOR after REST: the existing bytes must survive.
func TestAppendKeepsWhatWasAlreadyThere(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "log.txt", []byte("first line\n"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	if err := c.Append("/main/log.txt", strings.NewReader("second line\n")); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(hz.rootOf(t, st), "log.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "first line\nsecond line\n" {
		t.Fatalf("after append: %q — an append that truncates is data loss", got)
	}
}

// ─────────────────────────── the verbs ───────────────────────────

func TestMkdirRenameAndDeleteToTheTrash(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "old.txt", []byte("bye"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	if err := c.MakeDir("/main/newdir"); err != nil {
		t.Fatalf("mkd: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(hz.rootOf(t, st), "newdir")); err != nil || !fi.IsDir() {
		t.Fatalf("no directory: %v", err)
	}

	if err := c.Rename("/main/old.txt", "/main/newdir/renamed.txt"); err != nil {
		t.Fatalf("rnfr/rnto: %v", err)
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "newdir", "renamed.txt")); err != nil {
		t.Fatalf("rename did not land: %v", err)
	}

	if err := c.Delete("/main/newdir/renamed.txt"); err != nil {
		t.Fatalf("dele: %v", err)
	}
	// ⚠ To the TRASH, like every other surface.
	trashed, _ := filepath.Glob(filepath.Join(hz.rootOf(t, st), ".filex-trash", "*renamed.txt"))
	if len(trashed) == 0 {
		t.Fatal("a deleted file did not reach the trash")
	}
}

// ⚠ Clients chmod after an upload out of habit; an error there turns a
// completed transfer into a reported failure. MFMT, on the other hand, carries
// the one attribute filex can genuinely keep.
func TestSetTimeIsHonoured(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, "t.txt", []byte("x"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	want := time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	if err := c.SetTime("/main/t.txt", want); err != nil {
		t.Fatalf("mfmt: %v", err)
	}
	fi, err := os.Stat(filepath.Join(hz.rootOf(t, st), "t.txt"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := fi.ModTime().UTC(); !got.Equal(want) {
		t.Errorf("mtime = %s, want %s", got, want)
	}
}

// ─────────────────────────── permission ───────────────────────────

func TestReadOnlyStorageRefusesWrites(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	st.ReadOnly = true
	if err := hz.store.UpdateStorage(context.Background(), st); err != nil {
		t.Fatalf("update: %v", err)
	}
	hz.writeFile(t, st, "a.txt", []byte("readable"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	if err := c.Stor("/main/nope.txt", strings.NewReader("x")); err == nil {
		t.Fatal("a read-only storage accepted a write")
	}
	r, err := c.Retr("/main/a.txt")
	if err != nil {
		t.Fatalf("read-only storage refused a READ: %v", err)
	}
	r.Close()
}

func TestHiddenBucketsAreInvisible(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	hz.writeFile(t, st, ".filex-trash/old.txt", []byte("trashed"))
	hz.writeFile(t, st, "visible.txt", []byte("x"))
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	entries, err := c.List("/main")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, e := range entries {
		if e.Name == ".filex-trash" {
			t.Fatal("the trash bucket is listed")
		}
	}
	if _, err := c.Retr("/main/.filex-trash/old.txt"); err == nil {
		t.Fatal("a path inside the trash bucket is reachable")
	}
}

func TestQuotaIsEnforcedBeforeTheBytesLand(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	if err := quota.New(hz.store).SetQuota(context.Background(), u.ID, 64); err != nil {
		t.Fatalf("set quota: %v", err)
	}
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	if err := c.Stor("/main/too-big.bin", bytes.NewReader(bytes.Repeat([]byte("x"), 4096))); err == nil {
		t.Fatal("an over-quota upload was accepted")
	}
	if _, err := os.Stat(filepath.Join(hz.rootOf(t, st), "too-big.bin")); err == nil {
		t.Fatal("the over-quota object was written anyway")
	}
}

func TestConfinedCredentialSeesOnlyItsSubtree(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "ftp@example.com")
	st := hz.storage(t, "main")
	hz.storage(t, "other")
	hz.writeFile(t, st, "projects/acme/ok.txt", []byte("mine"))
	hz.writeFile(t, st, "secrets/theirs.txt", []byte("not mine"))

	tok := testutil.NewAPIToken(t, hz.store, u.ID, "read,write,root:main://projects/acme")
	c, err := hz.login(t, "ftp@example.com", tok)
	if err != nil {
		t.Fatalf("login with a confined token: %v", err)
	}

	entries, err := c.List("/")
	if err != nil {
		t.Fatalf("list /: %v", err)
	}
	if got := entryNames(entries); len(got) != 1 || got[0] != "main" {
		t.Fatalf("root = %v, want only main", got)
	}
	if _, err := c.FileSize("/main/projects/acme/ok.txt"); err != nil {
		t.Fatalf("size inside the confinement: %v", err)
	}
	if _, err := c.Retr("/main/secrets/theirs.txt"); err == nil {
		t.Fatal("a confined credential read outside its root")
	}
	if err := c.Stor("/main/secrets/new.txt", strings.NewReader("x")); err == nil {
		t.Fatal("a confined credential wrote outside its root")
	}
}

func TestUnknownStorageIsAbsent(t *testing.T) {
	hz := newHarness(t)
	hz.user(t, "ftp@example.com")
	hz.storage(t, "main")
	c := hz.mustLogin(t, "ftp@example.com", testPassword)

	if _, err := c.Retr("/nope/file.txt"); err == nil {
		t.Fatal("an unknown storage answered")
	}
}

// ⚠⚠ The folder-name leak, and the traversal bug underneath it. See the SFTP
// test of the same name: a grant is per-FOLDER, so listing the level above must
// work (otherwise the granted folder is unreachable) while showing NOTHING the
// caller was not given. The name alone is the leak.
func TestUnreachableSiblingsAreAbsentFromTheListing(t *testing.T) {
	hz := newHarness(t)
	u := hz.user(t, "ftp@example.com")
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

	c := hz.mustLogin(t, "ftp@example.com", testPassword)
	entries, err := c.List("/main")
	if err != nil {
		t.Fatalf("list /main: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Name] = true
	}
	if seen["acme-acquisition"] {
		t.Fatal("a folder the caller has no grant on appeared in the listing by name")
	}
	if !seen["mine"] {
		t.Fatalf("the granted folder is missing from the listing: %v", seen)
	}
	if _, err := c.Retr("/main/acme-acquisition/plan.txt"); err == nil {
		t.Fatal("a file under an ungranted folder was readable")
	}
}
