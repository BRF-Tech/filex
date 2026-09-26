package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/trash"
)

// A client that goes away half-way through a folder.
//
// ⚠⚠ Measured before the fix: a folder rename, move, delete, restore or purge
// ran under r.Context(), which net/http cancels the moment the client's
// connection closes. A person closing the tab is one way; the common one is a
// proxy that stops waiting — nginx after 60 s by default, Cloudflare after
// 100 s, the admin SPA's own 30 s — or an agent giving up on a slow tool call.
// On an object store a folder is changed one object at a time, each a request
// the SDK refuses once its context is cancelled. So the folder was left in two
// places — half renamed, half in the trash, half restored — with the catalogue
// still describing the old one. A retry of the rename was then refused as
// NAME_TAKEN and a retry of the restore as EXISTS, because the half that had
// arrived already held the name.

// objectStore is the local driver made to change a folder the way the S3
// driver's copyDir does: one object after another, each refused once the
// context is cancelled, as the SDK refuses a request it has not sent yet.
// Its client goes away after the first object.
type objectStore struct {
	*local.Driver
	root   string
	client *client
}

// client is whose request it is: every storage of a rig shares one.
type client struct{ leave context.CancelFunc }

func (d *objectStore) step() {
	if leave := d.client.leave; leave != nil {
		d.client.leave = nil
		leave()
	}
}

func (d *objectStore) abs(p string) string {
	return filepath.Join(d.root, filepath.FromSlash(strings.Trim(p, "/")))
}

func (d *objectStore) Move(ctx context.Context, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	from := d.abs(src)
	if fi, err := os.Stat(from); err != nil || !fi.IsDir() {
		err := d.Driver.Move(ctx, src, dst)
		d.step()
		return err
	}
	var files []string
	if err := filepath.WalkDir(from, func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			files = append(files, p)
		}
		return err
	}); err != nil {
		return err
	}
	to := d.abs(dst)
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("copy-dir %s: %w", strings.TrimPrefix(f, d.root), err)
		}
		target := filepath.Join(to, strings.TrimPrefix(f, from))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.Rename(f, target); err != nil {
			return err
		}
		d.step()
	}
	// No folder is left on an object store once its objects are gone.
	return os.RemoveAll(from)
}

func (d *objectStore) Write(ctx context.Context, p string, body io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := d.Driver.Write(ctx, p, body, size)
	d.step()
	return err
}

func (d *objectStore) Delete(ctx context.Context, p string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := d.Driver.Delete(ctx, p)
	d.step()
	return err
}

func (d *objectStore) List(ctx context.Context, p string) ([]storage.Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.Driver.List(ctx, p)
}

func (d *objectStore) Stat(ctx context.Context, p string) (storage.Object, error) {
	if err := ctx.Err(); err != nil {
		return storage.Object{}, err
	}
	return d.Driver.Stat(ctx, p)
}

type leavingRig struct {
	*renameRig
	client *client
	stores map[int64]*objectStore
	th     *handlers.Trash
}

func newLeavingRig(t *testing.T) *leavingRig {
	t.Helper()
	_, store, drv, st, root := newMutateFixture(t)
	c := &client{}
	r := &leavingRig{client: c, stores: map[int64]*objectStore{st.ID: {Driver: drv, root: root, client: c}}}
	r.renameRig = &renameRig{mh: handlers.NewManager(store, r.resolve), store: store, st: st, root: root}
	r.th = handlers.NewTrash(trash.New(store, r.resolve, nil), store)
	return r
}

func (r *leavingRig) resolve(id int64) (storage.Driver, error) {
	if d, ok := r.stores[id]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("unknown storage %d", id)
}

// another adds a second storage and returns its root on disk.
func (r *leavingRig) another(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": root}))
	st, err := r.store.CreateStorage(context.Background(), &model.Storage{
		Name: name, Driver: "local", MountPath: "/" + name, Enabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(root) + `"}`),
	})
	require.NoError(t, err)
	r.stores[st.ID] = &objectStore{Driver: drv, root: root, client: r.client}
	return root
}

