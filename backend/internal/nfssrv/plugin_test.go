package nfssrv

// NFS over a PLUGIN-backed storage.
//
// Why this file exists: the NFS surface presents storage as a billy.Filesystem
// with POSIX manners — an os.FileInfo per entry, an open handle that reads at
// offsets and writes at offsets, a root whose mtime must not move. A plugin
// answers over a wire and knows nothing about any of that, so the adapter in
// between is doing real work: a listing becomes []os.FileInfo, a ranged read
// becomes ReadAt, a write becomes spool-then-commit. None of it runs in the
// plugin package's own tests.
//
// ⚠ These drive the fs layer DIRECTLY rather than through a real NFS client,
// deliberately. The package's existing wire tests document why a mount is
// expensive here: the go-nfs client binds its own random local port out of
// 49152-65535 with nothing reserving it first, which cost 2 failures in 5 runs
// before a retry loop was added. Everything below the mount handshake — which
// nfssrv_test.go already covers over the wire for the local driver — is
// identical for any driver, so a plugin adds nothing by paying that cost
// again. What IS plugin-specific is the driver-facing half, and that is
// exactly what fs exposes.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/go-git/go-billy/v5"

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

// newPluginFS stands up the real server (New does not listen) on a storage
// backed by a running plugin, and returns the filesystem an accepted mount
// would be handed.
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
		Enabled: true, Store: store, Auth: res, ACL: acl.New(store),
		Body: filebody.New(store, nil), Quota: quota.New(store), SpoolDir: t.TempDir(),
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

	hash, err := authlocal.HashPassword("NfsPass!1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(ctx, "nfs@example.com", hash, model.RoleAdmin, "en", "UTC")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	principal, err := res.Password(ctx, u.Email, "NfsPass!1")
	if err != nil {
		t.Fatalf("password auth: %v", err)
	}

	// An export naming the storage: the mount is rooted inside it, which is
	// how anybody actually mounts one bucket.
	return newFS(srv, principal, &model.NFSExport{StorageName: "eklenti"}), p
}

func names(fis []os.FileInfo) []string {
	out := make([]string, 0, len(fis))
	for _, fi := range fis {
		out = append(out, fi.Name())
	}
	return out
}

// A directory listing has to carry name, size and dir-ness across the protocol
// hop. NFS clients cache attributes aggressively: a file that lists as 0 bytes
// is a file the client will happily report as empty to `cat` without ever
// asking again.
func TestPluginStorageListsOverNFS(t *testing.T) {
	f, p := newPluginFS(t)
	p.Seed("belgeler/rapor.txt", "plugin bytes")
	p.Seed("belgeler/alt/derin.txt", "derin")

	fis, err := f.ReadDir("/belgeler")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	got := names(fis)
	if len(got) != 2 {
		t.Fatalf("ReadDir = %v, want alt and rapor.txt", got)
	}
	for _, fi := range fis {
		switch fi.Name() {
		case "rapor.txt":
			if fi.IsDir() || fi.Size() != 12 {
				t.Fatalf("rapor.txt: dir=%v size=%d, want file of 12", fi.IsDir(), fi.Size())
			}
		case "alt":
			if !fi.IsDir() {
				t.Fatal("alt did not come back as a directory")
			}
		default:
			t.Fatalf("unexpected entry %q", fi.Name())
		}
	}

	st, err := f.Stat("/belgeler/rapor.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if st.Size() != 12 || st.IsDir() {
		t.Fatalf("Stat = size %d dir %v", st.Size(), st.IsDir())
	}
	if !st.ModTime().Equal(testplugin.SeedTime) {
		t.Fatalf("Stat mtime = %v, want the plugin's %v", st.ModTime(), testplugin.SeedTime)
	}

	if _, err := f.Stat("/belgeler/yok.txt"); !os.IsNotExist(err) {
		t.Fatalf("missing path error = %v, want os.ErrNotExist", err)
	}
}

