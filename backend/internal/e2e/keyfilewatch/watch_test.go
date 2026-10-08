package keyfilewatch_test

// Who may erase an encrypted folder's key history, and who is told
// (e2e/slotchange). Before 0.54 any rewrite of `.filex-e2e.json` that changed
// a key slot deleted every earlier version of it, whoever wrote it: an editor
// who did not own the folder could upload `{"v":3,"salt":"x"}` under its name
// and leave the folder with no key file that opens it. And the owner was told
// only when a browser announced the change.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brf-tech/filex/backend/internal/auth"
	"github.com/brf-tech/filex/backend/internal/e2e/keyfilewatch"
	"github.com/brf-tech/filex/backend/internal/model"
	"github.com/brf-tech/filex/backend/internal/notify"
	"github.com/brf-tech/filex/backend/internal/pathkey"
	"github.com/brf-tech/filex/backend/internal/quotastore"
	"github.com/brf-tech/filex/backend/internal/storage"
	"github.com/brf-tech/filex/backend/internal/storage/drivers/local"
)

const (
	kfOne   = `{"v":2,"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="}}`
	kfNames = `{"v":3,"req":["names"],"salt":"c2FsdA==","iter":600000,"verify":"dmVy","fmk":"wrapped","fmk_pw":"cHc=","rk":{"salt":"cms=","blob":"YmxvYg=="},"names":{"alg":"AES-SIV-512"}}`
	kfFake  = `{"v":3,"salt":"x"}`
	kfNewPw = `{"v":3,"req":["names"],"salt":"bmV3","iter":600000,"verify":"bmV3dg==","fmk":"wrapped","fmk_pw":"bmV3cA==","rk":{"salt":"cms=","blob":"YmxvYg=="},"names":{"alg":"AES-SIV-512"}}`

	storageID  = int64(1)
	markerID   = int64(100)
	folderID   = int64(10)
	ownerID    = int64(1)
	editorID   = int64(2)
	adminID    = int64(3)
	markerPath = "Kasa/.filex-e2e.json"
)

var people = map[int64]*model.User{
	ownerID:  {ID: ownerID, Email: "owner@example.com", Role: model.RoleUser},
	editorID: {ID: editorID, Email: "editor@example.com", Role: model.RoleUser},
	adminID:  {ID: adminID, Email: "admin@example.com", Role: model.RoleAdmin},
}

type fakeVersions struct {
	mu      sync.Mutex
	list    []*model.NodeVersion
	deleted []int64
}

func (f *fakeVersions) List(_ context.Context, _ int64) ([]*model.NodeVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*model.NodeVersion(nil), f.list...), nil
}

func (f *fakeVersions) HardDeleteVersion(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, id)
	return nil
}

type fakeAudit struct {
	mu   sync.Mutex
	rows []*model.AuditEntry
}

func (a *fakeAudit) InsertAuditEntry(_ context.Context, e *model.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, e)
	return nil
}

func (a *fakeAudit) action(name string) []*model.AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []*model.AuditEntry
	for _, r := range a.rows {
		if r.Action == name {
			out = append(out, r)
		}
	}
	return out
}

// fakeStore knows one folder, "Kasa", and who owns it (nil: nobody).
type fakeStore struct {
	owner *int64
}

func (s *fakeStore) GetNodeByPath(_ context.Context, sid int64, hash string) (*model.Node, error) {
	if hash == pathkey.Hash(sid, "Kasa") {
		return &model.Node{ID: folderID, StorageID: sid, Path: "Kasa", Name: "Kasa", Type: model.NodeTypeDirectory}, nil
	}
	return nil, nil
}

func (s *fakeStore) GetNodeOwner(_ context.Context, nodeID int64) (*int64, error) {
	if nodeID == folderID {
		return s.owner, nil
	}
	return nil, nil
}

func (s *fakeStore) GetUser(_ context.Context, id int64) (*model.User, error) {
	return people[id], nil
}

