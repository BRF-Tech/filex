package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/acl"
	"github.com/brf-tech/filex/backend/internal/api"
	"github.com/brf-tech/filex/backend/internal/api/handlers"
	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/ops"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
	"github.com/brf-tech/filex/backend/internal/testutil"
	"github.com/brf-tech/filex/backend/internal/trash"
	"github.com/brf-tech/filex/backend/internal/writehook"
)

// Rename and restore, asked with `queued=1`: a job of the operations queue.
//
// Both ran inside the request, and a folder on an object store is one request
// per object: the dialog waited with nothing on screen until the proxy gave
// up, then said the change had failed while the server carried on. Asked with
// `queued=1`, the same checks answer at once — a refusal still in the dialog —
// and what they allow is queued and answered 202.

type queueRig struct {
	*renameRig
	svc *ops.Service
	th  *handlers.Trash
	drv storage.Driver
}

// newQueueRig is the manager and the trash handler over one local storage,
// with the queue wired the way routes.go wires it.
func newQueueRig(t *testing.T) *queueRig {
	t.Helper()
	writehook.ConfigureOverwriteGuard(nil)
	ctx := context.Background()
	sqlDB, store := testutil.NewTestDB(t)
	root := t.TempDir()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(ctx, map[string]any{"root": root}))
	st, err := store.CreateStorage(ctx, &model.Storage{
		Name: "main", Driver: "local", MountPath: "/data", Enabled: true, RBACEnabled: true,
		ConfigJSON: json.RawMessage(`{"root":"` + escapeJSON(root) + `"}`),
	})
	require.NoError(t, err)
	resolve := func(int64) (storage.Driver, error) { return drv, nil }

	mh := handlers.NewManager(store, resolve)
	th := handlers.NewTrash(trash.New(store, resolve, nil), store)
	svc := ops.New(sqlDB, resolve)
	require.NoError(t, svc.Migrate(ctx))
	svc.SetSync(mh)
	svc.SetRestorer(th)
	svc.SetPurger(th)
	mh.AttachOps(svc)
	th.AttachOps(svc)
	return &queueRig{renameRig: &renameRig{mh: mh, store: store, st: st, root: root}, svc: svc, th: th, drv: drv}
}

func (q *queueRig) renameQueued(t *testing.T, item, name string) *httptest.ResponseRecorder {
	t.Helper()
	return mutateQueued(t, q.mh, "rename", map[string]any{"path": "main://", "item": "main://" + item, "name": name})
}

func mutateQueued(t *testing.T, mh *handlers.Manager, action string, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/files/manager?action="+action+"&queued=1", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mh.Mutate(rec, req)
	return rec
}

func (q *queueRig) restoreQueued(t *testing.T, as *model.User, ids ...int64) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"node_ids": ids})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/files/manager/restore?queued=1", bytes.NewReader(raw))
	if as != nil {
		req = req.WithContext(auth.WithUser(req.Context(), as))
	}
	rec := httptest.NewRecorder()
	q.th.Restore(rec, req)
	return rec
}

// drain runs the worker until op id leaves the queue.
func (q *queueRig) drain(t *testing.T, id int64) *ops.Op {
	t.Helper()
	ctx := context.Background()
	run, stop := context.WithCancel(ctx)
	defer stop()
	go q.svc.Run(run)
	defer q.svc.Stop()
	deadline := time.Now().Add(3 * time.Second)
	for {
		op, err := q.svc.Get(ctx, id)
		require.NoError(t, err)
		switch op.Status {
		case ops.StatusOK, ops.StatusFailed, ops.StatusPartial, ops.StatusCancelled:
			return op
		}
		require.True(t, time.Now().Before(deadline), "op %d did not finish (status %s)", id, op.Status)
		time.Sleep(10 * time.Millisecond)
	}
}

func (q *queueRig) queued(t *testing.T) []*ops.Op {
	t.Helper()
	list, err := q.svc.List(context.Background(), "")
	require.NoError(t, err)
	return list
}

