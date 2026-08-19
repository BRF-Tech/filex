package ftpsrv

// FTP(S) over a PLUGIN-backed storage.
//
// Why this file exists: the FTP surface hands the library an afero-shaped
// filesystem and does every transfer through ONE door, GetHandle, which
// returns a Reader+Writer+Seeker. That seeker is what REST (resume) and APPE
// are built on, so over a plugin an FTP download is a ranged read and an FTP
// resume is a ranged read at an offset the client chose. Nothing in the plugin
// package's own tests goes near that shape.
//
// ⚠ These drive the fs layer DIRECTLY rather than through a real FTP client.
// The reason is concrete and lives in this package: New() forces the passive
// data-port range to 30000-30100 whatever the caller asks for, so every
// wire-level test in CI competes for the same hundred ports with any other
// package doing the same. The wire half — TLS, PASV, the command grammar — is
// driver-agnostic and ftpsrv_test.go already covers it over the local driver;
// what is plugin-specific is everything below fs, which is what this drives.

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/brf-tech/filex/backend/internal/acl"
	authlocal "github.com/brf-tech/filex/backend/internal/auth/drivers/local"
	"github.com/brf-tech/filex/backend/internal/filebody"
	"github.com/brf-tech/filex/backend/internal/identitystore"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/plugin"
	"github.com/brf-tech/filex/backend/internal/plugin/testplugin"
	"github.com/brf-tech/filex/backend/internal/protocolauth"
	"github.com/brf-tech/filex/backend/internal/quota"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
)

// newPluginFS stands up the real server (New does not listen) over a storage
// backed by a running plugin, and returns the filesystem an authenticated
// client would be handed.
func newPluginFS(t *testing.T) (*fs, *testplugin.Plugin) {
	t.Helper()
	return newPluginFSWithCaps(t, testplugin.FullCaps())
}

