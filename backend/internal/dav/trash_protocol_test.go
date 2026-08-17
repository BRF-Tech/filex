package dav

// Protocol-surface deletion tests.
//
// A WebDAV DELETE must land in the same filex trash the web UI restores from,
// for a file and for a whole collection, and it must fail closed when the
// caller has no editor rights. The drivers below deliberately withhold
// capabilities so the fallback paths are exercised for real: before the shared
// trash.Put helper, a driver without Mover destroyed the bytes outright even
// though the DB row was left looking restorable.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/testutil/dbtest"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// ───────────────────────── capability-limited drivers ─────────────────────

// noMoveDriver is a local storage with Mover and Copier hidden — the shape of
// a backend that can only write and delete. Trashing it is impossible, so a
// delete here is genuinely permanent and MUST report itself as such.
type noMoveDriver struct{ inner storage.Driver }

func (d *noMoveDriver) Init(ctx context.Context, cfg map[string]any) error {
	inner, err := storage.Get("local")
	if err != nil {
		return err
	}
	d.inner = inner
	return d.inner.Init(ctx, cfg)
}
func (d *noMoveDriver) Name() string { return "nomove" }
func (d *noMoveDriver) List(ctx context.Context, p string) ([]storage.Object, error) {
	return d.inner.List(ctx, p)
}
func (d *noMoveDriver) Stat(ctx context.Context, p string) (storage.Object, error) {
	return d.inner.Stat(ctx, p)
}
func (d *noMoveDriver) Read(ctx context.Context, p string) (io.ReadCloser, error) {
	return d.inner.Read(ctx, p)
}
func (d *noMoveDriver) Capabilities() storage.Capabilities { return d.inner.Capabilities() }
func (d *noMoveDriver) Write(ctx context.Context, p string, r io.Reader, size int64) error {
	return d.inner.(storage.Writer).Write(ctx, p, r, size)
}
func (d *noMoveDriver) Delete(ctx context.Context, p string) error {
	return d.inner.(storage.Deleter).Delete(ctx, p)
}
func (d *noMoveDriver) Mkdir(ctx context.Context, p string) error {
	return d.inner.(storage.Mkdirer).Mkdir(ctx, p)
}

// copyOnlyDriver hides Mover but keeps Copier — the bytes can still be
// preserved (copy into the trash key, then delete the source), so a delete
// here MUST be restorable.
type copyOnlyDriver struct{ noMoveDriver }

func (d *copyOnlyDriver) Name() string { return "copyonly" }
func (d *copyOnlyDriver) Init(ctx context.Context, cfg map[string]any) error {
	inner, err := storage.Get("local")
	if err != nil {
		return err
	}
	d.inner = inner
	return d.inner.Init(ctx, cfg)
}
func (d *copyOnlyDriver) Copy(ctx context.Context, src, dst string) error {
	return d.inner.(storage.Copier).Copy(ctx, src, dst)
}

func init() {
	storage.Register("nomove", func() storage.Driver { return &noMoveDriver{} })
	storage.Register("copyonly", func() storage.Driver { return &copyOnlyDriver{} })
}

// addStorageDriver is addStorage with an explicit driver name.
func (ha *harness) addStorageDriver(t *testing.T, name, driver string) *model.Storage {
	t.Helper()
	root := t.TempDir()
	cfg, _ := json.Marshal(map[string]any{"path": root})
	st, err := ha.store.CreateStorage(context.Background(), &model.Storage{
		Name:       name,
		Driver:     driver,
		MountPath:  "/" + name,
		ConfigJSON: cfg,
		SyncMode:   model.SyncModeOnDemand,
		Enabled:    true,
	})
	require.NoError(t, err)
	return st
}

// storageRoot returns the temp dir backing a storage.
func (ha *harness) storageRoot(t *testing.T, st *model.Storage) string {
	t.Helper()
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(st.ConfigJSON, &cfg))
	return cfg["path"].(string)
}

// trashedCopies returns the on-disk `.filex-trash` files matching base.
func trashedCopies(t *testing.T, root, glob string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(root, trash.Prefix, glob))
	require.NoError(t, err)
	return m
}

// ────────────────────────────────── tests ─────────────────────────────────