func (s *fakeStore) GetStorage(_ context.Context, id int64) (*model.Storage, error) {
	return &model.Storage{ID: id, Name: "main"}, nil
}

type capture struct {
	notify.Service
	got chan notify.Event
}

func (c *capture) Send(_ context.Context, e notify.Event) (int64, error) {
	c.got <- e
	return 1, nil
}

type rig struct {
	watch    *keyfilewatch.Watch
	versions *fakeVersions
	audit    *fakeAudit
	sink     *capture
}

// newRig is a folder "Kasa" whose key file had two earlier versions (kfOne,
// then kfNames - newest first) and is now `live`.
func newRig(t *testing.T, owner *int64, live string) *rig {
	t.Helper()
	drv := &local.Driver{}
	require.NoError(t, drv.Init(context.Background(), map[string]any{"root": t.TempDir()}))
	put := func(key, body string) {
		require.NoError(t, drv.Write(context.Background(), key, strings.NewReader(body), int64(len(body))))
	}
	put(markerPath, live)
	put(".versions/2", kfNames)
	put(".versions/1", kfOne)
	r := &rig{
		versions: &fakeVersions{list: []*model.NodeVersion{
			{ID: 2, NodeID: markerID, VersionN: 2, StorageKey: ".versions/2"},
			{ID: 1, NodeID: markerID, VersionN: 1, StorageKey: ".versions/1"},
		}},
		audit: &fakeAudit{},
		sink:  &capture{got: make(chan notify.Event, 4)},
	}
	r.watch = &keyfilewatch.Watch{
		Audit:    r.audit,
		Versions: r.versions,
		Resolver: func(int64) (storage.Driver, error) { return drv, nil },
		Owners:   &fakeStore{owner: owner},
		Notify:   r.sink,
	}
	return r
}

func (r *rig) write(ctx context.Context) {
	r.watch.OnWritten(ctx, storageID, &model.Node{ID: markerID, StorageID: storageID, Path: markerPath, Name: ".filex-e2e.json", Type: model.NodeTypeFile}, "manager", true)
}

func (r *rig) told(t *testing.T) notify.Event {
	t.Helper()
	select {
	case ev := <-r.sink.got:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("nobody was told")
	}
	return notify.Event{}
}

func (r *rig) toldNobody(t *testing.T) {
	t.Helper()
	select {
	case ev := <-r.sink.got:
		t.Fatalf("nobody should be told, got %v", ev.Event)
	case <-time.After(300 * time.Millisecond):
	}
}

func as(id int64) context.Context {
	return auth.WithUser(context.Background(), people[id])
}

func ptr(v int64) *int64 { return &v }

func TestWatch_AnEditorWhoDoesNotOwnTheFolderCannotEraseItsKeyHistory(t *testing.T) {
	r := newRig(t, ptr(ownerID), kfFake)
	r.write(as(editorID))

	assert.Empty(t, r.versions.deleted, "a key file uploaded by somebody who does not own the folder deletes no version of it")
	rows := r.audit.action("e2e.key_file_rewritten")
	require.Len(t, rows, 1)
	assert.EqualValues(t, 2, rows[0].Metadata["versions_kept"])
	assert.Nil(t, rows[0].Metadata["versions_deleted"])

	// The owner is told - a warning: somebody else changed it.
	ev := r.told(t)
	assert.Equal(t, notify.EventE2EPasswordChanged, ev.Event)
	assert.Equal(t, notify.SeverityWarning, ev.Severity)
	require.NotNil(t, ev.UserID)
	assert.Equal(t, ownerID, *ev.UserID, "the OWNER is told, not the person who wrote it")
	assert.Equal(t, "Kasa", ev.Meta["folder"])
	assert.Equal(t, "main", ev.Meta["storage"])
	assert.EqualValues(t, 2, ev.Meta["versions_kept"])
	assert.Equal(t, "editor@example.com", ev.Meta["actor_email"])

	pw := r.audit.action("e2e.password_change")
	require.Len(t, pw, 1, "the server records the change itself")
	require.NotNil(t, pw[0].UserID)
	assert.Equal(t, editorID, *pw[0].UserID)
	assert.EqualValues(t, 2, pw[0].Metadata["versions_kept"])
}