// Open + ReadAt is how an NFS READ arrives: never "give me the file", always
// "give me this window". Over a plugin that becomes a ranged read the adapter
// may be emulating, so both the offset and the length have to be right — a
// window that silently starts at zero returns plausible bytes for the wrong
// part of the file, which no client can detect.
func TestPluginStorageReadsAtOffsetsOverNFS(t *testing.T) {
	f, p := newPluginFS(t)
	p.Seed("f.bin", "0123456789")

	h, err := f.Open("/f.bin")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer h.Close()

	buf := make([]byte, 4)
	n, err := h.ReadAt(buf, 3)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAt: %v", err)
	}
	if string(buf[:n]) != "3456" {
		t.Fatalf("ReadAt(3,4) = %q, want 3456", buf[:n])
	}

	// A read that starts at EOF is a short answer, not a failure — clients ask
	// for it at the end of every sequential transfer.
	if _, err := h.ReadAt(buf, 10); err != nil && err != io.EOF {
		t.Fatalf("ReadAt at EOF = %v, want nil or io.EOF", err)
	}
}

// Create → Write → Close is the whole NFS write path, and the commit happens
// on Close. The assertion looks at the plugin's own tree because a write that
// spools and never commits leaves an fs that reads back correctly from its own
// spool while the backend holds nothing.
func TestPluginStorageWritesOverNFS(t *testing.T) {
	f, p := newPluginFS(t)

	h, err := f.Create("/yeni.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.Write([]byte("merhaba dünya")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, ok := p.Data("yeni.txt")
	if !ok || string(got) != "merhaba dünya" {
		t.Fatalf("plugin holds %q (present=%v); tree: %v", got, ok, p.Paths())
	}
}

// MkdirAll, Rename and Remove — the three mutations a file manager mounted
// over NFS performs constantly. Remove is a soft delete into .filex-trash on
// the same driver, so over a plugin it is a protocol move; losing the bytes
// there is indistinguishable from a successful delete at the client.
func TestPluginStorageMutatesOverNFS(t *testing.T) {
	f, p := newPluginFS(t)

	if err := f.MkdirAll("/klasor", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	h, err := f.Create("/klasor/a.txt")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := h.Write([]byte("içerik")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := f.Rename("/klasor/a.txt", "/klasor/b.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if p.Exists("klasor/a.txt") {
		t.Fatalf("source survived the rename; tree: %v", p.Paths())
	}
	if got, ok := p.Data("klasor/b.txt"); !ok || string(got) != "içerik" {
		t.Fatalf("renamed file holds %q (present=%v); tree: %v", got, ok, p.Paths())
	}

	if err := f.Remove("/klasor/b.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if p.Exists("klasor/b.txt") {
		t.Fatalf("file survived the remove; tree: %v", p.Paths())
	}
	var trashed bool
	for _, pth := range p.Paths() {
		if b, ok := p.Data(pth); ok && string(b) == "içerik" {
			trashed = true
		}
	}
	if !trashed {
		t.Fatalf("Remove destroyed the bytes instead of trashing them; tree: %v", p.Paths())
	}
}

// A read-only plugin is handed to filex as a value without a Write method, and
// this surface decides what a mount may do by type-asserting exactly that. The
// failure this guards is a write that is accepted, spooled, and only fails at
// commit — by which point the client has been told the file is saved.
func TestReadOnlyPluginRefusesWritesOverNFS(t *testing.T) {
	caps := testplugin.FullCaps()
	caps.Write, caps.Delete = false, false
	f, p := newPluginFSWithCaps(t, caps)
	p.Seed("var.txt", "okunabilir")

	if _, err := f.Stat("/var.txt"); err != nil {
		t.Fatalf("Stat on a read-only plugin: %v", err)
	}
	h, err := f.Create("/yeni.txt")
	if err == nil {
		_, werr := h.Write([]byte("olmaz"))
		cerr := h.Close()
		t.Fatalf("Create on a read-only plugin succeeded (write=%v close=%v); tree: %v", werr, cerr, p.Paths())
	}
	// The refusal arrives as billy.ErrNotSupported (the fs maps
	// storage.ErrUnsupported that way), not as a permission error — recorded
	// here because the distinction is invisible from the client and a future
	// change of dialect should be a deliberate one.
	if !errors.Is(err, billy.ErrNotSupported) && !os.IsPermission(err) {
		t.Fatalf("Create error = %v, want a refusal", err)
	}
	if p.Exists("yeni.txt") {
		t.Fatalf("read-only plugin was written to; tree: %v", p.Paths())
	}
}
