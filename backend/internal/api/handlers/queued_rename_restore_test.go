package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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
	mh.AttachOps(svc)
	th.AttachOps(svc)
	return &queueRig{renameRig: &renameRig{mh: mh, store: store, st: st, root: root}, svc: svc, th: th}
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
		assert.Equal(t, []any{"rename", "restore"}, got)
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