// leon is a folder of three files, on the storage and in the catalogue.
func (r *leavingRig) leon(t *testing.T) *model.Node {
	t.Helper()
	dir := r.dir(t, "Leon")
	for _, n := range leonFiles {
		r.file(t, "Leon/"+n, n)
	}
	return dir
}

var leonFiles = []string{"a.txt", "b.txt", "c.txt"}

// leaving is a request whose client goes away once a storage has done the
// first object of the work.
func (r *leavingRig) leaving(method, target string, body any) *http.Request {
	raw, _ := json.Marshal(body)
	ctx, cancel := context.WithCancel(context.Background())
	r.client.leave = cancel
	req := httptest.NewRequest(method, target, bytes.NewReader(raw)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func (r *leavingRig) trashLeon(t *testing.T) {
	t.Helper()
	rec := callMutate(t, r.mh, "delete", map[string]any{
		"path": "main://", "items": []map[string]string{{"path": "main://Leon"}},
	})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func (r *leavingRig) onDisk(rel string) bool {
	_, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(rel)))
	return err == nil
}

// inTrash counts the files under `.filex-trash/`.
func (r *leavingRig) inTrash(t *testing.T) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(r.root, ".filex-trash"), func(_ string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			n++
		}
		return err
	})
	if err != nil && !os.IsNotExist(err) {
		require.NoError(t, err)
	}
	return n
}

func (r *leavingRig) rowAt(p string) *model.Node {
	n, err := r.store.GetNodeByPath(context.Background(), r.st.ID, pathkey.Hash(r.st.ID, p))
	if err != nil {
		return nil
	}
	return n
}

func assertWholeAt(t *testing.T, r *leavingRig, dir string) {
	t.Helper()
	for _, n := range leonFiles {
		assert.True(t, r.onDisk(dir+"/"+n), "%s is not in %s: the folder was left in two places", n, dir)
		assert.NotNil(t, r.rowAt("/"+dir+"/"+n), "the catalogue has no %s in %s", n, dir)
	}
}

func TestRename_AFolderIsRenamedWholeWhenTheClientLeavesHalfWay(t *testing.T) {
	r := newLeavingRig(t)
	r.leon(t)

	r.mh.Mutate(httptest.NewRecorder(), r.leaving(http.MethodPost, "/api/files/manager?action=rename",
		map[string]any{"path": "main://", "item": "main://Leon", "name": "Leo"}))

	assertWholeAt(t, r, "Leo")
	assert.False(t, r.onDisk("Leon"), "part of the folder stayed under its old name")
	assert.Nil(t, r.rowAt("/Leon"), "the catalogue still has the old name")
}

func TestMove_AFolderArrivesWholeWhenTheClientLeavesHalfWay(t *testing.T) {
	r := newLeavingRig(t)
	r.dir(t, "Arsiv")
	r.leon(t)

	r.mh.Mutate(httptest.NewRecorder(), r.leaving(http.MethodPost, "/api/files/manager?action=move",
		map[string]any{"path": "main://Arsiv", "items": []map[string]string{{"path": "main://Leon"}}}))

	assertWholeAt(t, r, "Arsiv/Leon")
	assert.False(t, r.onDisk("Leon"), "part of the folder stayed where it was")
	assert.Nil(t, r.rowAt("/Leon"), "the catalogue still has the folder where it was")
}

func TestDelete_AFolderGoesToTheTrashWholeWhenTheClientLeavesHalfWay(t *testing.T) {
	r := newLeavingRig(t)
	leon := r.leon(t)

	r.mh.Mutate(httptest.NewRecorder(), r.leaving(http.MethodPost, "/api/files/manager?action=delete",
		map[string]any{"path": "main://", "items": []map[string]string{{"path": "main://Leon"}}}))

	assert.False(t, r.onDisk("Leon"), "part of the folder was left in place")
	assert.Equal(t, 3, r.inTrash(t), "the trash does not hold the whole folder")
	got, err := r.store.GetNode(context.Background(), leon.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.DeletedAt, "the catalogue still lists the folder as live")
}