func newPluginFSWithCaps(t *testing.T, caps plugin.Capabilities) (*fs, *testplugin.Plugin) {
	t.Helper()
	ctx := context.Background()
	_, raw := dbtest.NewTestDB(t)
	store := identitystore.New(raw)
	res := protocolauth.New(store, acl.New(store), false)
	p := testplugin.Start(t, testplugin.WithCaps(caps))

	if _, err := store.CreateStorage(ctx, &model.Storage{
		Name: "eklenti", Driver: p.Register(t), Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"/data"}`),
	}); err != nil {
		t.Fatalf("storage: %v", err)
	}

	srv, err := New(Config{
		Enabled: true, Addr: "127.0.0.1:0", PublicHost: "127.0.0.1",
		CertDir: t.TempDir(), SpoolDir: t.TempDir(),
		Store: store, Auth: res, ACL: acl.New(store),
		Body: filebody.New(store, nil), Quota: quota.New(store),
		// The registry, by the storage row's driver name — the same lookup the
		// server performs in production.
		Resolver: func(id int64) (storage.Driver, error) {
			st, err := store.GetStorage(ctx, id)
			if err != nil {
				return nil, err
			}
			var cfg map[string]any
			if err := json.Unmarshal(st.ConfigJSON, &cfg); err != nil {
				return nil, err
			}
			drv, err := storage.Get(st.Driver)
			if err != nil {
				return nil, err
			}
			if err := drv.Init(ctx, cfg); err != nil {
				return nil, err
			}
			return drv, nil
		},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	hash, err := authlocal.HashPassword("FtpPass!1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(ctx, "ftp@example.com", hash, model.RoleAdmin, "en", "UTC")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	principal, err := res.Password(ctx, u.Email, "FtpPass!1")
	if err != nil {
		t.Fatalf("password auth: %v", err)
	}
	return newFS(srv, principal), p
}

// LIST and the size in it. FTP clients render the size straight from the
// listing and use it to decide whether a transfer finished; a plugin whose
// sizes are lost on the way through shows every file as complete at 0 bytes.
func TestPluginStorageListsOverFTP(t *testing.T) {
	f, p := newPluginFS(t)
	p.Seed("belgeler/rapor.txt", "plugin bytes")
	p.Seed("belgeler/alt/derin.txt", "derin")

	// The virtual root lists the storages, so a plugin storage has to be
	// reachable by name before anything below it matters.
	roots, err := f.ReadDir("/")
	if err != nil {
		t.Fatalf("ReadDir /: %v", err)
	}
	var found bool
	for _, fi := range roots {
		if fi.Name() == "eklenti" && fi.IsDir() {
			found = true
		}
	}
	if !found {
		t.Fatalf("plugin storage missing from the root listing: %v", roots)
	}

	fis, err := f.ReadDir("/eklenti/belgeler")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(fis) != 2 {
		t.Fatalf("ReadDir = %d entries, want 2", len(fis))
	}
	for _, fi := range fis {
		switch fi.Name() {
		case "rapor.txt":
			if fi.IsDir() || fi.Size() != 12 {
				t.Fatalf("rapor.txt: dir=%v size=%d", fi.IsDir(), fi.Size())
			}
		case "alt":
			if !fi.IsDir() {
				t.Fatal("alt did not come back as a directory")
			}
		}
	}

	st, err := f.Stat("/eklenti/belgeler/rapor.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Size() != 12 {
		t.Fatalf("Stat size = %d", st.Size())
	}
	if _, err := f.Stat("/eklenti/yok.txt"); !os.IsNotExist(err) {
		t.Fatalf("missing path error = %v, want os.ErrNotExist", err)
	}
}

// RETR, and then RETR with REST — a resumed download. The offset comes from
// the client, so over a plugin this is the adapter's ranged read with an
// arbitrary start; getting it wrong hands the client bytes from the wrong
// place and the resumed file is silently corrupt.
func TestPluginStorageDownloadsAndResumesOverFTP(t *testing.T) {
	f, p := newPluginFS(t)
	p.Seed("f.bin", "0123456789")

	h, err := f.GetHandle("/eklenti/f.bin", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("GetHandle: %v", err)
	}
	whole, err := io.ReadAll(h)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if string(whole) != "0123456789" {
		t.Fatalf("RETR = %q", whole)
	}

	h, err = f.GetHandle("/eklenti/f.bin", os.O_RDONLY, 4)
	if err != nil {
		t.Fatalf("GetHandle at offset: %v", err)
	}
	rest, err := io.ReadAll(h)
	if err != nil {
		t.Fatalf("read at offset: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if string(rest) != "456789" {
		t.Fatalf("REST 4 then RETR = %q, want 456789", rest)
	}
}

// STOR, and the bytes have to be on the BACKEND when the transfer closes —
// the commit happens in Close, so a test that only reads back through the
// same fs could be reading the spool it just wrote.
func TestPluginStorageUploadsOverFTP(t *testing.T) {
	f, p := newPluginFS(t)

	if err := f.MkdirAll("/eklenti/klasor", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	h, err := f.GetHandle("/eklenti/klasor/yeni.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0)
	if err != nil {
		t.Fatalf("GetHandle: %v", err)
	}
	if _, err := h.Write([]byte("merhaba dünya")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, ok := p.Data("klasor/yeni.txt")
	if !ok || string(got) != "merhaba dünya" {
		t.Fatalf("plugin holds %q (present=%v); tree: %v", got, ok, p.Paths())
	}
}

// RNFR/RNTO and DELE. Delete is a soft delete into .filex-trash on the same
// driver, which over a plugin is a protocol move — losing the bytes there
// looks exactly like a successful delete from the client's side.
func TestPluginStorageRenamesAndDeletesOverFTP(t *testing.T) {
	f, p := newPluginFS(t)
	p.Seed("klasor/a.txt", "içerik")

	if err := f.Rename("/eklenti/klasor/a.txt", "/eklenti/klasor/b.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if p.Exists("klasor/a.txt") {
		t.Fatalf("source survived the rename; tree: %v", p.Paths())
	}
	if got, ok := p.Data("klasor/b.txt"); !ok || string(got) != "içerik" {
		t.Fatalf("renamed file holds %q (present=%v); tree: %v", got, ok, p.Paths())
	}

	if err := f.Remove("/eklenti/klasor/b.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if p.Exists("klasor/b.txt") {
		t.Fatalf("file survived the delete; tree: %v", p.Paths())
	}
	var kept bool
	for _, pth := range p.Paths() {
		if b, ok := p.Data(pth); ok && string(b) == "içerik" {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("DELE destroyed the bytes instead of trashing them; tree: %v", p.Paths())
	}
}

// A read-only plugin has no Write method at all, and this surface decides what
// a session may do by type-asserting for one. The failure guarded here is an
// upload that is accepted and spooled and only fails at commit, after the
// client has been told the file is stored.
func TestReadOnlyPluginRefusesUploadsOverFTP(t *testing.T) {
	caps := testplugin.FullCaps()
	caps.Write, caps.Delete = false, false
	f, p := newPluginFSWithCaps(t, caps)
	p.Seed("var.txt", "okunabilir")

	if _, err := f.Stat("/eklenti/var.txt"); err != nil {
		t.Fatalf("Stat on a read-only plugin: %v", err)
	}
	h, err := f.GetHandle("/eklenti/yeni.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0)
	if err == nil {
		_, werr := h.Write([]byte("olmaz"))
		cerr := h.Close()
		t.Fatalf("upload to a read-only plugin was accepted (write=%v close=%v); tree: %v", werr, cerr, p.Paths())
	}
	if p.Exists("yeni.txt") {
		t.Fatalf("read-only plugin was written to; tree: %v", p.Paths())
	}
}
