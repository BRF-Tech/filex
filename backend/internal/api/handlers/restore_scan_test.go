package handlers_test

// Restoring is a write, and until now it was the only write in filex that
// nothing ever scanned.
//
// Two ways bytes go live without passing an upload surface:
//
//   - a version restore, where the bytes come out of `.versions/`. The
//     antivirus job's Eligible() skips that prefix on purpose — every
//     destructive write takes a snapshot, so scanning each one would multiply
//     the scan load by the edit rate — which left overwrite-with-clean,
//     roll-back-to-infected as a way to put an infected file live on an
//     install where every upload is scanned.
//
//   - a trash restore, which is the SAME operation the antivirus job's
//     quarantine can be undone by: quarantine and a user deletion produce an
//     identical row, so "restore from trash" includes "release a file ClamAV
//     condemned".
//
// Both now enqueue a scan of the restored file, asynchronously, exactly the
// way an upload does.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/queue"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/versioning"
)

// infectedMarker is what the stand-in scanner condemns. A scanner that says
// "infected" to everything would prove only that something ran; this one has
// to actually read the bytes that ended up live, so a restore that scanned
// the wrong content (the pre-restore file, say) fails the test.
const infectedMarker = "EICAR-STANDIN-PAYLOAD"

type markerScanner struct {
	mu    sync.Mutex
	reads []string
}

func (m *markerScanner) Supports() bool { return true }

func (m *markerScanner) Scan(_ context.Context, r io.Reader) (bool, string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return false, "", err
	}
	m.mu.Lock()
	m.reads = append(m.reads, string(b))
	m.mu.Unlock()
	if strings.Contains(string(b), infectedMarker) {
		return true, "Standin.Test-Signature", nil
	}
	return false, "", nil
}

func (m *markerScanner) scanned() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.reads...)
}

// restoreFixture wires a local storage plus the two restore handlers, with the
// real antivirus job installed as the process-wide scan sink. The sink runs
// the job inline instead of queueing it, so the test observes the verdict
// rather than the enqueue — the queue's own delivery is proven elsewhere.
type restoreFixture struct {
	store   db.Store
	st      *model.Storage
	drv     *local.Driver
	root    string
	scanner *markerScanner
	vers    *handlers.Versions
	versSvc *versioning.Service
	trashH  *handlers.Trash
}

func newRestoreFixture(t *testing.T, scanOnRestore bool) *restoreFixture {
	t.Helper()
	ctx := context.Background()
	_, store := testutil.NewTestDB(t)
	root := t.TempDir()

	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))

	st, err := store.CreateStorage(ctx, &model.Storage{
		Name:       "main",
		Driver:     "local",
		MountPath:  "/data",
		Enabled:    true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(root) + `"}`),
	})
	require.NoError(t, err)

	resolver := func(id int64) (storage.Driver, error) {
		if id != st.ID {
			return nil, fmt.Errorf("unknown storage %d", id)
		}
		return drv, nil
	}

	sc := &markerScanner{}
	job := queue.NewAntivirusScanner(store, resolver, sc, nil, nil, 0)

	if scanOnRestore {
		handlers.SetAntivirusEnqueue(func(c context.Context, n *model.Node) {
			_ = job.Handle(c, queue.Op{
				Type:    queue.TypeAntivirusScan,
				Payload: map[string]any{"node_id": n.ID},
			})
		})
	} else {
		// The pre-fix world: a sink is wired and works for uploads, and the
		// restore paths simply never reach it.
		handlers.SetAntivirusEnqueue(nil)
	}
	t.Cleanup(func() { handlers.SetAntivirusEnqueue(nil) })

	versSvc := versioning.New(store, resolver)
	vh := handlers.NewVersions(store, versSvc)
	th := handlers.NewTrash(trash.New(store, resolver, nil), store)

	return &restoreFixture{
		store: store, st: st, drv: drv, root: root,
		scanner: sc, vers: vh, versSvc: versSvc, trashH: th,
	}
}