// A driver that can neither Move nor Copy has no way to preserve the bytes.
// The delete is then genuinely permanent — and the important part is that it
// says so: the DB row must NOT be left sitting in the trash listing offering a
// Restore that cannot work.
func TestDeleteOnUntrashableDriverIsPermanentAndSaysSo(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorageDriver(t, "nomove", "nomove")
	root := ha.storageRoot(t, st)

	resp := ha.req(t, http.MethodPut, "/dav/nomove/a.txt", ha.adminEmail, ha.adminPass, "veri", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = ha.req(t, http.MethodDelete, "/dav/nomove/a.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, err := os.Stat(filepath.Join(root, "a.txt"))
	require.True(t, os.IsNotExist(err), "bytes are gone — this driver cannot trash")

	// No phantom trash row: nothing may claim to be restorable.
	entries, _, err := trash.New(ha.store, ha.resolver, nil).List(context.Background(), &st.ID, 100, 0)
	require.NoError(t, err)
	require.Empty(t, entries, "a permanent delete must not leave a restorable-looking trash entry")
}

// A driver with Copy but no Move CAN preserve the bytes, so DELETE must trash
// them rather than destroy them. Red evidence: before the shared helper this
// fell into the legacy hard-delete branch and the file was unrecoverable.
func TestDeleteOnCopyOnlyDriverLandsInTrash(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorageDriver(t, "copyonly", "copyonly")
	root := ha.storageRoot(t, st)
	ctx := context.Background()

	resp := ha.req(t, http.MethodPut, "/dav/copyonly/rapor.txt", ha.adminEmail, ha.adminPass, "önemli", nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	resp = ha.req(t, http.MethodDelete, "/dav/copyonly/rapor.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Gone from its old place…
	_, err := os.Stat(filepath.Join(root, "rapor.txt"))
	require.True(t, os.IsNotExist(err))

	// …but preserved in the trash, byte for byte.
	matches := trashedCopies(t, root, "*__rapor.txt")
	require.Len(t, matches, 1, "a Copy-capable driver must trash, not destroy")
	data, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	require.Equal(t, "önemli", string(data))

	// And it is restorable through the same trash service the web UI uses.
	entries, _, lerr := trash.New(ha.store, ha.resolver, nil).List(ctx, &st.ID, 100, 0)
	require.NoError(t, lerr)
	require.Len(t, entries, 1)
	require.Equal(t, "/rapor.txt", entries[0].Path)

	require.NoError(t, trash.New(ha.store, ha.resolver, nil).Restore(ctx, entries[0].ID))
	restored, err := os.ReadFile(filepath.Join(root, "rapor.txt"))
	require.NoError(t, err)
	require.Equal(t, "önemli", string(restored))
}

// A whole collection deleted over WebDAV must be restorable as one unit: one
// Restore call on the folder brings the folder and its children back.
func TestDeleteCollectionRestoresAsOneUnit(t *testing.T) {
	ha := newHarness(t)
	st := ha.addStorage(t, "depo", false, false)
	root := ha.storageRoot(t, st)
	ctx := context.Background()

	require.Equal(t, http.StatusCreated,
		ha.req(t, "MKCOL", "/dav/depo/proje", ha.adminEmail, ha.adminPass, "", nil).StatusCode)
	require.Equal(t, http.StatusCreated,
		ha.req(t, "MKCOL", "/dav/depo/proje/alt", ha.adminEmail, ha.adminPass, "", nil).StatusCode)
	require.Equal(t, http.StatusCreated,
		ha.req(t, http.MethodPut, "/dav/depo/proje/bir.txt", ha.adminEmail, ha.adminPass, "1", nil).StatusCode)
	require.Equal(t, http.StatusCreated,
		ha.req(t, http.MethodPut, "/dav/depo/proje/alt/iki.txt", ha.adminEmail, ha.adminPass, "2", nil).StatusCode)

	dirNode := ha.nodeByPath(t, st.ID, "proje")
	require.NotNil(t, dirNode)

	resp := ha.req(t, http.MethodDelete, "/dav/depo/proje", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Bytes preserved under the trash key, sub-structure intact.
	matches := trashedCopies(t, root, "*__proje")
	require.Len(t, matches, 1)
	for _, rel := range []string{"bir.txt", filepath.Join("alt", "iki.txt")} {
		_, err := os.Stat(filepath.Join(matches[0], rel))
		require.NoError(t, err, "child %s must ride along into the trash", rel)
	}

	// ONE restore of the folder row brings the whole tree back.
	require.NoError(t, trash.New(ha.store, ha.resolver, nil).Restore(ctx, dirNode.ID))
	for _, rel := range []string{"bir.txt", filepath.Join("alt", "iki.txt")} {
		_, err := os.Stat(filepath.Join(root, "proje", rel))
		require.NoError(t, err, "child %s must come back with the folder", rel)
	}
	child := ha.nodeByPath(t, st.ID, "proje/bir.txt")
	require.NotNil(t, child)
	require.Nil(t, child.DeletedAt)
}

// Deletion must fail closed. A read-only storage and a viewer-level grant both
// answer 403 (the pre-gate decides, so the answer is deterministic — the
// FileSystem alone would surface 404/405) and MUST NOT touch the bytes.
func TestDeleteFailsClosedAndKeepsBytes(t *testing.T) {
	ha := newHarness(t)
	ctx := context.Background()

	// (a) read-only storage
	ro := ha.addStorage(t, "salt", true, false)
	roRoot := ha.storageRoot(t, ro)
	require.NoError(t, os.WriteFile(filepath.Join(roRoot, "sabit.txt"), []byte("dokunma"), 0o644))

	resp := ha.req(t, http.MethodDelete, "/dav/salt/sabit.txt", ha.adminEmail, ha.adminPass, "", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	data, err := os.ReadFile(filepath.Join(roRoot, "sabit.txt"))
	require.NoError(t, err)
	require.Equal(t, "dokunma", string(data))
	require.Empty(t, trashedCopies(t, roRoot, "*__sabit.txt"), "a refused delete must not trash anything")

	// (b) RBAC storage where the caller is only a viewer
	rb := ha.addStorage(t, "gizli", false, true)
	rbRoot := ha.storageRoot(t, rb)
	require.NoError(t, os.WriteFile(filepath.Join(rbRoot, "rapor.txt"), []byte("gizli"), 0o644))

	uid := dbtest.SeedUserWithRole(t, ha.store, "izleyici@test.local", "ViewerPass!1", model.RoleUser)
	_, err = ha.store.CreateFileGrant(ctx, &model.FileGrant{
		StorageID:  rb.ID,
		UserID:     uid,
		PathPrefix: "",
		Level:      model.GrantViewer,
	})
	require.NoError(t, err)

	resp = ha.req(t, http.MethodDelete, "/dav/gizli/rapor.txt", "izleyici@test.local", "ViewerPass!1", "", nil)
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "viewer grant must not delete")
	data, err = os.ReadFile(filepath.Join(rbRoot, "rapor.txt"))
	require.NoError(t, err)
	require.Equal(t, "gizli", string(data))
	require.Empty(t, trashedCopies(t, rbRoot, "*__rapor.txt"))
}

// ───────────────────────────── hook correctness ───────────────────────────

// hookSink captures the events writehook emits.
type hookSink struct {
	notify.Service
	ch chan notify.Event
}

func (h *hookSink) Send(_ context.Context, e notify.Event) (int64, error) {
	select {
	case h.ch <- e:
	default:
	}
	return 1, nil
}

// captureHooks installs a sink for the duration of one test.
func captureHooks(t *testing.T) *hookSink {
	t.Helper()
	s := &hookSink{ch: make(chan notify.Event, 16)}
	writehook.Configure(nil, s)
	t.Cleanup(func() { writehook.Configure(nil, nil) })
	return s
}

// waitFor returns the first captured event of one of the wanted types.
func (h *hookSink) waitFor(t *testing.T, want ...notify.EventType) notify.Event {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case e := <-h.ch:
			for _, w := range want {
				if e.Event == w {
					return e
				}
			}
		case <-deadline:
			t.Fatalf("no %v event within timeout", want)
		}
	}
}

// The hook must describe what actually happened to the bytes. A trashed file
// reports file.trashed with the trash location; a genuinely destroyed one
// reports file.deleted. Reversing these silently changes what webhooks and
// notify tell people — a "trashed" notification for bytes nobody can restore.
func TestDeleteFiresHookMatchingWhatHappened(t *testing.T) {
	t.Run("trashed", func(t *testing.T) {
		sink := captureHooks(t)
		ha := newHarness(t)
		ha.addStorage(t, "depo", false, false)

		require.Equal(t, http.StatusCreated,
			ha.req(t, http.MethodPut, "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "x", nil).StatusCode)
		require.Equal(t, http.StatusNoContent,
			ha.req(t, http.MethodDelete, "/dav/depo/a.txt", ha.adminEmail, ha.adminPass, "", nil).StatusCode)

		e := sink.waitFor(t, notify.EventFileTrashed, notify.EventFileDeleted)
		require.Equal(t, notify.EventFileTrashed, e.Event, "bytes are restorable → file.trashed")
		require.Equal(t, writehook.OriginDAV, e.Meta["origin"])
		require.Contains(t, e.Meta["trash_path"], trash.Prefix)
	})

	t.Run("permanently deleted", func(t *testing.T) {
		sink := captureHooks(t)
		ha := newHarness(t)
		ha.addStorageDriver(t, "nomove", "nomove")

		require.Equal(t, http.StatusCreated,
			ha.req(t, http.MethodPut, "/dav/nomove/a.txt", ha.adminEmail, ha.adminPass, "x", nil).StatusCode)
		require.Equal(t, http.StatusNoContent,
			ha.req(t, http.MethodDelete, "/dav/nomove/a.txt", ha.adminEmail, ha.adminPass, "", nil).StatusCode)

		e := sink.waitFor(t, notify.EventFileTrashed, notify.EventFileDeleted)
		require.Equal(t, notify.EventFileDeleted, e.Event, "bytes are gone → file.deleted")
		require.Equal(t, writehook.OriginDAV, e.Meta["origin"])
		require.NotContains(t, e.Meta, "trash_path")
	})
}
