package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/db"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

// Renaming onto a name that is already taken.
//
// ⚠⚠ Measured before this guard existed: renaming `a.txt` to `b.txt` in a
// folder that already held a `b.txt` answered 200. The rename replaced b.txt's
// bytes (a local rename does, and so does an object store's copy-then-delete),
// and the catalogue then HARD-deleted b.txt's row to make room for the moved
// one — taking its version history, shares and comments with it. Nothing went
// to the trash. A move onto a taken name has kept both files since 0.41.0; the
// rename, which a person reaches far more often, was left out.

// renameSpy is the local driver with its Move counted and its Stat optionally
// broken for one path, so a test can prove the refusal happened BEFORE the
// driver was asked to move anything.
type renameSpy struct {
	*local.Driver
	moves    atomic.Int32
	failStat string
}

func (d *renameSpy) Move(ctx context.Context, src, dst string) error {
	d.moves.Add(1)
	return d.Driver.Move(ctx, src, dst)
}

func (d *renameSpy) Stat(ctx context.Context, p string) (storage.Object, error) {
	if d.failStat != "" && strings.Trim(p, "/") == d.failStat {
		return storage.Object{}, errors.New("backend unavailable")
	}
	return d.Driver.Stat(ctx, p)
}

type renameRig struct {
	mh    *handlers.Manager
	store db.Store
	spy   *renameSpy
	st    *model.Storage
	root  string
}

func newRenameRig(t *testing.T) *renameRig {
	t.Helper()
	_, store, drv, st, root := newMutateFixture(t)
	spy := &renameSpy{Driver: drv}
	mh := handlers.NewManager(store, func(int64) (storage.Driver, error) { return spy, nil })
	return &renameRig{mh: mh, store: store, spy: spy, st: st, root: root}
}

// file writes a file on disk and the row a sync would have made for it.
func (r *renameRig) file(t *testing.T, rel, body string) *model.Node {
	t.Helper()
	abs := filepath.Join(r.root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(body), 0o644))
	return r.row(t, rel, model.NodeTypeFile, int64(len(body)))
}

func (r *renameRig) dir(t *testing.T, rel string) *model.Node {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(r.root, filepath.FromSlash(rel)), 0o755))
	return r.row(t, rel, model.NodeTypeDirectory, 0)
}

func (r *renameRig) row(t *testing.T, rel string, kind model.NodeType, size int64) *model.Node {
	t.Helper()
	p := "/" + strings.Trim(rel, "/")
	var parent *int64
	if dir := filepath.ToSlash(filepath.Dir(p)); dir != "/" {
		if pn, _ := r.store.GetNodeByPath(context.Background(), r.st.ID, pathkey.Hash(r.st.ID, dir)); pn != nil {
			id := pn.ID
			parent = &id
		}
	}
	n, err := r.store.CreateNode(context.Background(), &model.Node{
		StorageID: r.st.ID, ParentID: parent, Name: filepath.Base(p), Path: p, StorageKey: p,
		PathHash: pathkey.Hash(r.st.ID, p), Type: kind, Size: size,
	})
	require.NoError(t, err)
	return n
}

func (r *renameRig) rename(t *testing.T, item, name string) *httptest.ResponseRecorder {
	t.Helper()
	return callMutate(t, r.mh, "rename", map[string]any{
		"path": "main://", "item": "main://" + item, "name": name,
	})
}

func (r *renameRig) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(rel)))
	require.NoError(t, err, "nothing at %s", rel)
	return string(b)
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	out := map[string]any{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return out
}

func TestRename_OntoATakenFile_IsRefusedAndKeepsBoth(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	a := r.file(t, "a.txt", "incoming")
	b := r.file(t, "b.txt", "was-here")
	_, err := r.store.CreateNodeVersion(ctx, &model.NodeVersion{NodeID: b.ID, VersionN: 1, StorageKey: ".versions/1/1", Size: 3})
	require.NoError(t, err)

	rec := r.rename(t, "a.txt", "b.txt")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	body := decodeBody(t, rec)
	assert.Equal(t, "NAME_TAKEN", body["code"])
	assert.Equal(t, "b.txt", body["name"])
	assert.NotContains(t, rec.Body.String(), r.root, "an error body must not carry a server path")
	assert.Zero(t, r.spy.moves.Load(), "the refusal must come before the driver is asked to move anything")

	assert.Equal(t, "was-here", r.read(t, "b.txt"), "the file that had the name was overwritten")
	assert.Equal(t, "incoming", r.read(t, "a.txt"))

	kept, err := r.store.GetNode(ctx, b.ID)
	require.NoError(t, err, "the row of the file that had the name was deleted")
	assert.Nil(t, kept.DeletedAt)
	assert.Equal(t, "/b.txt", kept.Path)
	versions, err := r.store.ListNodeVersions(ctx, b.ID)
	require.NoError(t, err)
	assert.Len(t, versions, 1, "its version history went with it")

	src, err := r.store.GetNode(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, "/a.txt", src.Path)
}