// seedFile writes bytes to the storage and catalogues them.
func (f *restoreFixture) seedFile(t *testing.T, rel, content string) *model.Node {
	t.Helper()
	abs := filepath.Join(f.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	clean := "/" + strings.TrimPrefix(rel, "/")
	n, err := f.store.CreateNode(context.Background(), &model.Node{
		StorageID:  f.st.ID,
		Name:       filepath.Base(rel),
		Path:       clean,
		PathHash:   pathkey.Hash(f.st.ID, clean),
		StorageKey: clean,
		Type:       model.NodeTypeFile,
		Size:       int64(len(content)),
	})
	require.NoError(t, err)
	return n
}

// overwrite replaces the live bytes and refreshes the cached size, the way a
// save does.
func (f *restoreFixture) overwrite(t *testing.T, n *model.Node, content string) {
	t.Helper()
	abs := filepath.Join(f.root, filepath.FromSlash(strings.TrimPrefix(n.Path, "/")))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	require.NoError(t, f.store.UpdateNodeMeta(context.Background(), n.ID,
		int64(len(content)), n.Mime, "etag-"+content, n.DBMtime))
}

func (f *restoreFixture) postJSON(t *testing.T, h http.HandlerFunc, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/restore", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// quarantined reports whether the node is soft-deleted into `.filex-trash/`,
// which is the whole of what quarantine means in the catalogue.
func (f *restoreFixture) quarantined(t *testing.T, id int64) bool {
	t.Helper()
	n, err := f.store.GetNode(context.Background(), id)
	require.NoError(t, err)
	return n.DeletedAt != nil && strings.HasPrefix(n.Path, "/"+trash.Prefix+"/")
}

// ---------------------------------------------------------------------------
// version restore
// ---------------------------------------------------------------------------

func TestVersionRestoreScansTheRestoredBytes(t *testing.T) {
	ctx := context.Background()
	f := newRestoreFixture(t, true)

	// An infected file arrives, is snapshotted, then overwritten with something
	// clean. The infected bytes now live only in `.versions/`, where nothing
	// scans them.
	n := f.seedFile(t, "gelen/rapor.doc", "invoice "+infectedMarker)
	v, err := f.versSvc.Snapshot(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, v)
	f.overwrite(t, n, "a perfectly ordinary invoice")

	rec := f.postJSON(t, f.vers.Restore, map[string]any{"node_id": n.ID, "version_id": v.ID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	reads := f.scanner.scanned()
	require.Len(t, reads, 1, "the restore must scan exactly the file it restored")
	assert.Contains(t, reads[0], infectedMarker, "the scan must read the RESTORED bytes")

	assert.True(t, f.quarantined(t, n.ID),
		"an infected version was restored and left live")
	onDisk, err := filepath.Glob(filepath.Join(f.root, trash.Prefix, "*__rapor.doc"))
	require.NoError(t, err)
	assert.Len(t, onDisk, 1, "the infected bytes must be parked in the trash")
}

func TestVersionRestoreOfACleanVersionIsLeftAlone(t *testing.T) {
	ctx := context.Background()
	f := newRestoreFixture(t, true)

	n := f.seedFile(t, "notlar.txt", "first draft")
	v, err := f.versSvc.Snapshot(ctx, n.ID)
	require.NoError(t, err)
	require.NotNil(t, v)
	f.overwrite(t, n, "second draft")

	rec := f.postJSON(t, f.vers.Restore, map[string]any{"node_id": n.ID, "version_id": v.ID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Len(t, f.scanner.scanned(), 1, "a clean restore is still scanned")
	assert.False(t, f.quarantined(t, n.ID), "a clean verdict must have no side effects")
}

// ---------------------------------------------------------------------------
// trash restore — the neighbouring path, and the stranger case: the file may
// be in the trash BECAUSE it was found infected.
// ---------------------------------------------------------------------------

// quarantineNow runs the real antivirus job over a node, which is how an
// infected file gets into the trash in the first place.
func (f *restoreFixture) quarantineNow(t *testing.T, id int64) {
	t.Helper()
	job := queue.NewAntivirusScanner(f.store,
		func(sid int64) (storage.Driver, error) {
			if sid != f.st.ID {
				return nil, fmt.Errorf("unknown storage %d", sid)
			}
			return f.drv, nil
		}, f.scanner, nil, nil, 0)
	require.NoError(t, job.Handle(context.Background(), queue.Op{
		Type:    queue.TypeAntivirusScan,
		Payload: map[string]any{"node_id": id},
	}))
	require.True(t, f.quarantined(t, id), "precondition: the file must be quarantined")
}

func TestTrashRestoreRescansAQuarantinedFile(t *testing.T) {
	f := newRestoreFixture(t, true)

	n := f.seedFile(t, "gelen/fatura.doc", "payload "+infectedMarker)
	f.quarantineNow(t, n.ID)
	before := len(f.scanner.scanned())

	rec := f.postJSON(t, f.trashH.Restore, map[string]any{"node_id": n.ID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Greater(t, len(f.scanner.scanned()), before,
		"restoring from the trash released the file without scanning it")
	assert.True(t, f.quarantined(t, n.ID),
		"a file quarantined for being infected must not survive a restore")

	// It went back to the trash under a FRESH key rather than being left at
	// its original path.
	back, err := f.store.GetNode(context.Background(), n.ID)
	require.NoError(t, err)
	assert.Equal(t, "/gelen/fatura.doc", back.StorageKey)
	_, err = os.Stat(filepath.Join(f.root, "gelen", "fatura.doc"))
	assert.True(t, os.IsNotExist(err), "the infected bytes must not be live at the original path")
}

func TestTrashRestoreOfACleanFileIsLeftAlone(t *testing.T) {
	ctx := context.Background()
	f := newRestoreFixture(t, true)

	n := f.seedFile(t, "belgeler/sozlesme.txt", "nothing to see here")
	out, err := trash.Put(ctx, f.drv, "belgeler/sozlesme.txt")
	require.NoError(t, err)
	require.True(t, out.Trashed)
	trashClean := "/" + strings.Trim(out.Key, "/")
	require.NoError(t, f.store.SoftDeleteAndRetag(ctx, n.ID, trashClean,
		pathkey.Hash(f.st.ID, trashClean), n.Path))

	rec := f.postJSON(t, f.trashH.Restore, map[string]any{"node_id": n.ID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.Len(t, f.scanner.scanned(), 1, "a restore from the trash is scanned")
	assert.False(t, f.quarantined(t, n.ID), "a clean file must stay restored")
	_, err = os.Stat(filepath.Join(f.root, "belgeler", "sozlesme.txt"))
	assert.NoError(t, err, "the bytes must be back at the original path")
}

// A folder restored from the trash brings its whole subtree back live. Scanning
// only the row the user clicked would protect the single-file case and miss the
// one that carries more files.
func TestTrashRestoreOfAFolderScansTheFilesInside(t *testing.T) {
	ctx := context.Background()
	f := newRestoreFixture(t, true)

	dir, err := f.store.CreateNode(ctx, &model.Node{
		StorageID: f.st.ID, Name: "arsiv", Path: "/arsiv",
		PathHash: pathkey.Hash(f.st.ID, "/arsiv"), StorageKey: "/arsiv",
		Type: model.NodeTypeDirectory,
	})
	require.NoError(t, err)
	child := f.seedFile(t, "arsiv/eski.doc", "old "+infectedMarker)
	require.NoError(t, f.store.MoveNode(ctx, child.ID, &dir.ID, "eski.doc",
		"/arsiv/eski.doc", pathkey.Hash(f.st.ID, "/arsiv/eski.doc")))

	out, err := trash.Put(ctx, f.drv, "arsiv")
	require.NoError(t, err)
	require.True(t, out.Trashed)
	trashClean := "/" + strings.Trim(out.Key, "/")
	require.NoError(t, f.store.SoftDeleteAndRetag(ctx, dir.ID, trashClean,
		pathkey.Hash(f.st.ID, trashClean), "/arsiv"))

	rec := f.postJSON(t, f.trashH.Restore, map[string]any{"node_id": dir.ID})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	reads := f.scanner.scanned()
	require.NotEmpty(t, reads, "the file inside the restored folder was never scanned")
	assert.Contains(t, strings.Join(reads, "\n"), infectedMarker)
	assert.True(t, f.quarantined(t, child.ID),
		"the infected file inside the restored folder must be quarantined")
}

// ---------------------------------------------------------------------------
// the shape of the gap, stated as a test
// ---------------------------------------------------------------------------

// The antivirus job refuses `.versions/` outright, and that is deliberate — it
// is what makes snapshotting cheap. This pins the reason restore has to carry
// the scan: nothing else will.
func TestVersionsPrefixIsStillOutOfScopeForTheScanner(t *testing.T) {
	f := newRestoreFixture(t, true)
	job := queue.NewAntivirusScanner(f.store, nil, f.scanner, nil, nil, 0)

	assert.False(t, job.Eligible(&model.Node{
		Type: model.NodeTypeFile, Size: 10, Path: "/.versions/42/1",
	}), "snapshots are not scanned; the restore is")
	assert.False(t, job.Eligible(&model.Node{
		Type: model.NodeTypeFile, Size: 10, Path: "/" + trash.Prefix + "/1-ab__x.doc",
	}), "trashed bytes are not scanned; the restore is")
	assert.True(t, job.Eligible(&model.Node{
		Type: model.NodeTypeFile, Size: 10, Path: "/gelen/x.doc",
	}))
}