func TestTrashRestore_AFolderComesBackWholeWhenTheClientLeavesHalfWay(t *testing.T) {
	r := newLeavingRig(t)
	leon := r.leon(t)
	r.trashLeon(t)
	require.Equal(t, 3, r.inTrash(t))

	r.th.Restore(httptest.NewRecorder(), r.leaving(http.MethodPost, "/api/files/manager/restore",
		map[string]any{"node_id": leon.ID}))

	for _, n := range leonFiles {
		assert.True(t, r.onDisk("Leon/"+n), "%s did not come back", n)
	}
	assert.Zero(t, r.inTrash(t), "part of the folder was left in the trash")
	got, err := r.store.GetNode(context.Background(), leon.ID)
	require.NoError(t, err)
	assert.Nil(t, got.DeletedAt, "the catalogue still has the folder in the trash")
	assert.Equal(t, "/Leon", got.Path)
}

func TestTrashPurge_AFolderIsPurgedWholeWhenTheClientLeavesHalfWay(t *testing.T) {
	r := newLeavingRig(t)
	leon := r.leon(t)
	r.trashLeon(t)
	require.Equal(t, 3, r.inTrash(t))
	var kids []int64
	rows, _, err := r.store.ListTrashed(context.Background(), &r.st.ID, 50, 0)
	require.NoError(t, err)
	for _, n := range rows {
		if n.ID != leon.ID {
			kids = append(kids, n.ID)
		}
	}
	require.Len(t, kids, 3)

	req := r.leaving(http.MethodDelete, "/api/admin/trash/"+strconv.FormatInt(leon.ID, 10), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(leon.ID, 10))
	r.th.Purge(httptest.NewRecorder(), req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))

	assert.Zero(t, r.inTrash(t), "part of the folder's bytes were left behind")
	for _, id := range append(kids, leon.ID) {
		n, err := r.store.GetNode(context.Background(), id)
		assert.True(t, err != nil || n == nil, "row %d is still in the trash", id)
	}
}

// The agent surface: `POST /api/ai/move` and `/api/ai/delete`, and the MCP
// `file_move` and `file_delete` tools, which run the same code under the MCP
// call's context. An agent that gives up on a slow call is a client leaving.

func (r *leavingRig) ai() *handlers.AI {
	return handlers.NewAI(r.store, r.resolve, nil, "", nil)
}

func TestAIMove_AFolderIsMovedWholeWhenTheAgentLeavesHalfWay(t *testing.T) {
	r := newLeavingRig(t)
	r.leon(t)

	r.ai().Move(httptest.NewRecorder(), r.leaving(http.MethodPost, "/api/ai/move",
		map[string]any{"src": "main://Leon", "dst": "main://Leo"}))

	assertWholeAt(t, r, "Leo")
	assert.False(t, r.onDisk("Leon"), "part of the folder stayed under its old name")
	assert.Nil(t, r.rowAt("/Leon"), "the catalogue still has the old name")
}

func TestAIMove_AFolderCrossesStoragesWholeWhenTheAgentLeavesHalfWay(t *testing.T) {
	r := newLeavingRig(t)
	r.leon(t)
	cold := r.another(t, "cold")

	r.ai().Move(httptest.NewRecorder(), r.leaving(http.MethodPost, "/api/ai/move",
		map[string]any{"src": "main://Leon", "dst": "cold://Leon"}))

	for _, n := range leonFiles {
		assert.FileExists(t, filepath.Join(cold, "Leon", n), "%s did not arrive", n)
	}
	assert.False(t, r.onDisk("Leon"), "the source is still there")
}

func TestAIDelete_AFolderGoesToTheTrashWholeWhenTheAgentLeavesHalfWay(t *testing.T) {
	r := newLeavingRig(t)
	leon := r.leon(t)

	r.ai().Delete(httptest.NewRecorder(), r.leaving(http.MethodPost, "/api/ai/delete",
		map[string]any{"path": "main://Leon"}))

	assert.False(t, r.onDisk("Leon"), "part of the folder was left in place")
	assert.Equal(t, 3, r.inTrash(t), "the trash does not hold the whole folder")
	got, err := r.store.GetNode(context.Background(), leon.ID)
	require.NoError(t, err)
	assert.NotNil(t, got.DeletedAt, "the catalogue still lists the folder as live")
}