// A folder renamed onto another folder's name. On a local disk the rename
// already failed (with the server's absolute paths in the message); on an
// object store the copy MERGED the two, replacing every file that shared a
// name. Refused either way, before anything moves.
func TestRename_OntoATakenFolder_IsRefused(t *testing.T) {
	r := newRenameRig(t)
	r.dir(t, "Belgeler")
	r.file(t, "Belgeler/rapor.txt", "new")
	r.dir(t, "Arsiv")
	r.file(t, "Arsiv/rapor.txt", "old")

	rec := r.rename(t, "Belgeler", "Arsiv")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, "NAME_TAKEN", decodeBody(t, rec)["code"])
	assert.NotContains(t, rec.Body.String(), r.root)
	assert.Zero(t, r.spy.moves.Load())
	assert.Equal(t, "old", r.read(t, "Arsiv/rapor.txt"))
	assert.Equal(t, "new", r.read(t, "Belgeler/rapor.txt"))
}

// Changing only the case of a name is a rename of the item onto itself as far
// as a case-insensitive disk is concerned: Stat("A.txt") answers with "a.txt".
// That must not read as "taken". (On a case-sensitive disk this is an ordinary
// rename to a free name; the test holds on both.)
func TestRename_CaseOnly_IsNotACollision(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	a := r.file(t, "a.txt", "same file")

	rec := r.rename(t, "a.txt", "A.txt")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	entries, err := os.ReadDir(r.root)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	assert.Equal(t, []string{"A.txt"}, names)
	moved, err := r.store.GetNode(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, "/A.txt", moved.Path)
}

// Where the disk can hold both spellings, the other one is a different file.
func TestRename_CaseOnly_OntoARealTwin_IsRefused(t *testing.T) {
	r := newRenameRig(t)
	r.file(t, "probe", "x")
	if _, err := os.Stat(filepath.Join(r.root, "PROBE")); err == nil {
		t.Skip("this filesystem is case-insensitive: it cannot hold a.txt and A.txt side by side")
	}
	r.file(t, "a.txt", "lower")
	r.file(t, "A.txt", "upper")

	rec := r.rename(t, "a.txt", "A.txt")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, "upper", r.read(t, "A.txt"))
	assert.Equal(t, "lower", r.read(t, "a.txt"))
}

// "." and ".." are not names: joined onto the item's folder they point at the
// folder itself or its parent. A local disk refused with the server's paths
// in the message; an object store copied the file ONTO the folder's key.
func TestRename_DotNames_AreRefused(t *testing.T) {
	r := newRenameRig(t)
	r.dir(t, "Leon")
	r.file(t, "Leon/not.txt", "x")
	for _, name := range []string{".", "..", " .. "} {
		rec := r.rename(t, "Leon/not.txt", name)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "%q: %s", name, rec.Body.String())
	}
	assert.Zero(t, r.spy.moves.Load())
	assert.Equal(t, "x", r.read(t, "Leon/not.txt"))
}

// A backend that cannot say whether the name is free has not said it is free.
func TestRename_InconclusiveCheck_IsRefusedWith503(t *testing.T) {
	r := newRenameRig(t)
	r.file(t, "a.txt", "incoming")
	r.spy.failStat = "b.txt"

	rec := r.rename(t, "a.txt", "b.txt")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	assert.Equal(t, "EXISTS_CHECK_FAILED", decodeBody(t, rec)["code"])
	assert.NotContains(t, rec.Body.String(), r.root)
	assert.Zero(t, r.spy.moves.Load())
}

// The listing shows a b.txt (a live row) even though its bytes are gone. The
// rename used to "succeed" by hard-deleting that row — and with it everything
// hanging off it.
func TestRename_OntoALiveRowWithNoBytes_IsRefused(t *testing.T) {
	r := newRenameRig(t)
	ctx := context.Background()
	r.file(t, "a.txt", "incoming")
	ghost := r.row(t, "b.txt", model.NodeTypeFile, 9)

	rec := r.rename(t, "a.txt", "b.txt")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	still, err := r.store.GetNode(ctx, ghost.ID)
	require.NoError(t, err)
	assert.Nil(t, still.DeletedAt)
	assert.Zero(t, r.spy.moves.Load())
}

// The ordinary rename is untouched.
func TestRename_ToAFreeName_StillWorks(t *testing.T) {
	r := newRenameRig(t)
	r.file(t, "a.txt", "content")
	rec := r.rename(t, "a.txt", "c.txt")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "content", r.read(t, "c.txt"))
	assert.EqualValues(t, 1, r.spy.moves.Load())
}
