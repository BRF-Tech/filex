package sftpsrv

// SFTP over a PLUGIN-backed storage.
//
// Why this file exists: SFTP is the surface with the least forgiving shape.
// Every read is a ReadAt at an offset the client chose, every write is a
// WriteAt, and both arrive out of order from several concurrent workers — so
// over a plugin, a download is a stream of ranged reads and an upload is a
// spool that is only committed on Close. The plugin package's own tests never
// touch that; they call Read and Write once, in order.
//
// ⚠ These drive the handler set DIRECTLY rather than through a real SSH
// client. The package itself sanctions the seam — session.live's comment says
// it is "nil in tests that drive the handlers directly" — and the reason to
// use it here is that the SSH half (host keys, the handshake, the ban list) is
// identical for every driver and already covered over the wire by
// sftpsrv_test.go. What is plugin-specific starts at fs.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/pkg/sftp"

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

// hostKeyDir is shared by every test in this file. Host-key generation is an
// RSA-3072 keygen, which is the single most expensive thing New() does; the
// keys themselves are irrelevant to what is being measured here, and hostKeys()
// reuses whatever it finds on disk.
var hostKeyDir = func() string {
	d, err := os.MkdirTemp("", "filex-sftp-plugin-hostkeys")
	if err != nil {
		panic(err)
	}
	return d
}()

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
		Enabled: true, Addr: "127.0.0.1:0", HostKeyDir: hostKeyDir, SpoolDir: t.TempDir(),
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

	hash, err := authlocal.HashPassword("SftpPass!1")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := store.CreateUser(ctx, "sftp@example.com", hash, model.RoleAdmin, "en", "UTC")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	principal, err := res.Password(ctx, u.Email, "SftpPass!1")
	if err != nil {
		t.Fatalf("password auth: %v", err)
	}

	h := srv.handlers(&session{principal: principal, login: u.Email})
	f, ok := h.FileList.(*fs)
	if !ok {
		t.Fatalf("handlers did not return the fs: %T", h.FileList)
	}
	return f, p
}

// listAll drains a ListerAt the way the sftp library does.
func listAll(t *testing.T, l sftp.ListerAt) []os.FileInfo {
	t.Helper()
	var out []os.FileInfo
	buf := make([]os.FileInfo, 32)
	for off := int64(0); ; {
		n, err := l.ListAt(buf, off)
		out = append(out, buf[:n]...)
		off += int64(n)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			t.Fatalf("ListAt: %v", err)
		}
		if n == 0 {
			return out
		}
	}
}

// SSH_FXP_READDIR and SSH_FXP_STAT. A client renders the size from the
// listing and uses it to decide when a download is complete, so a size lost on
// the way through the protocol is a download that reports success at zero
// bytes.
func TestPluginStorageListsOverSFTP(t *testing.T) {
	f, p := newPluginFS(t)
	p.Seed("belgeler/rapor.txt", "plugin bytes")
	p.Seed("belgeler/alt/derin.txt", "derin")

	entries := listAll(t, mustList(t, f, "List", "/eklenti/belgeler"))
	if len(entries) != 2 {
		t.Fatalf("READDIR = %d entries, want 2", len(entries))
	}
	for _, fi := range entries {
		switch fi.Name() {
		case "rapor.txt":
			if fi.IsDir() || fi.Size() != 12 {
				t.Fatalf("rapor.txt: dir=%v size=%d", fi.IsDir(), fi.Size())
			}
		case "alt":
			if !fi.IsDir() {
				t.Fatal("alt did not come back as a directory")
			}
		default:
			t.Fatalf("unexpected entry %q", fi.Name())
		}
	}

	st := listAll(t, mustList(t, f, "Stat", "/eklenti/belgeler/rapor.txt"))
	if len(st) != 1 || st[0].Size() != 12 {
		t.Fatalf("STAT = %v", st)
	}
	if !st[0].ModTime().Equal(testplugin.SeedTime) {
		t.Fatalf("STAT mtime = %v, want the plugin's %v", st[0].ModTime(), testplugin.SeedTime)
	}

	if _, err := f.Filelist(sftp.NewRequest("Stat", "/eklenti/yok.txt")); !os.IsNotExist(err) {
		t.Fatalf("missing path error = %v, want os.ErrNotExist", err)
	}
}

func mustList(t *testing.T, f *fs, method, path string) sftp.ListerAt {
	t.Helper()
	l, err := f.Filelist(sftp.NewRequest(method, path))
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return l
}