func TestRenameQueued_AFolderIsAJobOfTheQueue(t *testing.T) {
	q := newQueueRig(t)
	q.dir(t, "Leon")
	q.file(t, "Leon/a.txt", "a")

	rec := q.renameQueued(t, "Leon", "Leo")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var body struct {
		Op *ops.Op `json:"op"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Op, rec.Body.String())
	assert.Equal(t, ops.OpRename, body.Op.Kind)
	assert.DirExists(t, filepath.Join(q.root, "Leon"), "the rename ran inside the request")

	done := q.drain(t, body.Op.ID)
	require.Equal(t, ops.StatusOK, done.Status, done.Error)
	assert.Equal(t, "a", q.read(t, "Leo/a.txt"))
	_, err := os.Stat(filepath.Join(q.root, "Leon"))
	assert.True(t, os.IsNotExist(err), "the old name is still on the storage")
	row, _ := q.store.GetNodeByPath(context.Background(), q.st.ID, pathkey.Hash(q.st.ID, "/Leo/a.txt"))
	assert.NotNil(t, row, "the catalogue did not follow the rename")
}

// The dialog still hears a taken name at once, and nothing is queued.
func TestRenameQueued_ATakenNameIsRefusedAtOnce(t *testing.T) {
	q := newQueueRig(t)
	q.dir(t, "Leon")
	q.dir(t, "Leo")

	rec := q.renameQueued(t, "Leon", "Leo")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Equal(t, "NAME_TAKEN", decodeBody(t, rec)["code"])
	assert.Empty(t, q.queued(t), "a refused rename was queued")
}

// A server with no queue wired renames inside the request, as it always did.
func TestRenameQueued_WithNoQueueRenamesInTheRequest(t *testing.T) {
	r := newRenameRig(t)
	r.dir(t, "Leon")

	rec := mutateQueued(t, r.mh, "rename", map[string]any{"path": "main://", "item": "main://Leon", "name": "Leo"})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.DirExists(t, filepath.Join(r.root, "Leo"))
}

func (q *queueRig) trashed(t *testing.T, rels ...string) []int64 {
	t.Helper()
	ids := make([]int64, 0, len(rels))
	items := make([]map[string]string, 0, len(rels))
	for _, rel := range rels {
		row, err := q.store.GetNodeByPath(context.Background(), q.st.ID, pathkey.Hash(q.st.ID, "/"+rel))
		require.NoError(t, err)
		ids = append(ids, row.ID)
		items = append(items, map[string]string{"path": "main://" + rel})
	}
	rec := callMutate(t, q.mh, "delete", map[string]any{"path": "main://", "items": items})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return ids
}

func TestRestoreQueued_TheEntriesAreOneJob(t *testing.T) {
	q := newQueueRig(t)
	q.file(t, "a.txt", "a")
	q.file(t, "b.txt", "b")
	ids := q.trashed(t, "a.txt", "b.txt")

	rec := q.restoreQueued(t, nil, ids...)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var body struct {
		Ops []*ops.Op `json:"ops"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Ops, 1, rec.Body.String())
	assert.Equal(t, ops.OpRestore, body.Ops[0].Kind)
	assert.Equal(t, 2, body.Ops[0].Total)

	done := q.drain(t, body.Ops[0].ID)
	require.Equal(t, ops.StatusOK, done.Status, done.Error)
	assert.Equal(t, "a", q.read(t, "a.txt"))
	assert.Equal(t, "b", q.read(t, "b.txt"))
}

// Every entry is judged before anything is queued, the way the explorer's
// restore of one entry is: a batch is not half-allowed.
func TestRestoreQueued_AnEntryTheCallerMayNotWriteRefusesTheBatch(t *testing.T) {
	q := newQueueRig(t)
	q.th.AttachACL(acl.New(q.store))
	q.dir(t, "Ekip")
	q.dir(t, "Gizli")
	q.file(t, "Ekip/a.txt", "a")
	q.file(t, "Gizli/b.txt", "b")
	ids := q.trashed(t, "Ekip/a.txt", "Gizli/b.txt")
	editor := seedSharedUser(t, q.store, "yazar@filex.test", "TestUserPass!1")
	grant(t, q.store, q.st, editor, "Ekip", model.GrantEditor, true)

	rec := q.restoreQueued(t, editor, ids...)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	assert.Empty(t, q.queued(t), "a job was queued for a batch that was refused")
}