func TestWatch_TheOwnerRetiresTheOldKeyFiles(t *testing.T) {
	r := newRig(t, ptr(ownerID), kfNewPw)
	r.write(as(ownerID))

	assert.ElementsMatch(t, []int64{1, 2}, r.versions.deleted, "every earlier key file opens the folder with the old password")
	rows := r.audit.action("e2e.key_file_rewritten")
	require.Len(t, rows, 1)
	assert.EqualValues(t, 2, rows[0].Metadata["versions_deleted"])
	assert.Nil(t, rows[0].Metadata["versions_kept"])

	ev := r.told(t)
	assert.Equal(t, notify.SeverityInfo, ev.Severity, "the owner changed it")
	require.NotNil(t, ev.UserID)
	assert.Equal(t, ownerID, *ev.UserID)
	assert.Contains(t, ev.Meta["changes"], "password")
}

func TestWatch_AnAdministratorRetiresThem(t *testing.T) {
	r := newRig(t, ptr(ownerID), kfNewPw)
	r.write(as(adminID))

	assert.ElementsMatch(t, []int64{1, 2}, r.versions.deleted)
	ev := r.told(t)
	assert.Equal(t, notify.SeverityWarning, ev.Severity, "the owner hears of a change somebody else made")
	require.NotNil(t, ev.UserID)
	assert.Equal(t, ownerID, *ev.UserID)
}

// A staged upload's commit runs in the ops worker, with no signed-in account
// on its context: the person it is done for is the writer.
func TestWatch_ABackgroundWriteIsTheWriteOfThePersonItIsDoneFor(t *testing.T) {
	r := newRig(t, ptr(ownerID), kfNewPw)
	ctx := quotastore.WithActor(quotastore.WithOwner(context.Background(), ownerID), ownerID)
	r.write(ctx)
	assert.ElementsMatch(t, []int64{1, 2}, r.versions.deleted)
	_ = r.told(t)

	r = newRig(t, ptr(ownerID), kfNewPw)
	ctx = quotastore.WithActor(quotastore.WithOwner(context.Background(), editorID), editorID)
	r.write(ctx)
	assert.Empty(t, r.versions.deleted)
	_ = r.told(t)

	// Nobody the server can name: nothing deleted.
	r = newRig(t, ptr(ownerID), kfNewPw)
	r.write(context.Background())
	assert.Empty(t, r.versions.deleted)
	_ = r.told(t)
}

// A folder nobody owns: only an administrator retires its key files, and the
// administrators are told (a broadcast).
func TestWatch_AFolderNobodyOwns(t *testing.T) {
	r := newRig(t, nil, kfNewPw)
	r.write(as(editorID))
	assert.Empty(t, r.versions.deleted)
	ev := r.told(t)
	assert.Nil(t, ev.UserID, "nobody owns it: a broadcast the administrators read")
	assert.Equal(t, notify.SeverityWarning, ev.Severity)

	r = newRig(t, nil, kfNewPw)
	r.write(as(adminID))
	assert.ElementsMatch(t, []int64{1, 2}, r.versions.deleted)
	_ = r.told(t)
}

// Only a key-slot change is a password change: a new level is audited, and
// nobody is told.
func TestWatch_ALevelChangeIsNotAPasswordChange(t *testing.T) {
	r := newRig(t, ptr(ownerID), kfNames)
	r.versions.list = r.versions.list[1:]
	r.write(as(editorID))
	assert.Empty(t, r.versions.deleted)
	require.Len(t, r.audit.action("e2e.key_file_rewritten"), 1)
	assert.Empty(t, r.audit.action("e2e.password_change"))
	r.toldNobody(t)
}