// The read path is ReadAt, always: an SFTP client asks for windows and asks
// for them out of order across several workers. Over a plugin each of those is
// a ranged read the adapter may be emulating, so a window that quietly starts
// at zero would hand the client the beginning of the file for every request
// and assemble a file that is wrong in a way no checksum-free client notices.
func TestPluginStorageReadsAtOffsetsOverSFTP(t *testing.T) {
	f, p := newPluginFS(t)
	p.Seed("f.bin", "0123456789")

	ra, err := f.Fileread(sftp.NewRequest("Get", "/eklenti/f.bin"))
	if err != nil {
		t.Fatalf("Fileread: %v", err)
	}
	if c, ok := ra.(io.Closer); ok {
		defer c.Close()
	}

	// Out of order on purpose: the tail before the head.
	buf := make([]byte, 4)
	n, err := ra.ReadAt(buf, 6)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt(6): %v", err)
	}
	if string(buf[:n]) != "6789" {
		t.Fatalf("ReadAt(6,4) = %q, want 6789", buf[:n])
	}
	n, err = ra.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt(0): %v", err)
	}
	if string(buf[:n]) != "0123" {
		t.Fatalf("ReadAt(0,4) = %q, want 0123", buf[:n])
	}
}

// The write path is WriteAt into a spool, committed on Close. Asserting
// through the plugin's own tree is the point: an fs that never commits reads
// back perfectly from the spool it just wrote while the backend holds nothing.
func TestPluginStorageWritesOverSFTP(t *testing.T) {
	f, p := newPluginFS(t)

	if err := f.Filecmd(sftp.NewRequest("Mkdir", "/eklenti/klasor")); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	w, err := f.Filewrite(sftp.NewRequest("Put", "/eklenti/klasor/yeni.txt"))
	if err != nil {
		t.Fatalf("Filewrite: %v", err)
	}
	body := []byte("merhaba dünya")
	// Second half first: SFTP clients write out of order and the spool has to
	// cope, which is the whole reason there is a spool.
	if _, err := w.WriteAt(body[7:], 7); err != nil {
		t.Fatalf("WriteAt tail: %v", err)
	}
	if _, err := w.WriteAt(body[:7], 0); err != nil {
		t.Fatalf("WriteAt head: %v", err)
	}
	c, ok := w.(io.Closer)
	if !ok {
		t.Fatalf("writer does not close: %T", w)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, ok2 := p.Data("klasor/yeni.txt")
	if !ok2 || string(got) != string(body) {
		t.Fatalf("plugin holds %q (present=%v); tree: %v", got, ok2, p.Paths())
	}
}

// Rename and Remove. Remove is a soft delete into .filex-trash on the same
// driver, so over a plugin it is a protocol move; the bytes disappearing there
// is indistinguishable from a successful delete at the client.
func TestPluginStorageRenamesAndRemovesOverSFTP(t *testing.T) {
	f, p := newPluginFS(t)
	p.Seed("klasor/a.txt", "içerik")

	r := sftp.NewRequest("Rename", "/eklenti/klasor/a.txt")
	r.Target = "/eklenti/klasor/b.txt"
	if err := f.Filecmd(r); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if p.Exists("klasor/a.txt") {
		t.Fatalf("source survived the rename; tree: %v", p.Paths())
	}
	if got, ok := p.Data("klasor/b.txt"); !ok || string(got) != "içerik" {
		t.Fatalf("renamed file holds %q (present=%v); tree: %v", got, ok, p.Paths())
	}

	if err := f.Filecmd(sftp.NewRequest("Remove", "/eklenti/klasor/b.txt")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if p.Exists("klasor/b.txt") {
		t.Fatalf("file survived the remove; tree: %v", p.Paths())
	}
	var kept bool
	for _, pth := range p.Paths() {
		if b, ok := p.Data(pth); ok && string(b) == "içerik" {
			kept = true
		}
	}
	if !kept {
		t.Fatalf("Remove destroyed the bytes instead of trashing them; tree: %v", p.Paths())
	}
}

// A read-only plugin is a value without a Write method, and this surface
// decides what a session may do by type-asserting for one. The failure guarded
// is an upload accepted, spooled, and refused only at commit — after the
// client has been told the file is stored.
func TestReadOnlyPluginRefusesWritesOverSFTP(t *testing.T) {
	caps := testplugin.FullCaps()
	caps.Write, caps.Delete = false, false
	f, p := newPluginFSWithCaps(t, caps)
	p.Seed("var.txt", "okunabilir")

	if _, err := f.Filelist(sftp.NewRequest("Stat", "/eklenti/var.txt")); err != nil {
		t.Fatalf("Stat on a read-only plugin: %v", err)
	}
	w, err := f.Filewrite(sftp.NewRequest("Put", "/eklenti/yeni.txt"))
	if err == nil {
		if c, ok := w.(io.Closer); ok {
			_ = c.Close()
		}
		t.Fatalf("upload to a read-only plugin was accepted; tree: %v", p.Paths())
	}
	if p.Exists("yeni.txt") {
		t.Fatalf("read-only plugin was written to; tree: %v", p.Paths())
	}
}