// The explorer asks for a queued rename or restore only from a server that
// says it runs them. An older one ignores `queued=1` and renames inside the
// request, which the explorer still handles; a restore batch it would refuse.
func TestCapabilities_SayWhichChangesTheServerQueues(t *testing.T) {
	queuedOf := func(t *testing.T, srv *httptest.Server, client *http.Client) (any, bool) {
		t.Helper()
		resp, err := client.Get(srv.URL + "/api/files/capabilities")
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		out := map[string]any{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		v, ok := out["queued"]
		return v, ok
	}

	withQueue := func(t *testing.T, withTrash bool) (*httptest.Server, *http.Client) {
		srv, client, _ := testutil.NewTestServerWith(t, nil, func(d *api.Deps) {
			opsDB, _ := testutil.NewTestDB(t)
			d.Ops = ops.New(opsDB, d.StorageResolver)
			t.Cleanup(d.Ops.Stop)
			if withTrash {
				d.Trash = trash.New(d.Store, d.StorageResolver, d.Quota)
			}
		})
		return srv, client
	}

	t.Run("with the queue and the trash wired", func(t *testing.T) {
		srv, client := withQueue(t, true)
		got, ok := queuedOf(t, srv, client)
		require.True(t, ok, "the server does not say what it queues")
		assert.Equal(t, []any{"rename", "restore", "purge"}, got)
	})

	t.Run("with no trash, no restore", func(t *testing.T) {
		srv, client := withQueue(t, false)
		got, _ := queuedOf(t, srv, client)
		assert.Equal(t, []any{"rename"}, got)
	})

	t.Run("with no queue", func(t *testing.T) {
		srv, client, _ := testutil.NewTestServer(t)
		_, ok := queuedOf(t, srv, client)
		assert.False(t, ok, "a server with no queue claims to queue")
	})
}

// purgeQueued is DELETE /api/admin/trash/{id}?queued=1.
func purgeQueued(t *testing.T, th *handlers.Trash, id int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/admin/trash/"+strconv.FormatInt(id, 10)+"?queued=1", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", strconv.FormatInt(id, 10))
	rec := httptest.NewRecorder()
	th.Purge(rec, req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx)))
	return rec
}

