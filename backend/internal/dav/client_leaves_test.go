package dav

// A WebDAV client that gives up half-way through a folder's DELETE or MOVE.
//
// ⚠⚠ The HTTP API was fixed for this (PR #60, storage.DetachMutation); WebDAV
// was not: RemoveAll's trash.Put and Rename's Move ran on the request's
// context, which net/http cancels the moment the client hangs up — a DAV
// client's own timeout on a slow DELETE of a large folder is the common case.
// On an object store a folder is changed one object at a time, and the SDK
// refuses every request after that point: the folder was left half in the
// trash, or half at its new name.

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// hangUp is a test's hold on one storage: `first` closes once the folder's
// first object has moved, and the storage waits for `gone` — the test closes
// it after hanging up the client — before it goes on.
type hangUp struct {
	first chan struct{}
	gone  chan struct{}
	once  sync.Once
}

var hangUps sync.Map // storage root -> *hangUp

// objectStoreDAV is the local driver changing a folder the way an object store
// does: one object after another, each refused once its context is cancelled,
// as the SDK refuses a request it has not sent yet.
type objectStoreDAV struct {
	*local.Driver
	root string
}

func init() {
	storage.Register("objectstore-dav", func() storage.Driver { return &objectStoreDAV{Driver: &local.Driver{}} })
}

func (d *objectStoreDAV) Name() string { return "objectstore-dav" }

func (d *objectStoreDAV) Init(ctx context.Context, cfg map[string]any) error {
	d.root, _ = cfg["path"].(string)
	return d.Driver.Init(ctx, cfg)
}

func (d *objectStoreDAV) Move(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	from := filepath.Join(d.root, filepath.FromSlash(strings.Trim(src, "/")))
	fi, err := os.Stat(from)
	if err != nil || !fi.IsDir() {
		return d.Driver.Move(ctx, src, dst)
	}
	var files []string
	if err := filepath.WalkDir(from, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			rel, _ := filepath.Rel(from, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return err
	}); err != nil {
		return err
	}
	for i, rel := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := d.Driver.Move(ctx, path.Join(src, rel), path.Join(dst, rel)); err != nil {
			return err
		}
		if i == 0 {
			d.afterFirst(ctx)
		}
	}
	return os.RemoveAll(from)
}

// afterFirst lets the test hang up, then gives the server the moment it needs
// to notice.
func (d *objectStoreDAV) afterFirst(ctx context.Context) {
	v, ok := hangUps.Load(d.root)
	if !ok {
		return
	}
	h := v.(*hangUp)
	h.once.Do(func() { close(h.first) })
	<-h.gone
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
	}
}

// leaveHalfWay sends method on target and hangs up once the folder's first
// object has moved.
func (ha *harness) leaveHalfWay(t *testing.T, root, method, target string, hdr map[string]string) {
	t.Helper()
	h := &hangUp{first: make(chan struct{}), gone: make(chan struct{})}
	hangUps.Store(root, h)
	t.Cleanup(func() { hangUps.Delete(root) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, ha.srv.URL+target, nil)
	require.NoError(t, err)
	req.SetBasicAuth(ha.adminEmail, ha.adminPass)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := ha.srv.Client().Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	select {
	case <-h.first:
	case <-time.After(10 * time.Second):
		t.Fatalf("the %s never reached the folder", method)
	}
	cancel()
	<-done
	close(h.gone)
}

func (ha *harness) seedFolder(t *testing.T, storageName string) {
	t.Helper()
	require.Equal(t, http.StatusCreated, ha.req(t, "MKCOL", "/dav/"+storageName+"/proje", ha.adminEmail, ha.adminPass, "", nil).StatusCode)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		require.Equal(t, http.StatusCreated,
			ha.req(t, http.MethodPut, "/dav/"+storageName+"/proje/"+name, ha.adminEmail, ha.adminPass, "içerik "+name, nil).StatusCode)
	}
}

// eventually polls cond for a few seconds: the server finishes after the
// client has gone.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// RED PROOF (2026-09-26, before storage.DetachMutation reached WebDAV): the
// folder was left with two of its three files at its old place and one in the
// trash.
func TestDAVDelete_AFolderIsTrashedWholeWhenTheClientLeaves(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorageDriver(t, "nesne", "objectstore-dav")
	root := ha.storageRoot(t, st)
	ha.seedFolder(t, "nesne")

	ha.leaveHalfWay(t, root, http.MethodDelete, "/dav/nesne/proje", nil)

	eventually(t, "the folder was left half in the trash", func() bool {
		_, err := os.Stat(filepath.Join(root, "proje"))
		return os.IsNotExist(err)
	})
	trashed, err := filepath.Glob(filepath.Join(root, trash.Prefix, "*__proje", "*.txt"))
	require.NoError(t, err)
	assert.Len(t, trashed, 3, "every file of the folder is in the trash")
}

// RED PROOF (2026-09-26, before storage.DetachMutation reached WebDAV): one
// file at the new name, two left at the old.
func TestDAVMove_AFolderArrivesWholeWhenTheClientLeaves(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorageDriver(t, "nesne", "objectstore-dav")
	root := ha.storageRoot(t, st)
	ha.seedFolder(t, "nesne")

	ha.leaveHalfWay(t, root, "MOVE", "/dav/nesne/proje",
		map[string]string{"Destination": ha.srv.URL + "/dav/nesne/yeni"})

	eventually(t, "the folder was left in two places", func() bool {
		_, err := os.Stat(filepath.Join(root, "proje"))
		return os.IsNotExist(err)
	})
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		b, err := os.ReadFile(filepath.Join(root, "yeni", name))
		require.NoError(t, err, "%s did not arrive", name)
		assert.Equal(t, "içerik "+name, string(b))
	}
}