// A permanent delete from the admin's Trash page ran inside the request: a
// folder is purged one object and one row at a time, and the page's client
// gave up after 30 s. Asked with `queued=1` it is a job of the queue.
func TestPurgeQueued_IsAJobOfTheQueue(t *testing.T) {
	q := newQueueRig(t)
	ctx := context.Background()
	q.dir(t, "Leon")
	q.file(t, "Leon/a.txt", "a")
	ids := q.trashed(t, "Leon")

	rec := purgeQueued(t, q.th, ids[0])
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var body struct {
		Op *ops.Op `json:"op"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Op, rec.Body.String())
	assert.Equal(t, ops.OpPurge, body.Op.Kind)
	_, err := q.store.GetNode(ctx, ids[0])
	require.NoError(t, err, "the entry went inside the request")

	done := q.drain(t, body.Op.ID)
	require.Equal(t, ops.StatusOK, done.Status, done.Error)
	n, err := q.store.GetNode(ctx, ids[0])
	assert.True(t, err != nil || n == nil, "the entry is still in the trash")
}

// A server with no queue wired purges inside the request, as it always did.
func TestPurgeQueued_WithNoQueuePurgesInTheRequest(t *testing.T) {
	q := newQueueRig(t)
	ctx := context.Background()
	q.file(t, "a.txt", "a")
	ids := q.trashed(t, "a.txt")
	resolve := func(int64) (storage.Driver, error) { return q.drv, nil }
	plain := handlers.NewTrash(trash.New(q.store, resolve, nil), q.store)

	rec := purgeQueued(t, plain, ids[0])
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	n, err := q.store.GetNode(ctx, ids[0])
	assert.True(t, err != nil || n == nil, "the entry is still in the trash")
}

// A restore or purge job reaches only the entries of the storage it was queued
// for. The handler judged its entries on that storage; an id that points
// anywhere else did not come from that judgement.
//
// RED PROOF (before queuedEntry, 2026-09-26): a restore job queued for an
// empty storage with another storage's entry id ended `ok`, the entry back
// out of the trash; a purge job the same way ended `ok` with the entry gone.
func TestQueuedTrashJob_AnEntryOfAnotherStorageIsNotItsToTouch(t *testing.T) {
	for _, kind := range []string{ops.OpRestore, ops.OpPurge} {
		t.Run(kind, func(t *testing.T) {
			q := newQueueRig(t)
			ctx := context.Background()
			q.file(t, "a.txt", "a")
			ids := q.trashed(t, "a.txt")
			other, err := q.store.CreateStorage(ctx, &model.Storage{
				Name: "other", Driver: "local", MountPath: "/other", Enabled: true, ConfigJSON: json.RawMessage(`{}`),
			})
			require.NoError(t, err)

			op, err := q.svc.Submit(ctx, kind, other.ID, []string{strconv.FormatInt(ids[0], 10)}, "")
			require.NoError(t, err)
			done := q.drain(t, op.ID)
			assert.Equal(t, ops.StatusFailed, done.Status, "the job touched an entry of another storage")
			n, err := q.store.GetNode(ctx, ids[0])
			require.NoError(t, err, "the entry is gone")
			assert.NotNil(t, n.DeletedAt, "the entry left the trash")
		})
	}
}

// A purge takes out what is in the trash when it runs. An entry restored while
// its purge waited in the queue is a live folder again, and stays one.
//
// RED PROOF (PR #63, 2026-09-26): the queued purge ended `ok` and the restored
// folder's rows were gone — the folder and its file hard-deleted from the
// catalogue (and with them shares, tags, comments and versions), its bytes
// left on the storage where no listing shows them.
func TestPurgeQueued_AnEntryRestoredBeforeItsTurnIsLeftAlone(t *testing.T) {
	q := newQueueRig(t)
	ctx := context.Background()
	q.dir(t, "Leon")
	q.file(t, "Leon/a.txt", "a")
	ids := q.trashed(t, "Leon")

	rec := purgeQueued(t, q.th, ids[0])
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var body struct {
		Op *ops.Op `json:"op"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	// The owner restores it while the purge waits its turn.
	require.NoError(t, q.th.Service.Restore(ctx, ids[0]))

	done := q.drain(t, body.Op.ID)
	assert.NotEqual(t, ops.StatusOK, done.Status, "the purge claims to have purged a live folder")
	n, err := q.store.GetNode(ctx, ids[0])
	require.NoError(t, err, "the restored folder's row is gone")
	assert.Nil(t, n.DeletedAt)
	child, _ := q.store.GetNodeByPath(ctx, q.st.ID, pathkey.Hash(q.st.ID, "/Leon/a.txt"))
	assert.NotNil(t, child, "the restored folder's file lost its row")
	assert.Equal(t, "a", q.read(t, "Leon/a.txt"))
}

// DELETE /api/admin/trash/{id} is for trash entries. A live node's id is one
// that does not exist here, synchronous or queued.
//
// RED PROOF (v0.46.0 and before): the synchronous purge of a live folder's id
// answered 200 and hard-deleted its rows.
func TestPurge_ALiveNodeIsNotATrashEntry(t *testing.T) {
	q := newQueueRig(t)
	ctx := context.Background()
	live := q.dir(t, "Canli")
	q.file(t, "Canli/a.txt", "a")
	resolve := func(int64) (storage.Driver, error) { return q.drv, nil }
	plain := handlers.NewTrash(trash.New(q.store, resolve, nil), q.store)

	for name, th := range map[string]*handlers.Trash{"synchronous": plain, "queued": q.th} {
		rec := purgeQueued(t, th, live.ID)
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s: %s", name, rec.Body.String())
	}
	q.drainAll(t)
	n, err := q.store.GetNode(ctx, live.ID)
	require.NoError(t, err, "the live folder's row is gone")
	assert.Nil(t, n.DeletedAt)
	assert.Equal(t, "a", q.read(t, "Canli/a.txt"))
}

// drainAll runs the worker until nothing is left pending or running.
func (q *queueRig) drainAll(t *testing.T) {
	t.Helper()
	for _, op := range q.queued(t) {
		if op.Status == ops.StatusPending || op.Status == ops.StatusRunning {
			q.drain(t, op.ID)
		}
	}
}

// A purge whose entry is already gone has nothing left to do, and says it is
// done: the second of two administrators purging the same entry, or a purge
// requeued at boot after the process died past the row's deletion.
//
// RED PROOF (PR #63, 2026-09-26): the second job ended `failed` with
// "sql: no rows in result set".
func TestPurgeQueued_AnEntryAlreadyGoneIsDone(t *testing.T) {
	q := newQueueRig(t)
	q.file(t, "a.txt", "a")
	ids := q.trashed(t, "a.txt")

	var queued []int64
	for i := 0; i < 2; i++ {
		rec := purgeQueued(t, q.th, ids[0])
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		var body struct {
			Op *ops.Op `json:"op"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		queued = append(queued, body.Op.ID)
	}
	for _, id := range queued {
		done := q.drain(t, id)
		assert.Equal(t, ops.StatusOK, done.Status, "op %d: %s", id, done.Error)
	}
}

// A queued restore names what it restores on its audit row, the way the
// synchronous one names its entry.
//
// RED PROOF (PR #61, 2026-09-26): the queued answer set no target, so the
// `file.restore` row named nothing — no id, no path.
func TestRestoreQueued_TheAuditRowNamesTheEntries(t *testing.T) {
	q := newQueueRig(t)
	q.file(t, "a.txt", "a")
	q.file(t, "b.txt", "b")
	ids := q.trashed(t, "a.txt", "b.txt")

	audit := func(t *testing.T, ids ...int64) (string, map[string]any) {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"node_ids": ids})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/files/manager/restore?queued=1", bytes.NewReader(raw))
		ctx, d := auth.WithAuditDetail(req.Context())
		rec := httptest.NewRecorder()
		q.th.Restore(rec, req.WithContext(ctx))
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		target, meta := d.ApplyTarget("", nil)
		return target, d.Into(meta)
	}

	target, meta := audit(t, ids[0])
	assert.Equal(t, strconv.FormatInt(ids[0], 10), target, "one entry is named by its id")
	assert.Equal(t, "/a.txt", meta["target_name"], "one entry is named by where it came from")

	_, meta = audit(t, ids[1], ids[0])
	name, _ := meta["target_name"].(string)
	assert.Contains(t, name, "/a.txt")
	assert.Contains(t, name, "/b.txt")
	assert.ElementsMatch(t, []int64{ids[1], ids[0]}, meta["node_ids"], "the batch's ids are in the row")
}

// A queued restore names at most maxRestoreBatch entries: each is judged
// inside the request, and the batch is one row of the queue.
//
// RED PROOF (PR #61, 2026-09-26): 1001 ids were walked one lookup after
// another (the answer was the first unknown id's 404) — nothing bounded the
// list.
func TestRestoreQueued_ABatchIsBounded(t *testing.T) {
	q := newQueueRig(t)
	ids := make([]int64, 1001)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	rec := q.restoreQueued(t, nil, ids...)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Equal(t, "TOO_MANY", decodeBody(t, rec)["code"])
	assert.Empty(t, q.queued(t))
}
